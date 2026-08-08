// Package keymap maps MIDI note numbers to the physical keyboard keys a
// particular game reads its instrument input from. It knows nothing about
// MIDI parsing or how keys are actually injected — it only decides which
// virtual-key code(s) must be down to sound a given note.
package keymap

// Shift identifies which octave-shift modifier (if any) must be held down
// to reach a note from the profile's base (unshifted) key layout.
type Shift int

const (
	ShiftNone Shift = iota // no modifier held
	ShiftUp                // modifier held that transposes the base layout up
	ShiftDown              // modifier held that transposes the base layout down
)

// Key identifies a physical key by its Windows virtual-key code.
type Key struct {
	VK       uint16
	Extended bool // set for keys that require KEYEVENTF_EXTENDEDKEY (arrows, numpad, right Ctrl/Alt, ...)
}

// Resolution is the result of resolving a MIDI note against a Profile: which
// key to press, and which octave-shift state must be active while pressing it.
type Resolution struct {
	Key   Key
	Shift Shift
}

// Profile is a game's instrument key layout. Implementations are expected to
// be stateless and safe for concurrent use; the caller (the engine) owns all
// mutable state such as which shift is currently active.
type Profile interface {
	// Name identifies the profile, e.g. for UI display and config files.
	Name() string

	// Resolve returns the key and octave-shift state needed to sound note.
	// If the note is reachable in more than one shift state, Resolve prefers
	// preferredShift to avoid unnecessary modifier toggling. ok is false if
	// the note cannot be reached in any shift state (out of the instrument's
	// total range); the caller should drop the note.
	Resolve(note uint8, preferredShift Shift) (res Resolution, ok bool)

	// ShiftKey returns the physical modifier key that must be held to put
	// the instrument into shift state s. ok is false for ShiftNone, which
	// has no key of its own.
	ShiftKey(s Shift) (key Key, ok bool)

	// Sustain returns the key that corresponds to the sustain/damper pedal
	// (MIDI CC64).
	Sustain() Key
}
