// Package hotkey registers the system-wide panic-stop hotkey. It matters
// because the app is never focused while playing — the game is — so the
// stop key must fire regardless of what window has focus. See
// PROJECT_CONTEXT.md section 9.
package hotkey

import (
	"fmt"
	"sync"

	lib "golang.design/x/hotkey"
)

// Key identifies a physical key eligible to be registered as the global
// stop hotkey. Deliberately a small curated set: Esc, F8, F9 and F10 are
// the keys BPSR itself doesn't use, so RegisterHotKey swallowing them
// system-wide doesn't break anything in-game. (RegisterHotKey consumes the
// key entirely — e.g. registering Esc means the game's own Esc/menu stops
// working while the listener is active. Esc is deliberately included
// because it's confirmed safe for BPSR specifically; be careful extending
// this set for other game profiles.)
type Key uint32

const (
	KeyEscape Key = Key(lib.KeyEscape)
	KeyF8     Key = Key(lib.KeyF8)
	KeyF9     Key = Key(lib.KeyF9)
	KeyF10    Key = Key(lib.KeyF10)
)

// DefaultKey is the stop hotkey used unless the user configures a different
// one. F9 rather than Esc: Esc is more prone to registration conflicts (and,
// per PROJECT_CONTEXT.md section 9, RegisterHotKey consumes whatever key
// it's given system-wide, so registering Esc would also stop BPSR itself
// from ever seeing an Esc keypress for as long as the listener is active).
// F8/F9/F10 aren't used by BPSR, so swallowing one of them system-wide is
// safe.
const DefaultKey = KeyF9

// Listener is a registered global hotkey that invokes a callback on every
// key-down, regardless of which window has focus. The zero value is not
// usable; construct with Start.
type Listener struct {
	hk   *lib.Hotkey
	done chan struct{}

	closeOnce sync.Once
	closeErr  error
}

// Start registers key as a system-wide hotkey with no modifiers and begins
// calling onTrigger (on its own goroutine) every time it's pressed. Callers
// marshal onTrigger to any other goroutine/thread themselves (e.g. a GUI's
// main-thread dispatcher) — it runs on the listener's goroutine.
func Start(key Key, onTrigger func()) (*Listener, error) {
	hk := lib.New(nil, lib.Key(key))
	if err := hk.Register(); err != nil {
		return nil, fmt.Errorf("hotkey: registering stop hotkey: %w", err)
	}

	l := &Listener{hk: hk, done: make(chan struct{})}
	// Capture the channel once, up front: hk.Keydown() reads a struct field
	// that Unregister() reassigns concurrently, so re-calling it on every
	// loop iteration below races with a Close() running on another
	// goroutine (found by go test -race). The channel we capture here is
	// still the one Unregister() closes (it closes the same underlying
	// channel before swapping in a new one for future use), so reading only
	// from this fixed reference is both race-free and still correctly
	// unblocks the loop when Close() runs.
	keydown := hk.Keydown()
	go l.loop(keydown, onTrigger)
	return l, nil
}

func (l *Listener) loop(keydown <-chan lib.Event, onTrigger func()) {
	for {
		select {
		case _, ok := <-keydown:
			if !ok {
				return
			}
			onTrigger()
		case <-l.done:
			return
		}
	}
}

// Close unregisters the hotkey and stops the listener goroutine. Safe to
// call more than once — later calls return the same result as the first,
// rather than panicking on a double-close of the done channel.
func (l *Listener) Close() error {
	l.closeOnce.Do(func() {
		close(l.done)
		l.closeErr = l.hk.Unregister()
	})
	return l.closeErr
}
