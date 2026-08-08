package hotkey

import "testing"

// These pin our Key constants to the actual Windows virtual-key codes
// (VK_ESCAPE=0x1B, VK_F8=0x77, VK_F9=0x78, VK_F10=0x79) rather than just
// trusting the underlying library's build-tagged values, since a mismatch
// here would silently register the wrong physical key.
func TestKeyConstantsMatchWindowsVirtualKeyCodes(t *testing.T) {
	cases := map[Key]uint32{
		KeyEscape: 0x1B,
		KeyF8:     0x77,
		KeyF9:     0x78,
		KeyF10:    0x79,
	}
	for k, want := range cases {
		if uint32(k) != want {
			t.Errorf("key %v = %#x, want %#x", k, uint32(k), want)
		}
	}
}

func TestDefaultKeyIsF9(t *testing.T) {
	if DefaultKey != KeyF9 {
		t.Errorf("DefaultKey = %v, want KeyF9", DefaultKey)
	}
}

func TestCloseIsSafeToCallMoreThanOnce(t *testing.T) {
	l, err := Start(KeyF10, func() {})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}

	first := l.Close()
	if first != nil {
		t.Errorf("first Close() = %v, want nil", first)
	}

	// Must not panic ("close of closed channel") and must return the same
	// result as the first call.
	second := l.Close()
	if second != first {
		t.Errorf("second Close() = %v, want the same result as the first Close() (%v)", second, first)
	}
}
