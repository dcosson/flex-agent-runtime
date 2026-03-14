package sandbox

type Tier int

const (
	Tier1 Tier = 1
	Tier2 Tier = 2
)

func ClassifyTool(toolName string) Tier {
	switch toolName {
	case "read_file", "write_file", "edit_file", "grep", "glob", "git_status", "git_diff", "git_log", "git_show":
		return Tier1
	case "bash", "git_push", "git_clone", "git_fetch", "git_pull", "git_add", "git_commit":
		return Tier2
	default:
		return Tier2
	}
}
