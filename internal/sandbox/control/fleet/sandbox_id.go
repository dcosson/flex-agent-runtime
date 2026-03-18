package fleet

import (
	"fmt"
	"strings"
)

// sandboxIDPrefix distinguishes fleet-managed sandbox IDs from other adapters.
// Cross-adapter prefix convention:
//   - "direct:{instanceID}" -- Direct adapter (Plan 19)
//   - "fleet:{instanceID}:{sessionID}" -- Fleet adapter (this package)
//   - Raw session IDs (no prefix) -- Node/Native adapter
const sandboxIDPrefix = "fleet:"

// encodeSandboxID constructs a fleet SandboxID from an instance ID and session
// ID. The format is "fleet:{instanceID}:{sessionID}".
//
// Instance IDs MUST NOT contain colons (this is a hard constraint validated at
// runtime). Current cloud providers satisfy this: EC2 uses "i-{hex}", GCP uses
// numeric IDs, Azure uses alphanumeric resource names. Session IDs may contain
// colons since everything after the first separator is treated as the sessionID.
func encodeSandboxID(instanceID, sessionID string) (string, error) {
	if instanceID == "" || sessionID == "" {
		return "", fmt.Errorf("invalid sandbox ID components: instanceID=%q sessionID=%q", instanceID, sessionID)
	}
	if strings.Contains(instanceID, ":") {
		return "", fmt.Errorf("invalid instance ID: contains colon: %q", instanceID)
	}
	return sandboxIDPrefix + instanceID + ":" + sessionID, nil
}

// SandboxIDPrefix is the prefix for fleet-managed sandbox IDs.
// Exported so the orchestrator can detect fleet-prefixed IDs.
const SandboxIDPrefix = sandboxIDPrefix

// ParseSandboxID extracts the instance ID and session ID from a fleet
// SandboxID. Returns an error if the ID is malformed (wrong prefix, missing
// separator, or empty components).
//
// This is the exported entry point; internal callers use parseSandboxID.
func ParseSandboxID(sandboxID string) (instanceID, sessionID string, err error) {
	return parseSandboxID(sandboxID)
}

// parseSandboxID extracts the instance ID and session ID from a fleet
// SandboxID. Returns an error if the ID is malformed (wrong prefix, missing
// separator, or empty components).
func parseSandboxID(sandboxID string) (instanceID, sessionID string, err error) {
	if !strings.HasPrefix(sandboxID, sandboxIDPrefix) {
		return "", "", fmt.Errorf("invalid fleet sandbox ID: missing prefix: %q", sandboxID)
	}
	rest := sandboxID[len(sandboxIDPrefix):]
	idx := strings.Index(rest, ":")
	if idx < 0 {
		return "", "", fmt.Errorf("invalid fleet sandbox ID: missing session separator: %q", sandboxID)
	}
	instanceID, sessionID = rest[:idx], rest[idx+1:]
	if instanceID == "" || sessionID == "" {
		return "", "", fmt.Errorf("invalid fleet sandbox ID: empty component: %q", sandboxID)
	}
	return instanceID, sessionID, nil
}
