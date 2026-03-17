package agenttest

import (
	"testing"

	"github.com/anthropics/flex-agent-runtime/internal/agent"
)

// NewTestStack is a compatibility alias used by plan18 task wording.
func NewTestStack(t testing.TB, opts ...agent.ServiceOption) *AgentTestStack {
	return NewAgentTestStack(t, opts...)
}
