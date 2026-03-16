package harness

import (
	"fmt"
	"strconv"
	"strings"
	"testing"

	"h2-agent-runtime/internal/agent"
	"h2-agent-runtime/internal/rpc/api"
)

type SnapshotAssertion struct{}

func (SnapshotAssertion) AssertMonotonicIDs(t *testing.T, snaps []api.Snapshot) {
	t.Helper()
	if len(snaps) == 0 {
		t.Fatal("expected snapshots, got none")
	}
	for i := 1; i < len(snaps); i++ {
		prev := numericSuffix(snaps[i-1].Name)
		curr := numericSuffix(snaps[i].Name)
		if curr < prev {
			t.Fatalf("snapshot ids not monotonic: %q before %q", snaps[i-1].Name, snaps[i].Name)
		}
	}
}

func (SnapshotAssertion) AssertFileContent(t *testing.T, svc *MemorySandboxService, sessionID, filePath string, wantContains string) {
	t.Helper()
	got, ok := svc.ReadSessionFile(sessionID, filePath)
	if !ok {
		t.Fatalf("expected file %q to exist", filePath)
	}
	if !strings.Contains(got, wantContains) {
		t.Fatalf("file %q content mismatch: wanted substring %q, got %q", filePath, wantContains, got)
	}
}

func AssertEventSequence(t *testing.T, events []agent.AgentEvent, want ...agent.AgentEventType) {
	t.Helper()
	idx := 0
	for _, evt := range events {
		if idx < len(want) && evt.Type == want[idx] {
			idx++
		}
	}
	if idx != len(want) {
		t.Fatalf("missing event sequence element at index %d (want=%v)", idx, want)
	}
}

func numericSuffix(name string) int {
	parts := strings.Split(name, "-")
	if len(parts) == 0 {
		return 0
	}
	n, err := strconv.Atoi(parts[len(parts)-1])
	if err != nil {
		return 0
	}
	return n
}

func DebugEvents(events []agent.AgentEvent) string {
	lines := make([]string, 0, len(events))
	for i, evt := range events {
		lines = append(lines, fmt.Sprintf("[%d] %s turn=%d tool=%s", i, evt.Type, evt.Turn, evt.ToolName))
	}
	return strings.Join(lines, "\n")
}
