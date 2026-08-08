package keymap

import "testing"

func TestBPSRResolveUnshifted(t *testing.T) {
	p := BPSR()

	res, ok := p.Resolve(60, ShiftNone) // C4, base layout
	if !ok {
		t.Fatal("Resolve(60, ShiftNone): want ok, got false")
	}
	if res.Shift != ShiftNone {
		t.Errorf("Shift = %v, want ShiftNone", res.Shift)
	}
	if res.Key.VK != vkLetter('A') {
		t.Errorf("VK = %#x, want VK_A", res.Key.VK)
	}
}

func TestBPSRResolvePrefersActiveShift(t *testing.T) {
	p := BPSR()

	// MIDI 60 (C4) is reachable unshifted (base note 60) AND with ShiftDown
	// held (base note 72, Q). If ShiftDown is already active, Resolve must
	// stay in ShiftDown rather than drop back to ShiftNone.
	res, ok := p.Resolve(60, ShiftDown)
	if !ok {
		t.Fatal("Resolve(60, ShiftDown): want ok, got false")
	}
	if res.Shift != ShiftDown {
		t.Errorf("Shift = %v, want ShiftDown (should prefer currently active shift)", res.Shift)
	}
	if res.Key.VK != vkLetter('Q') {
		t.Errorf("VK = %#x, want VK_Q", res.Key.VK)
	}
}

func TestBPSRResolveRequiresShift(t *testing.T) {
	p := BPSR()

	// MIDI 36 (C2) is only reachable with ShiftDown held (base note 48, Z).
	res, ok := p.Resolve(36, ShiftNone)
	if !ok {
		t.Fatal("Resolve(36, ShiftNone): want ok, got false")
	}
	if res.Shift != ShiftDown {
		t.Errorf("Shift = %v, want ShiftDown", res.Shift)
	}
	if res.Key.VK != vkLetter('Z') {
		t.Errorf("VK = %#x, want VK_Z", res.Key.VK)
	}

	// MIDI 95 (B6) is only reachable with ShiftUp held (base note 83, U).
	res, ok = p.Resolve(95, ShiftNone)
	if !ok {
		t.Fatal("Resolve(95, ShiftNone): want ok, got false")
	}
	if res.Shift != ShiftUp {
		t.Errorf("Shift = %v, want ShiftUp", res.Shift)
	}
	if res.Key.VK != vkLetter('U') {
		t.Errorf("VK = %#x, want VK_U", res.Key.VK)
	}
}

func TestBPSRResolveOutOfRange(t *testing.T) {
	p := BPSR()

	for _, note := range []uint8{35, 96, 0, 127} {
		if _, ok := p.Resolve(note, ShiftNone); ok {
			t.Errorf("Resolve(%d, ShiftNone): want ok=false, got true", note)
		}
	}
}

func TestBPSRAllReachableNotesResolve(t *testing.T) {
	p := BPSR()
	for note := 36; note <= 95; note++ {
		if _, ok := p.Resolve(uint8(note), ShiftNone); !ok {
			t.Errorf("Resolve(%d, ShiftNone): want ok=true, got false", note)
		}
	}
}

func TestBPSRShiftKeysAndSustain(t *testing.T) {
	p := BPSR()

	if _, ok := p.ShiftKey(ShiftNone); ok {
		t.Error("ShiftKey(ShiftNone): want ok=false, got true")
	}
	if k, ok := p.ShiftKey(ShiftUp); !ok || k.VK != vkLShift {
		t.Errorf("ShiftKey(ShiftUp) = %+v, %v; want VK_LSHIFT, true", k, ok)
	}
	if k, ok := p.ShiftKey(ShiftDown); !ok || k.VK != vkLControl {
		t.Errorf("ShiftKey(ShiftDown) = %+v, %v; want VK_LCONTROL, true", k, ok)
	}
	if k := p.Sustain(); k.VK != vkSpace {
		t.Errorf("Sustain() = %+v, want VK_SPACE", k)
	}
}
