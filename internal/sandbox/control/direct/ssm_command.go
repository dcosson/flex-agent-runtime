package direct

import (
	"fmt"
	"strings"
)

// shellQuote wraps a value in single quotes with proper escaping for use
// in shell commands. Single quotes in the value are escaped using the
// standard '\” technique (end quote, escaped literal quote, start quote).
//
// This produces output that is safe to interpolate into a bash command
// even when the value contains single quotes, double quotes, newlines,
// dollar signs, backticks, or other special characters.
func shellQuote(s string) string {
	// Replace each single quote with: end single quote, escaped single quote, start single quote
	escaped := strings.ReplaceAll(s, "'", "'\\''")
	return "'" + escaped + "'"
}

// buildSSMCommand constructs a shell command string for launching a
// daemonized process via SSM. All user-supplied values (binary, args,
// env values) are shell-quoted to prevent injection.
//
// The generated command:
//  1. Exports environment variables (each value shell-quoted)
//  2. Launches the binary with args in the background via nohup
//  3. Writes PID and /proc start time to a well-known PID file
//  4. Writes a status marker file
func buildSSMCommand(processID string, binary string, args []string, env map[string]string) string {
	var b strings.Builder

	// Export environment variables
	// Sort keys for deterministic output (important for testing).
	if len(env) > 0 {
		keys := sortedKeys(env)
		for _, k := range keys {
			fmt.Fprintf(&b, "export %s=%s\n", k, shellQuote(env[k]))
		}
	}

	// Build the command line with all args shell-quoted
	fmt.Fprintf(&b, "nohup %s", shellQuote(binary))
	for _, arg := range args {
		fmt.Fprintf(&b, " %s", shellQuote(arg))
	}

	// Redirect output to log file, run in background
	logFile := fmt.Sprintf("/var/log/flex-agent-%s.log", processID)
	fmt.Fprintf(&b, " > %s 2>&1 &\n", shellQuote(logFile))

	// Capture PID
	b.WriteString("PID=$!\n")

	// Write PID and start time to well-known file
	pidFile := fmt.Sprintf("/var/run/flex-agent-%s.pid", processID)
	fmt.Fprintf(&b, "echo \"$PID\" > %s\n", shellQuote(pidFile))
	fmt.Fprintf(&b, "START_TIME=$(cat /proc/$PID/stat 2>/dev/null | awk '{print $22}')\n")
	fmt.Fprintf(&b, "echo \"$START_TIME\" >> %s\n", shellQuote(pidFile))

	// Write status marker
	statusFile := fmt.Sprintf("/var/run/flex-agent-%s.status", processID)
	fmt.Fprintf(&b, "echo \"launched\" > %s\n", shellQuote(statusFile))

	return b.String()
}

// sortedKeys returns the keys of a map sorted lexicographically.
func sortedKeys(m map[string]string) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	// Simple insertion sort -- env maps are small
	for i := 1; i < len(keys); i++ {
		for j := i; j > 0 && keys[j] < keys[j-1]; j-- {
			keys[j], keys[j-1] = keys[j-1], keys[j]
		}
	}
	return keys
}
