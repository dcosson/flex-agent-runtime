------------------------ MODULE agent_loop_service ------------------------
(*
 * Layer 2 service-level TLA+ model for AgentLoopService.
 *
 * This builds on Layer 1 (specs/agent_loop.tla) by abstracting each session to
 * simple lifecycle states and focusing on service-level concurrency:
 *   - Close() two-phase drain
 *   - maxSess capacity limits
 *   - session registry invariants
 *   - WaitGroup consistency (safety property 7)
 *
 * Sessions are modeled as {idle, active, destroyed}. Turn start/complete are
 * atomic actions that update both session state and WaitGroup accounting.
 *)

EXTENDS Integers, FiniteSets, TLC

CONSTANTS
    Sessions,      \* Session IDs (e.g., {s1, s2, s3})
    MaxSess,       \* Service maxSess capacity
    InitSessions,  \* Initially registered sessions
    MaxTurnStarts, \* Bound turn-start operations to keep state space tractable
    MaxSessionOps  \* Bound create/destroy operations to keep state space tractable

SessionStates == {"idle", "active", "destroyed"}
ClosePhases == {"none", "phase1", "waiting", "phase2", "done"}

VARIABLES
    sessState,       \* [Sessions -> SessionStates]
    registry,        \* Active service registry entries
    turnPending,     \* [Sessions -> BOOLEAN], whether turnDone callback exists
    borrowed,        \* [Sessions -> BOOLEAN], caller captured ms pointer pre-close
    lateStartUsed,   \* [Sessions -> BOOLEAN], bounds late-arriving post-close start
    serviceClosed,   \* service closed flag
    closePhase,      \* none/phase1/waiting/phase2/done
    closeTargets,    \* Snapshot of sessions seen at Close() begin
    phase1Done,      \* Subset of closeTargets processed in phase 1
    phase2Done,      \* Subset of closeTargets processed in phase 2
    wg,              \* WaitGroup count
    starts,          \* Number of wg.Add calls
    dones,           \* Number of wg.Done calls
    sessionOps       \* Number of create/destroy operations

vars == <<sessState, registry, turnPending, borrowed, lateStartUsed,
          serviceClosed, closePhase, closeTargets, phase1Done, phase2Done,
          wg, starts, dones, sessionOps>>

Init ==
    /\ InitSessions \subseteq Sessions
    /\ MaxSess \in Nat
    /\ MaxTurnStarts \in Nat
    /\ MaxSessionOps \in Nat
    /\ Cardinality(InitSessions) <= MaxSess
    /\ sessState = [s \in Sessions |-> IF s \in InitSessions THEN "idle" ELSE "destroyed"]
    /\ registry = InitSessions
    /\ turnPending = [s \in Sessions |-> FALSE]
    /\ borrowed = [s \in Sessions |-> FALSE]
    /\ lateStartUsed = [s \in Sessions |-> FALSE]
    /\ serviceClosed = FALSE
    /\ closePhase = "none"
    /\ closeTargets = {}
    /\ phase1Done = {}
    /\ phase2Done = {}
    /\ wg = 0
    /\ starts = 0
    /\ dones = 0
    /\ sessionOps = 0

\* -----------------------------------------------------------------------
\* Session lifecycle / turn actions
\* -----------------------------------------------------------------------

CreateSession(s) ==
    /\ s \in Sessions
    /\ ~serviceClosed
    /\ closePhase = "none"
    /\ s \notin registry
    /\ sessState[s] = "destroyed"
    /\ sessionOps < MaxSessionOps
    /\ Cardinality(registry) < MaxSess
    /\ registry' = registry \cup {s}
    /\ sessState' = [sessState EXCEPT ![s] = "idle"]
    /\ borrowed' = [borrowed EXCEPT ![s] = FALSE]
    /\ lateStartUsed' = [lateStartUsed EXCEPT ![s] = FALSE]
    /\ sessionOps' = sessionOps + 1
    /\ UNCHANGED <<turnPending, serviceClosed, closePhase, closeTargets,
                   phase1Done, phase2Done, wg, starts, dones>>

