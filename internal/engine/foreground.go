package engine

import (
	"strings"
	"unsafe"

	"golang.org/x/sys/windows"
)

var (
	user32                  = windows.NewLazySystemDLL("user32.dll")
	procGetForegroundWindow = user32.NewProc("GetForegroundWindow")
	procGetWindowTextW      = user32.NewProc("GetWindowTextW")
)

// ForegroundChecker reports whether the currently focused window is the one
// the player is expected to send keystrokes into. Playback refuses to start
// unless it matches, since SendInput always targets whatever has focus.
type ForegroundChecker interface {
	Matches() (bool, error)
}

// WindowTitleChecker matches the foreground window by a case-insensitive
// substring of its title.
type WindowTitleChecker struct {
	TitleSubstr string
}

// NewWindowTitleChecker returns a ForegroundChecker that matches when the
// foreground window's title contains titleSubstr, case-insensitively.
func NewWindowTitleChecker(titleSubstr string) *WindowTitleChecker {
	return &WindowTitleChecker{TitleSubstr: titleSubstr}
}

func (w *WindowTitleChecker) Matches() (bool, error) {
	title, err := foregroundWindowTitle()
	if err != nil {
		return false, err
	}
	return strings.Contains(strings.ToLower(title), strings.ToLower(w.TitleSubstr)), nil
}

func foregroundWindowTitle() (string, error) {
	hwnd, _, _ := procGetForegroundWindow.Call()
	if hwnd == 0 {
		// No window is focused at all (e.g. the desktop). Not an error —
		// it simply never matches.
		return "", nil
	}

	buf := make([]uint16, 512)
	ret, _, _ := procGetWindowTextW.Call(
		hwnd,
		uintptr(unsafe.Pointer(&buf[0])),
		uintptr(len(buf)),
	)
	// ret==0 covers both "call failed" and "window legitimately has an
	// empty title" (e.g. some tool windows) — either way there's nothing to
	// match against, so treat it as "no title" rather than an error.
	if ret == 0 {
		return "", nil
	}

	return windows.UTF16ToString(buf[:ret]), nil
}
