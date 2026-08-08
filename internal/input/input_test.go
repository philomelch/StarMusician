package input

import (
	"errors"
	"testing"
	"time"
)

type call struct {
	vk       uint16
	extended bool
	keyUp    bool
}

func fakeInjector(t *testing.T) (*Injector, *[]call) {
	t.Helper()
	var calls []call
	in := newInjector(
		func(vk uint16, extended, keyUp bool) error {
			calls = append(calls, call{vk, extended, keyUp})
			return nil
		},
		func(time.Duration) {},
	)
	return in, &calls
}

func TestPressFreshKeySendsSingleKeyDown(t *testing.T) {
	in, calls := fakeInjector(t)

	if err := in.Press(0x41, false); err != nil {
		t.Fatalf("Press: %v", err)
	}

	want := []call{{0x41, false, false}}
	if !equalCalls(*calls, want) {
		t.Errorf("calls = %+v, want %+v", *calls, want)
	}
}

func TestPressHeldKeyRetriggers(t *testing.T) {
	in, calls := fakeInjector(t)

	if err := in.Press(0x41, false); err != nil {
		t.Fatalf("first Press: %v", err)
	}
	*calls = nil // only care about the second Press's sequence

	if err := in.Press(0x41, false); err != nil {
		t.Fatalf("second Press: %v", err)
	}

	// Overlapping note on the same key: release, pause, press again.
	want := []call{{0x41, false, true}, {0x41, false, false}}
	if !equalCalls(*calls, want) {
		t.Errorf("calls = %+v, want %+v (retrigger sequence)", *calls, want)
	}
}

func TestReleaseKeepsKeyDownWhileStillHeld(t *testing.T) {
	in, calls := fakeInjector(t)

	must(t, in.Press(0x41, false))
	must(t, in.Press(0x41, false)) // second overlapping note on same key
	*calls = nil

	// One of the two overlapping notes ends: key must stay down.
	must(t, in.Release(0x41, false))
	if len(*calls) != 0 {
		t.Errorf("calls after first Release = %+v, want none (key still held by other note)", *calls)
	}

	// The last note ends: now the key should actually come up.
	must(t, in.Release(0x41, false))
	want := []call{{0x41, false, true}}
	if !equalCalls(*calls, want) {
		t.Errorf("calls after second Release = %+v, want %+v", *calls, want)
	}
}

func TestReleaseUnheldKeyIsNoOp(t *testing.T) {
	in, calls := fakeInjector(t)

	if err := in.Release(0x41, false); err != nil {
		t.Fatalf("Release on unheld key: %v", err)
	}
	if len(*calls) != 0 {
		t.Errorf("calls = %+v, want none", *calls)
	}
}

func TestReleaseAllReleasesEveryHeldKeyAndClearsState(t *testing.T) {
	in, calls := fakeInjector(t)

	must(t, in.Press(0x41, false))
	must(t, in.Press(0x42, false))
	must(t, in.Press(0x42, false)) // held twice; ReleaseAll must still only send one key-up
	*calls = nil

	if err := in.ReleaseAll(); err != nil {
		t.Fatalf("ReleaseAll: %v", err)
	}

	if len(in.held) != 0 {
		t.Errorf("held map after ReleaseAll = %+v, want empty", in.held)
	}

	gotUps := map[uint16]int{}
	for _, c := range *calls {
		if !c.keyUp {
			t.Errorf("ReleaseAll sent a key-down: %+v", c)
			continue
		}
		gotUps[c.vk]++
	}
	want := map[uint16]int{0x41: 1, 0x42: 1}
	for vk, n := range want {
		if gotUps[vk] != n {
			t.Errorf("key-up count for vk %#x = %d, want %d", vk, gotUps[vk], n)
		}
	}
}

func TestPressPropagatesSendError(t *testing.T) {
	wantErr := errors.New("boom")
	in := newInjector(
		func(vk uint16, extended, keyUp bool) error { return wantErr },
		func(time.Duration) {},
	)

	if err := in.Press(0x41, false); !errors.Is(err, wantErr) {
		t.Errorf("Press error = %v, want %v", err, wantErr)
	}
}

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func equalCalls(got, want []call) bool {
	if len(got) != len(want) {
		return false
	}
	for i := range got {
		if got[i] != want[i] {
			return false
		}
	}
	return true
}
