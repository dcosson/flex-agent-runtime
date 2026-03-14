package transport

const (
	ProcedureSandboxCreateSession  = "/rpc.v1.SandboxService/CreateSession"
	ProcedureSandboxGetSession     = "/rpc.v1.SandboxService/GetSession"
	ProcedureSandboxPauseSession   = "/rpc.v1.SandboxService/PauseSession"
	ProcedureSandboxResumeSession  = "/rpc.v1.SandboxService/ResumeSession"
	ProcedureSandboxDestroySession = "/rpc.v1.SandboxService/DestroySession"
	ProcedureSandboxExecuteTool    = "/rpc.v1.SandboxService/ExecuteTool"
	ProcedureSandboxExecuteStream  = "/rpc.v1.SandboxService/ExecuteToolStream"
	ProcedureSandboxTurnComplete   = "/rpc.v1.SandboxService/TurnComplete"
	ProcedureSandboxCreateSnapshot = "/rpc.v1.SandboxService/CreateSnapshot"
	ProcedureSandboxRollback       = "/rpc.v1.SandboxService/RollbackSession"
	ProcedureSandboxListSnapshots  = "/rpc.v1.SandboxService/ListSnapshots"
	ProcedureSandboxHealthCheck    = "/rpc.v1.SandboxService/HealthCheck"

	ProcedureEventsStream = "/rpc.v1.AgentEventService/StreamAgentEvents"

	ProcedureTerminalStream = "/rpc.v1.TerminalService/StreamTerminal"
)
