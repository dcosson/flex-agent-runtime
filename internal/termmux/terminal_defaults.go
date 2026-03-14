package termmux

// Terminal environment defaults for headless agent CLI sessions.
// These ensure full color and escape sequence support when no real
// terminal is attached.

const (
	DefaultTERM      = "xterm-256color"
	DefaultCOLORTERM = "truecolor"
	DefaultCOLORFGBG = "15;0" // white on black (dark theme)
)

// TerminalEnvDefaults returns the environment variables to set for headless
// child processes. These are merged with (but overridden by) any driver-specific
// or user-specified env vars.
func TerminalEnvDefaults() map[string]string {
	return map[string]string{
		"TERM":      DefaultTERM,
		"COLORTERM": DefaultCOLORTERM,
		"COLORFGBG": DefaultCOLORFGBG,
	}
}
