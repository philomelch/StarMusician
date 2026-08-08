package keymap

// Virtual-key codes used by the BPSR profile. Letters and digits use their
// standard Windows VK values (VK_0..VK_9 == ASCII '0'..'9', VK_A..VK_Z ==
// ASCII 'A'..'Z'); the rest are named VK_OEM_* constants from winuser.h.
const (
	vkSpace    = 0x20
	vkLShift   = 0xA0
	vkLControl = 0xA2
	vkOEM4     = 0xDB // '['
	vkOEM6     = 0xDD // ']'
)

func vkDigit(d byte) uint16  { return uint16('0' + d) }
func vkLetter(c byte) uint16 { return uint16(c) } // c must already be uppercase ASCII

// bpsrBaseLow and bpsrBaseHigh are the MIDI note numbers of the lowest and
// highest note reachable with no octave-shift modifier held (C3..B5 using
// the convention where MIDI 60 == C4).
const (
	bpsrBaseLow  = 48
	bpsrBaseHigh = 83
)

// bpsrKeys maps every note in [bpsrBaseLow, bpsrBaseHigh] to its unshifted
// key. Layout confirmed against saptia14/bpsr_midi_player's config.py:
// naturals sit on Z X C V B N M / A S D F G H J / Q W E R T Y U for octaves
// 3/4/5 respectively; sharps for octaves 3-4 sit on the number row (1-9, 0)
// and octave 5's sharps sit on I O P [ ].
var bpsrKeys = map[uint8]Key{
	// Octave 3 (C3-B3, MIDI 48-59)
	48: {VK: vkLetter('Z')}, // C3
	49: {VK: vkDigit(1)},    // C#3
	50: {VK: vkLetter('X')}, // D3
	51: {VK: vkDigit(2)},    // D#3
	52: {VK: vkLetter('C')}, // E3
	53: {VK: vkLetter('V')}, // F3
	54: {VK: vkDigit(3)},    // F#3
	55: {VK: vkLetter('B')}, // G3
	56: {VK: vkDigit(4)},    // G#3
	57: {VK: vkLetter('N')}, // A3
	58: {VK: vkDigit(5)},    // A#3
	59: {VK: vkLetter('M')}, // B3

	// Octave 4 (C4-B4, MIDI 60-71)
	60: {VK: vkLetter('A')}, // C4
	61: {VK: vkDigit(6)},    // C#4
	62: {VK: vkLetter('S')}, // D4
	63: {VK: vkDigit(7)},    // D#4
	64: {VK: vkLetter('D')}, // E4
	65: {VK: vkLetter('F')}, // F4
	66: {VK: vkDigit(8)},    // F#4
	67: {VK: vkLetter('G')}, // G4
	68: {VK: vkDigit(9)},    // G#4
	69: {VK: vkLetter('H')}, // A4
	70: {VK: vkDigit(0)},    // A#4
	71: {VK: vkLetter('J')}, // B4

	// Octave 5 (C5-B5, MIDI 72-83)
	72: {VK: vkLetter('Q')}, // C5
	73: {VK: vkLetter('I')}, // C#5
	74: {VK: vkLetter('W')}, // D5
	75: {VK: vkLetter('O')}, // D#5
	76: {VK: vkLetter('E')}, // E5
	77: {VK: vkLetter('R')}, // F5
	78: {VK: vkLetter('P')}, // F#5
	79: {VK: vkLetter('T')}, // G5
	80: {VK: vkOEM4},        // G#5 '['
	81: {VK: vkLetter('Y')}, // A5
	82: {VK: vkOEM6},        // A#5 ']'
	83: {VK: vkLetter('U')}, // B5
}

type bpsrProfile struct{}

// BPSR returns the key-mapping profile for Blue Protocol: Star Resonance's
// 36-key instrument. Holding Left Shift transposes the whole board up one
// octave; holding Left Ctrl transposes it down one octave. Combined with the
// unshifted range this reaches MIDI notes 36-95 (C2-B6).
func BPSR() Profile { return bpsrProfile{} }

func (bpsrProfile) Name() string { return "BPSR" }

func (bpsrProfile) Resolve(note uint8, preferredShift Shift) (Resolution, bool) {
	for _, s := range shiftTryOrder(preferredShift) {
		baseNote, ok := unshiftedNote(note, s)
		if !ok {
			continue
		}
		key, ok := bpsrKeys[baseNote]
		if !ok {
			continue
		}
		return Resolution{Key: key, Shift: s}, true
	}
	return Resolution{}, false
}

// unshiftedNote returns the note that would need to sound on the unshifted
// board for shift s to produce note, and whether that note falls within the
// unshifted range at all.
func unshiftedNote(note uint8, s Shift) (uint8, bool) {
	n := int(note)
	switch s {
	case ShiftUp:
		n -= 12
	case ShiftDown:
		n += 12
	}
	if n < bpsrBaseLow || n > bpsrBaseHigh {
		return 0, false
	}
	return uint8(n), true
}

// shiftTryOrder returns the shift states to attempt, trying preferred first
// to avoid unnecessary modifier toggling (see PROJECT_CONTEXT.md section 6).
func shiftTryOrder(preferred Shift) [3]Shift {
	all := [3]Shift{ShiftNone, ShiftUp, ShiftDown}
	order := [3]Shift{preferred}
	i := 1
	for _, s := range all {
		if s == preferred {
			continue
		}
		order[i] = s
		i++
	}
	return order
}

func (bpsrProfile) ShiftKey(s Shift) (Key, bool) {
	switch s {
	case ShiftUp:
		return Key{VK: vkLShift}, true
	case ShiftDown:
		return Key{VK: vkLControl}, true
	default:
		return Key{}, false
	}
}

func (bpsrProfile) Sustain() Key { return Key{VK: vkSpace} }