AcquireHandle(s) ==
    /\ s \in Sessions
    /\ closePhase = "none"
    /\ s \in registry
    /\ sessState[s] \in {"idle", "active"}
    /\ ~borrowed[s]
    /\ borrowed' = [borrowed EXCEPT ![s] = TRUE]
    /\ UNCHANGED <<sessState, registry, turnPending, lateStartUsed,
                   serviceClosed, closePhase, closeTargets, phase1Done, phase2Done,
                   wg, starts, dones, sessionOps>>

StartTurn(s) ==
    /\ s \in Sessions
    /\ ~serviceClosed
    /\ closePhase = "none"
    /\ s \in registry
    /\ sessState[s] = "idle"
    /\ ~turnPending[s]
    /\ starts < MaxTurnStarts
    /\ sessState' = [sessState EXCEPT ![s] = "active"]
    /\ turnPending' = [turnPending EXCEPT ![s] = TRUE]
    /\ wg' = wg + 1
    /\ starts' = starts + 1
    /\ UNCHANGED <<registry, borrowed, lateStartUsed, serviceClosed,
                   closePhase, closeTargets, phase1Done, phase2Done, dones, sessionOps>>

CompleteTurn(s) ==
    /\ s \in Sessions
    /\ turnPending[s]
    /\ turnPending' = [turnPending EXCEPT ![s] = FALSE]
    /\ wg' = wg - 1
    /\ dones' = dones + 1
    /\ sessState' =
         IF sessState[s] = "active"
         THEN [sessState EXCEPT ![s] = "idle"]
         ELSE sessState
    /\ UNCHANGED <<registry, borrowed, lateStartUsed, serviceClosed,
                   closePhase, closeTargets, phase1Done, phase2Done, starts, sessionOps>>

DestroySession(s) ==
    /\ s \in Sessions
    /\ ~serviceClosed
    /\ closePhase = "none"
    /\ s \in registry
    /\ sessionOps < MaxSessionOps
    /\ registry' = registry \ {s}
    /\ sessState' = [sessState EXCEPT ![s] = "destroyed"]
    /\ IF turnPending[s]
       THEN /\ turnPending' = [turnPending EXCEPT ![s] = FALSE]
            /\ wg' = wg - 1
            /\ dones' = dones + 1
       ELSE /\ UNCHANGED <<turnPending, wg, dones>>
    /\ sessionOps' = sessionOps + 1
    /\ UNCHANGED <<borrowed, lateStartUsed, serviceClosed, closePhase,
                   closeTargets, phase1Done, phase2Done, starts>>

\* -----------------------------------------------------------------------
\* Close() two-phase drain (service.go section 2.9 in plan)
\* -----------------------------------------------------------------------

CloseBegin ==
    /\ ~serviceClosed
    /\ closePhase = "none"
    /\ serviceClosed' = TRUE
    /\ closePhase' = "phase1"
    /\ closeTargets' = registry
    /\ phase1Done' = {}
    /\ phase2Done' = {}
    /\ UNCHANGED <<sessState, registry, turnPending, borrowed, lateStartUsed,
                   wg, starts, dones, sessionOps>>

ClosePhase1Step(s) ==
    /\ s \in Sessions
    /\ closePhase = "phase1"
    /\ s \in closeTargets \ phase1Done
    /\ phase1Done' = phase1Done \cup {s}
    /\ registry' = registry \ {s}
    /\ sessState' = [sessState EXCEPT ![s] = "destroyed"]
    /\ IF turnPending[s]
       THEN /\ turnPending' = [turnPending EXCEPT ![s] = FALSE]
            /\ wg' = wg - 1
            /\ dones' = dones + 1
       ELSE /\ UNCHANGED <<turnPending, wg, dones>>
    /\ UNCHANGED <<borrowed, lateStartUsed, serviceClosed, closePhase,
                   closeTargets, phase2Done, starts, sessionOps>>

