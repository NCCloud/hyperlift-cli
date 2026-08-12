package iostreams

const (
	escReset  = "\033[0m"
	escBold   = "\033[1m"
	escRed    = "\033[31m"
	escGreen  = "\033[32m"
	escYellow = "\033[33m"
	escCyan   = "\033[36m"
	escGray   = "\033[90m"
)

// ColorScheme builds ANSI-colored strings. It honors the color gate of its
// IOStreams, which reads NO_COLOR and the TTY state. When color is off, every
// method returns its input unchanged.
type ColorScheme struct {
	enabled bool
}

// ColorScheme returns a scheme that follows the color gate of this stream.
func (s *IOStreams) ColorScheme() *ColorScheme {
	return &ColorScheme{enabled: s.ColorEnabled()}
}

func (c *ColorScheme) wrap(code, str string) string {
	if !c.enabled {
		return str
	}

	return code + str + escReset
}

// Bold renders bold text.
func (c *ColorScheme) Bold(s string) string { return c.wrap(escBold, s) }

// Red renders red text (errors).
func (c *ColorScheme) Red(s string) string { return c.wrap(escRed, s) }

// Green renders green text (success).
func (c *ColorScheme) Green(s string) string { return c.wrap(escGreen, s) }

// Yellow renders yellow text (warnings).
func (c *ColorScheme) Yellow(s string) string { return c.wrap(escYellow, s) }

// Cyan renders cyan text (accents/links).
func (c *ColorScheme) Cyan(s string) string { return c.wrap(escCyan, s) }

// Gray renders gray text (secondary detail).
func (c *ColorScheme) Gray(s string) string { return c.wrap(escGray, s) }

// Enabled reports whether color output is on.
func (c *ColorScheme) Enabled() bool { return c.enabled }
