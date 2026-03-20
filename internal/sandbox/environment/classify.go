package environment

// IsFileOp returns true for tool names that operate on the filesystem
// (read, write, edit, grep, glob) and should be dispatched via the
// environment's filesystem API. All other tools are treated as process
// operations and dispatched via the command execution API.
// This is independent of the Tier 1/2 classifier in internal/tools,
// which is a NativeSandbox-specific concept.
func IsFileOp(toolName string) bool {
	switch toolName {
	case "read", "write", "edit", "grep", "glob":
		return true
	default:
		return false
	}
}