ClosePhase1Finish ==
    /\ closePhase = "phase1"
    /\ phase1Done = closeTargets
    /\ closePhase' = "waiting"
    /\ UNCHANGED <<sessState, registry, turnPending, borrowed, lateStartUsed,
                   serviceClosed, closeTargets, phase1Done, phase2Done,
                   wg, starts, dones, sessionOps>>

\* Late-arriving turn: caller captured ms pointer before close, then starts
\* turn after phase 1. This increments wg and installs a new turnDone.
LateStartTurn(s) ==
    /\ s \in Sessions
    /\ serviceClosed
    /\ closePhase = "waiting"
    /\ s \in closeTargets
    /\ borrowed[s]
    /\ ~lateStartUsed[s]
    /\ ~turnPending[s]
    /\ starts < MaxTurnStarts
    /\ turnPending' = [turnPending EXCEPT ![s] = TRUE]
    /\ lateStartUsed' = [lateStartUsed EXCEPT ![s] = TRUE]
    /\ wg' = wg + 1
    /\ starts' = starts + 1
    /\ UNCHANGED <<sessState, registry, borrowed, serviceClosed, closePhase,
                   closeTargets, phase1Done, phase2Done, dones, sessionOps>>

\* Driver/terminal path can still complete the late turn before timeout.
LateTurnCompletes(s) ==
    /\ s \in Sessions
    /\ closePhase \in {"waiting", "phase2"}
    /\ turnPending[s]
    /\ turnPending' = [turnPending EXCEPT ![s] = FALSE]
    /\ wg' = wg - 1
    /\ dones' = dones + 1
    /\ UNCHANGED <<sessState, registry, borrowed, lateStartUsed, serviceClosed,
                   closePhase, closeTargets, phase1Done, phase2Done, starts, sessionOps>>

CloseWaitSuccess ==
    /\ closePhase = "waiting"
    /\ wg = 0
    /\ closePhase' = "done"
    /\ UNCHANGED <<sessState, registry, turnPending, borrowed, lateStartUsed,
                   serviceClosed, closeTargets, phase1Done, phase2Done,
                   wg, starts, dones, sessionOps>>

CloseWaitTimeout ==
    /\ closePhase = "waiting"
    /\ wg > 0
    /\ closePhase' = "phase2"
    /\ phase2Done' = {}
    /\ UNCHANGED <<sessState, registry, turnPending, borrowed, lateStartUsed,
                   serviceClosed, closeTargets, phase1Done,
                   wg, starts, dones, sessionOps>>

ClosePhase2Step(s) ==
    /\ s \in Sessions
    /\ closePhase = "phase2"
    /\ s \in closeTargets \ phase2Done
    /\ phase2Done' = phase2Done \cup {s}
    /\ IF turnPending[s]
       THEN /\ turnPending' = [turnPending EXCEPT ![s] = FALSE]
            /\ wg' = wg - 1
            /\ dones' = dones + 1
       ELSE /\ UNCHANGED <<turnPending, wg, dones>>
    /\ UNCHANGED <<sessState, registry, borrowed, lateStartUsed, serviceClosed,
                   closePhase, closeTargets, phase1Done, starts, sessionOps>>

ClosePhase2Finish ==
    /\ closePhase = "phase2"
    /\ phase2Done = closeTargets
    /\ wg = 0
    /\ closePhase' = "done"
    /\ UNCHANGED <<sessState, registry, turnPending, borrowed, lateStartUsed,
                   serviceClosed, closeTargets, phase1Done, phase2Done,
                   wg, starts, dones, sessionOps>>

\* Stuttering terminal step so TLC doesn't report deadlock at normal completion.
ClosedTerminal ==
    /\ closePhase = "done"
    /\ wg = 0
    /\ UNCHANGED vars

