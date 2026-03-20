package environment

import "testing"

func TestIsFileOp(t *testing.T) {
	fileOps := []string{"read_file", "write_file", "edit_file", "grep", "glob"}
	for _, name := range fileOps {
		if !IsFileOp(name) {
			t.Errorf("IsFileOp(%q) = false, want true", name)
		}
	}

	commandOps := []string{"bash", "git_status", "git_diff", "git_log",
		"git_show", "git_add", "git_commit", "git_push",
		"code_interpreter", "unknown_tool", "",
		"read", "write", "edit"}
	for _, name := range commandOps {
		if IsFileOp(name) {
			t.Errorf("IsFileOp(%q) = true, want false", name)
		}
	}
}
