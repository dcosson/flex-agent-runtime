package direct

import (
	"strings"
	"testing"
)

func TestShellQuote(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{
			name:  "simple string",
			input: "hello",
			want:  "'hello'",
		},
		{
			name:  "empty string",
			input: "",
			want:  "''",
		},
		{
			name:  "string with spaces",
			input: "hello world",
			want:  "'hello world'",
		},
		{
			name:  "string with single quote",
			input: "it's",
			want:  "'it'\\''s'",
		},
		{
			name:  "string with multiple single quotes",
			input: "it's a 'test'",
			want:  "'it'\\''s a '\\''test'\\'''",
		},
		{
			name:  "string with double quotes",
			input: `say "hello"`,
			want:  `'say "hello"'`,
		},
		{
			name:  "string with dollar sign",
			input: "$HOME",
			want:  "'$HOME'",
		},
		{
			name:  "string with backticks",
			input: "`whoami`",
			want:  "'`whoami`'",
		},
		{
			name:  "string with newline",
			input: "line1\nline2",
			want:  "'line1\nline2'",
		},
		{
			name:  "string with backslash",
			input: `path\to\file`,
			want:  `'path\to\file'`,
		},
		{
			name:  "string with semicolons and pipes",
			input: "cmd; rm -rf / | cat",
			want:  "'cmd; rm -rf / | cat'",
		},
		{
			name:  "string with tab",
			input: "col1\tcol2",
			want:  "'col1\tcol2'",
		},
		{
			name:  "string with parentheses",
			input: "$(command)",
			want:  "'$(command)'",
		},
		{
			name:  "only single quote",
			input: "'",
			want:  "''\\'''",
		},
		{
			name:  "string with exclamation mark",
			input: "hello!",
			want:  "'hello!'",
		},
		{
			name:  "string with all special chars",
			input: `$'"\` + "`" + `()!;|&<>`,
			want:  `'$'\''"` + "\\`" + `()!;|&<>'`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := shellQuote(tt.input)
			if got != tt.want {
				t.Errorf("shellQuote(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

func TestBuildSSMCommand(t *testing.T) {
	t.Run("simple command no env", func(t *testing.T) {
		cmd := buildSSMCommand("proc-123", "/usr/local/bin/flexagent", []string{"serve", "agent", "--listen=:8081"}, nil)

		// Should contain the nohup command with quoted binary and args
		if !strings.Contains(cmd, "nohup '/usr/local/bin/flexagent' 'serve' 'agent' '--listen=:8081'") {
			t.Errorf("command missing expected nohup line, got:\n%s", cmd)
		}
		// Should redirect to log file
		if !strings.Contains(cmd, "/var/log/flex-agent-proc-123.log") {
			t.Errorf("command missing log file reference, got:\n%s", cmd)
		}
		// Should write PID file
		if !strings.Contains(cmd, "/var/run/flex-agent-proc-123.pid") {
			t.Errorf("command missing PID file reference, got:\n%s", cmd)
		}
		// Should write status marker
		if !strings.Contains(cmd, "/var/run/flex-agent-proc-123.status") {
			t.Errorf("command missing status file reference, got:\n%s", cmd)
		}
		// Should NOT have any export lines
		if strings.Contains(cmd, "export") {
			t.Errorf("command should not have export lines with nil env, got:\n%s", cmd)
		}
	})

	t.Run("command with env vars", func(t *testing.T) {
		env := map[string]string{
			"ANTHROPIC_API_KEY": "sk-ant-123",
			"HOME":              "/home/ubuntu",
		}
		cmd := buildSSMCommand("proc-456", "/usr/local/bin/flexagent", []string{"serve"}, env)

		// Env vars should be sorted and quoted
		if !strings.Contains(cmd, "export ANTHROPIC_API_KEY='sk-ant-123'") {
			t.Errorf("command missing ANTHROPIC_API_KEY export, got:\n%s", cmd)
		}
		if !strings.Contains(cmd, "export HOME='/home/ubuntu'") {
			t.Errorf("command missing HOME export, got:\n%s", cmd)
		}
		// ANTHROPIC should come before HOME (sorted)
		antIdx := strings.Index(cmd, "ANTHROPIC_API_KEY")
		homeIdx := strings.Index(cmd, "HOME=")
		if antIdx > homeIdx {
			t.Errorf("env vars should be sorted: ANTHROPIC_API_KEY before HOME, got:\n%s", cmd)
		}
	})

	t.Run("env value with single quotes", func(t *testing.T) {
		env := map[string]string{
			"MSG": "it's a test",
		}
		cmd := buildSSMCommand("proc-789", "/bin/echo", nil, env)

		if !strings.Contains(cmd, "export MSG='it'\\''s a test'") {
			t.Errorf("env value with single quote not escaped properly, got:\n%s", cmd)
		}
	})

	t.Run("env value with dollar sign", func(t *testing.T) {
		env := map[string]string{
			"VAL": "$HOME/path",
		}
		cmd := buildSSMCommand("proc-001", "/bin/echo", nil, env)

		if !strings.Contains(cmd, "export VAL='$HOME/path'") {
			t.Errorf("env value with dollar sign not quoted properly, got:\n%s", cmd)
		}
	})

	t.Run("env value with backticks", func(t *testing.T) {
		env := map[string]string{
			"VAL": "`whoami`",
		}
		cmd := buildSSMCommand("proc-002", "/bin/echo", nil, env)

		if !strings.Contains(cmd, "export VAL='`whoami`'") {
			t.Errorf("env value with backticks not quoted properly, got:\n%s", cmd)
		}
	})

	t.Run("env value with newlines", func(t *testing.T) {
		env := map[string]string{
			"MULTI": "line1\nline2",
		}
		cmd := buildSSMCommand("proc-003", "/bin/echo", nil, env)

		if !strings.Contains(cmd, "export MULTI='line1\nline2'") {
			t.Errorf("env value with newlines not quoted properly, got:\n%s", cmd)
		}
	})

	t.Run("binary with spaces", func(t *testing.T) {
		cmd := buildSSMCommand("proc-004", "/path/to my/binary", []string{"arg 1"}, nil)

		if !strings.Contains(cmd, "nohup '/path/to my/binary' 'arg 1'") {
			t.Errorf("binary and args with spaces not quoted properly, got:\n%s", cmd)
		}
	})

	t.Run("args with double quotes", func(t *testing.T) {
		cmd := buildSSMCommand("proc-005", "/bin/echo", []string{`say "hello"`}, nil)

		if !strings.Contains(cmd, `'say "hello"'`) {
			t.Errorf("arg with double quotes not quoted properly, got:\n%s", cmd)
		}
	})

	t.Run("empty args", func(t *testing.T) {
		cmd := buildSSMCommand("proc-006", "/bin/true", nil, nil)

		if !strings.Contains(cmd, "nohup '/bin/true'") {
			t.Errorf("command with no args not formed properly, got:\n%s", cmd)
		}
	})

	t.Run("empty env map", func(t *testing.T) {
		cmd := buildSSMCommand("proc-007", "/bin/true", nil, map[string]string{})

		if strings.Contains(cmd, "export") {
			t.Errorf("command should not have export lines with empty env, got:\n%s", cmd)
		}
	})

	t.Run("command captures PID and start time", func(t *testing.T) {
		cmd := buildSSMCommand("proc-008", "/bin/echo", nil, nil)

		if !strings.Contains(cmd, "PID=$!") {
			t.Errorf("command missing PID capture, got:\n%s", cmd)
		}
		if !strings.Contains(cmd, "START_TIME=$(cat /proc/$PID/stat") {
			t.Errorf("command missing start time capture, got:\n%s", cmd)
		}
		if !strings.Contains(cmd, "echo \"$PID\"") {
			t.Errorf("command missing PID echo, got:\n%s", cmd)
		}
		if !strings.Contains(cmd, "echo \"$START_TIME\"") {
			t.Errorf("command missing start time echo, got:\n%s", cmd)
		}
	})

	t.Run("status marker written", func(t *testing.T) {
		cmd := buildSSMCommand("proc-009", "/bin/echo", nil, nil)

		if !strings.Contains(cmd, `echo "launched"`) {
			t.Errorf("command missing status marker, got:\n%s", cmd)
		}
	})
}

func TestSortedKeys(t *testing.T) {
	m := map[string]string{
		"C": "3",
		"A": "1",
		"B": "2",
	}
	keys := sortedKeys(m)
	if len(keys) != 3 || keys[0] != "A" || keys[1] != "B" || keys[2] != "C" {
		t.Errorf("sortedKeys returned %v, want [A B C]", keys)
	}
}