Next ==
    \/ \E s \in Sessions :
         \/ CreateSession(s)
         \/ AcquireHandle(s)
         \/ StartTurn(s)
         \/ CompleteTurn(s)
         \/ DestroySession(s)
         \/ ClosePhase1Step(s)
         \/ LateStartTurn(s)
         \/ LateTurnCompletes(s)
         \/ ClosePhase2Step(s)
    \/ CloseBegin
    \/ ClosePhase1Finish
    \/ CloseWaitSuccess
    \/ CloseWaitTimeout
    \/ ClosePhase2Finish
    \/ ClosedTerminal

Fairness ==
    /\ WF_vars(CloseBegin)
    /\ WF_vars(ClosePhase1Finish)
    /\ WF_vars(CloseWaitSuccess)
    /\ WF_vars(CloseWaitTimeout)
    /\ WF_vars(ClosePhase2Finish)
    /\ \A s \in Sessions :
         /\ WF_vars(ClosePhase1Step(s))
         /\ WF_vars(ClosePhase2Step(s))
         /\ WF_vars(LateTurnCompletes(s))

Spec == Init /\ [][Next]_vars /\ Fairness

\* -----------------------------------------------------------------------
\* Safety invariants (Layer 2 targets)
\* -----------------------------------------------------------------------

TypeInvariant ==
    /\ registry \subseteq Sessions
    /\ closeTargets \subseteq Sessions
    /\ phase1Done \subseteq closeTargets
    /\ phase2Done \subseteq closeTargets
    /\ \A s \in Sessions : sessState[s] \in SessionStates
    /\ \A s \in Sessions : turnPending[s] \in BOOLEAN
    /\ \A s \in Sessions : borrowed[s] \in BOOLEAN
    /\ \A s \in Sessions : lateStartUsed[s] \in BOOLEAN
    /\ serviceClosed \in BOOLEAN
    /\ closePhase \in ClosePhases
    /\ wg \in Nat
    /\ starts \in Nat
    /\ dones \in Nat
    /\ sessionOps \in Nat

CapacityLimit ==
    Cardinality(registry) <= MaxSess

SessionRegistryInvariant ==
    /\ \A s \in registry : sessState[s] \in {"idle", "active"}
    /\ \A s \in Sessions \ registry : sessState[s] = "destroyed"
    /\ \A s \in Sessions : sessState[s] = "active" => s \in registry

WaitGroupConsistency ==
    /\ starts >= dones
    /\ wg = starts - dones
    /\ \A s \in Sessions : turnPending[s] => starts > dones

OperationBounds ==
    /\ starts <= MaxTurnStarts
    /\ sessionOps <= MaxSessionOps

CloseTwoPhaseInvariant ==
    /\ (closePhase = "none") => (~serviceClosed /\ closeTargets = {} /\ phase1Done = {} /\ phase2Done = {})
    /\ (closePhase \in {"phase1", "waiting", "phase2", "done"}) => serviceClosed
    /\ (closePhase = "waiting") => phase1Done = closeTargets
    /\ (closePhase = "done") => (wg = 0 /\ \A s \in closeTargets : ~turnPending[s] /\ registry = {})

SafetyInvariant ==
    /\ CapacityLimit
    /\ SessionRegistryInvariant
    /\ WaitGroupConsistency
    /\ OperationBounds
    /\ CloseTwoPhaseInvariant

\* -----------------------------------------------------------------------
\* Liveness
\* -----------------------------------------------------------------------

\* Close eventually reaches done once started.
CloseTermination ==
    (closePhase = "phase1") ~> (closePhase = "done")

\* If a late-arriving turn appears while waiting, phase 2 eventually drains it.
LateStartDrain ==
    ((closePhase = "waiting") /\ (wg > 0)) ~> ((closePhase = "done") /\ (wg = 0))

\* Close returns only with drained WaitGroup.
CloseReturnsWithZeroWG ==
    (closePhase = "done") ~> (wg = 0)

=========================================================================
