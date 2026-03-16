package harness

import "os/exec"

// execCommand wraps exec.Command for testability.
var execCommand = exec.Command
