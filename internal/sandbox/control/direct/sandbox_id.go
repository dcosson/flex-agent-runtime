package direct

import (
	"fmt"
	"strings"
)

const sandboxIDPrefix = "direct:"

// encodeSandboxID creates a sandbox ID from an EC2 instance ID by
// prepending the "direct:" prefix. This prefix allows an orchestrator
// to determine which adapter owns a given sandbox ID.
func encodeSandboxID(instanceID string) string {
	return sandboxIDPrefix + instanceID
}

// parseSandboxID extracts the EC2 instance ID from a sandbox ID by
// stripping the "direct:" prefix. Returns an error if the prefix is
// missing or the instance ID portion is empty.
func parseSandboxID(sandboxID string) (string, error) {
	if !strings.HasPrefix(sandboxID, sandboxIDPrefix) {
		return "", fmt.Errorf("direct: invalid sandbox ID prefix: expected %q prefix, got %q", sandboxIDPrefix, sandboxID)
	}
	instanceID := sandboxID[len(sandboxIDPrefix):]
	if instanceID == "" {
		return "", fmt.Errorf("direct: empty instance ID in sandbox ID %q", sandboxID)
	}
	return instanceID, nil
}
