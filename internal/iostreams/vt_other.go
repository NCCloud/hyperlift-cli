//go:build !windows

package iostreams

// enableVirtualTerminal is a no-op outside Windows: Unix terminals process
// ANSI escapes natively.
func enableVirtualTerminal(uintptr) bool { return true }
