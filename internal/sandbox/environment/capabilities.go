package environment

import "time"

// Capabilities describes what an ExecutionEnvironment supports.
type Capabilities struct {
	Snapshots          bool
	Rollback           bool
	Pause              bool
	StreamingProgress  bool
	TierRouting        bool
	MaxSessionDuration time.Duration
	ConcurrentSessions int
}

var LocalCapabilities = Capabilities{
	Snapshots:         false,
	Rollback:          false,
	Pause:             true,
	TierRouting:       false,
	StreamingProgress: true,
}

var E2BCapabilities = Capabilities{
	Snapshots:          false,
	Rollback:           false,
	Pause:              true,
	TierRouting:        false,
	StreamingProgress:  true,
	MaxSessionDuration: 24 * time.Hour,
}

var DaytonaCapabilities = Capabilities{
	Snapshots:         false,
	Rollback:          false,
	Pause:             false,
	TierRouting:       false,
	StreamingProgress: true,
}

var FlyCapabilities = Capabilities{
	Snapshots:         false,
	Rollback:          false,
	Pause:             true,
	TierRouting:       false,
	StreamingProgress: false,
}
