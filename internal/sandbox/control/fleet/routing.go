package fleet

import "sync/atomic"

// CapacityRouter selects the best instance for a new sandbox using a spread
// strategy that prefers the most available capacity. This maximizes per-instance
// headroom so burst traffic is absorbed without immediate scale-up.
type CapacityRouter struct {
	config     FleetConfig
	roundRobin atomic.Uint64
}

// NewCapacityRouter creates a router configured from the given FleetConfig.
func NewCapacityRouter(config FleetConfig) *CapacityRouter {
	return &CapacityRouter{config: config}
}

// SelectInstance picks the best instance for a new sandbox.
// Returns ErrNoCapacity if no instance can accept the sandbox.
//
// Strategy: spread (prefer most available capacity). When multiple instances
// have equal headroom, use IdleSince as a tiebreaker (prefer the instance that
// has been idle longest, promoting even distribution). If IdleSince is also
// equal (e.g., all instances at the same session count), use an atomic counter
// for round-robin to prevent map iteration order bias.
//
// Caller must ensure instances are not concurrently mutated (hold fleet read
// lock or pass a snapshot).
func (r *CapacityRouter) SelectInstance(instances []*ManagedInstance) (*ManagedInstance, error) {
	type candidate struct {
		inst  *ManagedInstance
		score float64
	}
	var candidates []candidate

	for _, inst := range instances {
		if inst.State != InstanceReady && inst.State != InstanceActive {
			continue
		}
		if inst.SessionCount >= int64(inst.MaxSessions) {
			continue
		}
		headroom := 1.0 - (float64(inst.SessionCount) / float64(inst.MaxSessions))
		if headroom < (1.0 - r.config.CapacityHeadroom) {
			continue
		}
		candidates = append(candidates, candidate{inst: inst, score: headroom})
	}

	if len(candidates) == 0 {
		return nil, ErrNoCapacity
	}

	// Find the best score.
	bestScore := candidates[0].score
	for _, c := range candidates[1:] {
		if c.score > bestScore {
			bestScore = c.score
		}
	}

	// Collect all candidates within epsilon of the best score.
	const epsilon = 0.001
	var tied []candidate
	for _, c := range candidates {
		if bestScore-c.score < epsilon {
			tied = append(tied, c)
		}
	}

	if len(tied) == 1 {
		return tied[0].inst, nil
	}

	// Tiebreaker: prefer the instance idle longest (oldest IdleSince).
	// For active instances (IdleSince is zero), fall through to round-robin.
	best := tied[0]
	allZero := best.inst.IdleSince.IsZero()
	for _, c := range tied[1:] {
		if !c.inst.IdleSince.IsZero() {
			allZero = false
			if best.inst.IdleSince.IsZero() || c.inst.IdleSince.Before(best.inst.IdleSince) {
				best = c
			}
		}
	}

	if allZero {
		// All tied candidates are active. Use atomic counter for round-robin.
		idx := r.roundRobin.Add(1) % uint64(len(tied))
		return tied[idx].inst, nil
	}

	return best.inst, nil
}
