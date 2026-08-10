//go:build windows

package iostreams

import "golang.org/x/sys/windows"

// enableVirtualTerminal turns on ANSI escape processing for the console
// behind fd. It reports whether escape sequences will render; when it reports
// false the caller must not write color.
func enableVirtualTerminal(fd uintptr) bool {
	h := windows.Handle(fd)
	var mode uint32
	if err := windows.GetConsoleMode(h, &mode); err != nil {
		return false
	}
	if mode&windows.ENABLE_VIRTUAL_TERMINAL_PROCESSING != 0 {
		return true
	}
	return windows.SetConsoleMode(h, mode|windows.ENABLE_VIRTUAL_TERMINAL_PROCESSING) == nil
}
