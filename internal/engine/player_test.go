package engine

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/philomelch/StarMusician/internal/keymap"
	"github.com/philomelch/StarMusician/internal/midi"
)

type injCall struct {
	kind     string // "press", "release", "releaseAll"
	vk       uint16
	extended bool
}

type fakeInjector struct {
	mu    sync.Mutex
	calls []injCall
	err   error
}

func (f *fakeInjector) Press(vk uint16, extended bool) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, injCall{"press", vk, extended})
	return f.err
}

func (f *fakeInjector) Release(vk uint16, extended bool) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, injCall{"release", vk, extended})
	return f.err
}

func (f *fakeInjector) ReleaseAll() error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, injCall{kind: "releaseAll"})
	return nil
}

func (f *fakeInjector) snapshot() []injCall {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]injCall, len(f.calls))
	copy(out, f.calls)
	return out
}

type fakeForeground struct {
	ok  bool
	err error
}

func (f fakeForeground) Matches() (bool, error) { return f.ok, f.err }

func TestDispatchNoteOnThenNoteOff(t *testing.T) {
	profile := keymap.BPSR()
	inj := &fakeInjector{}
	p := New(profile, inj)

	res, ok := profile.Resolve(60, keymap.ShiftNone)
	if !ok {
		t.Fatal("test setup: note 60 should resolve")
	}

	must(t, p.dispatch(midi.Event{Type: midi.NoteOn, Note: 60}))
	must(t, p.dispatch(midi.Event{Type: midi.NoteOff, Note: 60}))

	want := []injCall{
		{"press", res.Key.VK, res.Key.Extended},
		{"release", res.Key.VK, res.Key.Extended},
	}
	assertCalls(t, inj.snapshot(), want)

	if len(p.active) != 0 {
		t.Errorf("active notes after note-off = %v, want empty", p.active)
	}
}

func TestDispatchOutOfRangeNoteIsDropped(t *testing.T) {
	inj := &fakeInjector{}
	p := New(keymap.BPSR(), inj)

	must(t, p.dispatch(midi.Event{Type: midi.NoteOn, Note: 120}))
	must(t, p.dispatch(midi.Event{Type: midi.NoteOff, Note: 120}))

	if calls := inj.snapshot(); len(calls) != 0 {
		t.Errorf("calls = %+v, want none for an out-of-range note", calls)
	}
}

func TestDispatchSustain(t *testing.T) {
	profile := keymap.BPSR()
	inj := &fakeInjector{}
	p := New(profile, inj)

	sustainKey := profile.Sustain()

	must(t, p.dispatch(midi.Event{Type: midi.Sustain, On: true}))
	must(t, p.dispatch(midi.Event{Type: midi.Sustain, On: false}))

	want := []injCall{
		{"press", sustainKey.VK, sustainKey.Extended},
		{"release", sustainKey.VK, sustainKey.Extended},
	}
	assertCalls(t, inj.snapshot(), want)
}

func TestDispatchOverlappingNotesOnSameKeyReleaseIndependently(t *testing.T) {
	profile := keymap.BPSR()
	inj := &fakeInjector{}
	p := New(profile, inj)

	res, ok := profile.Resolve(60, keymap.ShiftNone)
	if !ok {
		t.Fatal("test setup: note 60 should resolve")
	}

	// Same pitch on two different channels overlapping in time: both press
	// the same physical key, and each must be released independently.
	must(t, p.dispatch(midi.Event{Type: midi.NoteOn, Channel: 0, Note: 60}))
	must(t, p.dispatch(midi.Event{Type: midi.NoteOn, Channel: 1, Note: 60}))

	if len(p.active) != 2 {
		t.Fatalf("active notes = %v, want 2 entries", p.active)
	}

	must(t, p.dispatch(midi.Event{Type: midi.NoteOff, Channel: 0, Note: 60}))
	if len(p.active) != 1 {
		t.Fatalf("active notes after first release = %v, want 1 entry left", p.active)
	}

	must(t, p.dispatch(midi.Event{Type: midi.NoteOff, Channel: 1, Note: 60}))
	if len(p.active) != 0 {
		t.Fatalf("active notes after both released = %v, want empty", p.active)
	}

	want := []injCall{
		{"press", res.Key.VK, res.Key.Extended},
		{"press", res.Key.VK, res.Key.Extended},
		{"release", res.Key.VK, res.Key.Extended},
		{"release", res.Key.VK, res.Key.Extended},
	}
	assertCalls(t, inj.snapshot(), want)
}

func TestDispatchOverlappingNotesOnSameKeyDifferentTracksSameChannel(t *testing.T) {
	profile := keymap.BPSR()
	inj := &fakeInjector{}
	p := New(profile, inj)

	res, ok := profile.Resolve(60, keymap.ShiftNone)
	if !ok {
		t.Fatal("test setup: note 60 should resolve")
	}

	// Two different instrument parts (e.g. merged via Song.FilterParts for
	// multi-part playback) commonly share MIDI channel 0 in real files, and
	// can legitimately overlap the exact same pitch. This must not collide
	// with the channel+note-only key a naive implementation would use.
	must(t, p.dispatch(midi.Event{Type: midi.NoteOn, Track: 1, Channel: 0, Note: 60}))
	must(t, p.dispatch(midi.Event{Type: midi.NoteOn, Track: 2, Channel: 0, Note: 60}))

	if len(p.active) != 2 {
		t.Fatalf("active notes = %v, want 2 entries (distinct tracks)", p.active)
	}

	must(t, p.dispatch(midi.Event{Type: midi.NoteOff, Track: 1, Channel: 0, Note: 60}))
	if len(p.active) != 1 {
		t.Fatalf("active notes after releasing track 1 = %v, want 1 entry left (track 2 still sounding)", p.active)
	}

	must(t, p.dispatch(midi.Event{Type: midi.NoteOff, Track: 2, Channel: 0, Note: 60}))
	if len(p.active) != 0 {
		t.Fatalf("active notes after both released = %v, want empty", p.active)
	}

	want := []injCall{
		{"press", res.Key.VK, res.Key.Extended},
		{"press", res.Key.VK, res.Key.Extended},
		{"release", res.Key.VK, res.Key.Extended},
		{"release", res.Key.VK, res.Key.Extended},
	}
	assertCalls(t, inj.snapshot(), want)
}

func TestDispatchReOnsetBeforeNoteOffReleasesLIFO(t *testing.T) {
	profile := keymap.BPSR()
	inj := &fakeInjector{}
	p := New(profile, inj)

	res, ok := profile.Resolve(60, keymap.ShiftNone)
	if !ok {
		t.Fatal("test setup: note 60 should resolve")
	}

	// The exact same (track, channel, note) re-onsets before its own
	// note-off fires — a real if unusual pattern in some MIDI exports.
	// Each instance must get its own tracked entry so both note-offs
	// correctly release the key, one per matching press.
	must(t, p.dispatch(midi.Event{Type: midi.NoteOn, Track: 0, Channel: 0, Note: 60}))
	must(t, p.dispatch(midi.Event{Type: midi.NoteOn, Track: 0, Channel: 0, Note: 60}))

	nk := noteKey{track: 0, channel: 0, note: 60}
	if len(p.active[nk]) != 2 {
		t.Fatalf("active[%v] = %v, want 2 stacked instances", nk, p.active[nk])
	}

	must(t, p.dispatch(midi.Event{Type: midi.NoteOff, Track: 0, Channel: 0, Note: 60}))
	if len(p.active[nk]) != 1 {
		t.Fatalf("active[%v] after one release = %v, want 1 instance left", nk, p.active[nk])
	}

	must(t, p.dispatch(midi.Event{Type: midi.NoteOff, Track: 0, Channel: 0, Note: 60}))
	if _, exists := p.active[nk]; exists {
		t.Errorf("active[%v] should be removed once its last instance is released, got %v", nk, p.active[nk])
	}

	want := []injCall{
		{"press", res.Key.VK, res.Key.Extended},
		{"press", res.Key.VK, res.Key.Extended},
		{"release", res.Key.VK, res.Key.Extended},
		{"release", res.Key.VK, res.Key.Extended},
	}
	assertCalls(t, inj.snapshot(), want)
}

func TestEnsureShiftOnlyTogglesWhenTargetOctaveChanges(t *testing.T) {
	profile := keymap.BPSR()
	inj := &fakeInjector{}
	p := New(profile, inj)

	// Two notes in a row that both resolve to ShiftNone: no modifier key
	// should ever be touched.
	must(t, p.dispatch(midi.Event{Type: midi.NoteOn, Channel: 0, Note: 60}))
	must(t, p.dispatch(midi.Event{Type: midi.NoteOn, Channel: 0, Note: 62}))
	for _, c := range inj.snapshot() {
		if c.vk != mustResolve(t, profile, 60).VK && c.vk != mustResolve(t, profile, 62).VK {
			t.Errorf("unexpected call touching a non-note key: %+v", c)
		}
	}

	// A note only reachable via ShiftUp (95 = B6) must press the shift key
	// exactly once before the note key.
	inj2 := &fakeInjector{}
	p2 := New(profile, inj2)
	must(t, p2.dispatch(midi.Event{Type: midi.NoteOn, Channel: 0, Note: 95}))

	shiftUpKey, ok := profile.ShiftKey(keymap.ShiftUp)
	if !ok {
		t.Fatal("test setup: ShiftUp should have a key")
	}
	res95, ok := profile.Resolve(95, keymap.ShiftNone)
	if !ok || res95.Shift != keymap.ShiftUp {
		t.Fatal("test setup: note 95 should require ShiftUp")
	}

	want := []injCall{
		{"press", shiftUpKey.VK, shiftUpKey.Extended},
		{"press", res95.Key.VK, res95.Key.Extended},
	}
	assertCalls(t, inj2.snapshot(), want)
	if p2.currentShift != keymap.ShiftUp {
		t.Errorf("currentShift = %v, want ShiftUp", p2.currentShift)
	}

	// A subsequent note that requires switching to ShiftDown must first
	// release the still-held note 95 (the modifier is about to change what
	// its key sounds like — see ensureShift), then release the old modifier
	// before pressing the new one.
	res36, ok := profile.Resolve(36, keymap.ShiftUp) // only reachable via ShiftDown
	if !ok || res36.Shift != keymap.ShiftDown {
		t.Fatal("test setup: note 36 should require ShiftDown")
	}
	must(t, p2.dispatch(midi.Event{Type: midi.NoteOn, Channel: 0, Note: 36}))

	shiftDownKey, _ := profile.ShiftKey(keymap.ShiftDown)
	calls := inj2.snapshot()
	last4 := calls[len(calls)-4:]
	want2 := []injCall{
		{"release", res95.Key.VK, res95.Key.Extended},
		{"release", shiftUpKey.VK, shiftUpKey.Extended},
		{"press", shiftDownKey.VK, shiftDownKey.Extended},
		{"press", res36.Key.VK, res36.Key.Extended},
	}
	assertCalls(t, last4, want2)
	if len(p2.active) != 1 {
		t.Errorf("active notes after toggle = %v, want only note 36 (note 95 was cut short by the toggle)", p2.active)
	}
}

func TestEnsureShiftCutsShortHeldNotesBeforeToggling(t *testing.T) {
	profile := keymap.BPSR()
	inj := &fakeInjector{}
	p := New(profile, inj)

	// Two overlapping notes both reachable under ShiftNone (the currently
	// active shift, so no toggle yet).
	res60, ok := profile.Resolve(60, keymap.ShiftNone)
	if !ok {
		t.Fatal("test setup: note 60 should resolve under ShiftNone")
	}
	res48, ok := profile.Resolve(48, keymap.ShiftNone)
	if !ok {
		t.Fatal("test setup: note 48 should resolve under ShiftNone")
	}
	must(t, p.dispatch(midi.Event{Type: midi.NoteOn, Channel: 0, Note: 60}))
	must(t, p.dispatch(midi.Event{Type: midi.NoteOn, Channel: 0, Note: 48}))
	if len(p.active) != 2 {
		t.Fatalf("active notes = %v, want 2 held notes", p.active)
	}

	// A third, still-overlapping note only reachable via ShiftUp forces a
	// toggle. Both held notes must be released first: holding the modifier
	// would otherwise silently change what pitch their keys sound.
	must(t, p.dispatch(midi.Event{Type: midi.NoteOn, Channel: 0, Note: 95}))

	calls := inj.snapshot()
	releasedBeforeToggle := map[uint16]bool{}
	for _, c := range calls[2:] { // skip the two initial note-on presses
		if c.kind == "release" && (c.vk == res60.Key.VK || c.vk == res48.Key.VK) {
			releasedBeforeToggle[c.vk] = true
		}
	}
	if !releasedBeforeToggle[res60.Key.VK] || !releasedBeforeToggle[res48.Key.VK] {
		t.Errorf("calls = %+v, want both held notes (60 and 48) released before the ShiftUp toggle", calls)
	}
	if len(p.active) != 1 {
		t.Errorf("active notes after toggle = %v, want only note 95 (60 and 48 were cut short)", p.active)
	}

	// The now-orphaned note-offs for the cut-short notes must be harmless.
	must(t, p.dispatch(midi.Event{Type: midi.NoteOff, Channel: 0, Note: 60}))
	must(t, p.dispatch(midi.Event{Type: midi.NoteOff, Channel: 0, Note: 48}))
}

func mustResolve(t *testing.T, p keymap.Profile, note uint8) keymap.Key {
	t.Helper()
	res, ok := p.Resolve(note, keymap.ShiftNone)
	if !ok {
		t.Fatalf("note %d should resolve", note)
	}
	return res.Key
}

func TestPlayReportsProgressTicksBetweenSparseEvents(t *testing.T) {
	var mu sync.Mutex
	var reports []Progress

	p := New(keymap.BPSR(), &fakeInjector{}, WithCountdown(0), WithProgress(func(pr Progress) {
		mu.Lock()
		reports = append(reports, pr)
		mu.Unlock()
	}))
	p.progressTickInterval = 5 * time.Millisecond

	// A 60ms gap with nothing in between: without ticking, progress would
	// only be reported twice (once per event). With ticking at 5ms, several
	// more reports should land in between.
	events := []midi.Event{
		{Time: 0, Type: midi.NoteOn, Note: 60},
		{Time: 0.06, Type: midi.NoteOff, Note: 60},
	}

	if err := p.Play(context.Background(), events); err != nil {
		t.Fatalf("Play: %v", err)
	}

	mu.Lock()
	got := len(reports)
	mu.Unlock()

	if got < 5 {
		t.Errorf("got %d progress reports over a 60ms gap ticking every 5ms, want at least 5", got)
	}
}

func TestPlayStopsProgressTickerAfterNormalCompletion(t *testing.T) {
	var mu sync.Mutex
	var count int

	p := New(keymap.BPSR(), &fakeInjector{}, WithCountdown(0), WithProgress(func(Progress) {
		mu.Lock()
		count++
		mu.Unlock()
	}))
	p.progressTickInterval = 5 * time.Millisecond

	events := []midi.Event{{Time: 0, Type: midi.NoteOn, Note: 60}}
	if err := p.Play(context.Background(), events); err != nil {
		t.Fatalf("Play: %v", err)
	}

	mu.Lock()
	countAtReturn := count
	mu.Unlock()

	// If reportProgressTicks' goroutine leaked (never saw its context
	// cancelled), it would keep firing every 5ms indefinitely; waiting ten
	// tick-intervals gives it ample opportunity to prove that.
	time.Sleep(50 * time.Millisecond)

	mu.Lock()
	countAfterWait := count
	mu.Unlock()

	if countAfterWait != countAtReturn {
		t.Errorf("progress reports kept arriving after Play() returned (%d at return, %d after waiting) — the progress-ticker goroutine leaked", countAtReturn, countAfterWait)
	}
}

func TestPlayDispatchesInOrderAndReleasesAllAtEnd(t *testing.T) {
	profile := keymap.BPSR()
	inj := &fakeInjector{}
	p := New(profile, inj, WithCountdown(0))

	events := []midi.Event{
		{Time: 0, Type: midi.NoteOn, Note: 60},
		{Time: 0.001, Type: midi.NoteOff, Note: 60},
	}

	if err := p.Play(context.Background(), events); err != nil {
		t.Fatalf("Play: %v", err)
	}

	res, _ := profile.Resolve(60, keymap.ShiftNone)
	calls := inj.snapshot()
	want := []injCall{
		{"press", res.Key.VK, res.Key.Extended},
		{"release", res.Key.VK, res.Key.Extended},
		{"releaseAll", 0, false},
	}
	assertCalls(t, calls, want)
}

func TestPlayRejectsConcurrentPlay(t *testing.T) {
	inj := &fakeInjector{}
	p := New(keymap.BPSR(), inj, WithCountdown(0))

	events := []midi.Event{{Time: 2, Type: midi.NoteOn, Note: 60}}

	done := make(chan error, 1)
	go func() { done <- p.Play(context.Background(), events) }()

	// Give the first Play a moment to register itself as in-progress.
	time.Sleep(20 * time.Millisecond)

	if err := p.Play(context.Background(), events); !errors.Is(err, ErrAlreadyPlaying) {
		t.Errorf("second Play() = %v, want ErrAlreadyPlaying", err)
	}

	p.Stop()
	<-done
}

func TestStopInterruptsPlayAndReleasesKeys(t *testing.T) {
	inj := &fakeInjector{}
	p := New(keymap.BPSR(), inj, WithCountdown(0))

	events := []midi.Event{
		{Time: 0, Type: midi.NoteOn, Note: 60},
		{Time: 2, Type: midi.NoteOff, Note: 60}, // far enough away that Stop must interrupt the wait
	}

	done := make(chan error, 1)
	go func() { done <- p.Play(context.Background(), events) }()

	time.Sleep(20 * time.Millisecond)
	p.Stop()

	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Errorf("Play() after Stop = %v, want context.Canceled", err)
		}
	case <-time.After(500 * time.Millisecond):
		t.Fatal("Play did not return within 500ms of Stop being called")
	}

	calls := inj.snapshot()
	if len(calls) == 0 || calls[len(calls)-1].kind != "releaseAll" {
		t.Errorf("calls = %+v, want to end with a releaseAll", calls)
	}
	// The far-future note-off must never have been reached.
	for _, c := range calls {
		if c.kind == "release" {
			t.Errorf("unexpected explicit release before Stop interrupted playback: %+v", c)
		}
	}
}

func TestStopBeforePlayHasStartedCancelsTheUpcomingPlay(t *testing.T) {
	inj := &fakeInjector{}
	p := New(keymap.BPSR(), inj, WithCountdown(0))

	// Simulates a caller (like the GUI) that marks a Player "stoppable"
	// slightly before actually invoking Play — e.g. the panic-stop hotkey
	// fires in that gap. Stop() here has no cancel func to call yet, but
	// must still be honored by the Play() that follows.
	p.Stop()

	events := []midi.Event{{Time: 0, Type: midi.NoteOn, Note: 60}}
	err := p.Play(context.Background(), events)
	if !errors.Is(err, context.Canceled) {
		t.Errorf("Play() after an earlier Stop() = %v, want context.Canceled", err)
	}

	for _, c := range inj.snapshot() {
		if c.kind == "press" {
			t.Errorf("unexpected press: Play should have been cancelled before dispatching any event, got %+v", c)
		}
	}
}

func TestStopRequestDoesNotCarryOverToALaterUnrelatedPlay(t *testing.T) {
	inj := &fakeInjector{}
	p := New(keymap.BPSR(), inj, WithCountdown(0))

	p.Stop()
	events := []midi.Event{{Time: 0, Type: midi.NoteOn, Note: 60}}
	if err := p.Play(context.Background(), events); !errors.Is(err, context.Canceled) {
		t.Fatalf("first Play() after Stop() = %v, want context.Canceled", err)
	}

	// The stopRequested flag must be consumed by that first Play() call,
	// not left armed to cancel every future one.
	if err := p.Play(context.Background(), events); err != nil {
		t.Errorf("second Play() = %v, want nil (stale Stop() must not carry over)", err)
	}
}

func TestStopDuringCountdownRespondsWithinMaxSleepChunk(t *testing.T) {
	inj := &fakeInjector{}
	p := New(keymap.BPSR(), inj, WithCountdown(5))

	events := []midi.Event{{Time: 0, Type: midi.NoteOn, Note: 60}}

	done := make(chan error, 1)
	go func() { done <- p.Play(context.Background(), events) }()

	time.Sleep(20 * time.Millisecond) // well inside the first of 5 countdown seconds
	stopAt := time.Now()
	p.Stop()

	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Errorf("Play() after Stop during countdown = %v, want context.Canceled", err)
		}
		if elapsed := time.Since(stopAt); elapsed > 200*time.Millisecond {
			t.Errorf("Play took %v to notice Stop during the countdown, want well under 1s (bounded by maxSleepChunk)", elapsed)
		}
	case <-time.After(time.Second):
		t.Fatal("Play did not return within 1s of Stop being called during the countdown — the old uncapped 1s sleep would fail this")
	}
}

func TestPlayRefusesToStartWithoutForegroundMatch(t *testing.T) {
	inj := &fakeInjector{}
	p := New(keymap.BPSR(), inj, WithCountdown(0), WithForegroundChecker(fakeForeground{ok: false}))

	events := []midi.Event{{Time: 0, Type: midi.NoteOn, Note: 60}}

	err := p.Play(context.Background(), events)
	if !errors.Is(err, ErrForegroundMismatch) {
		t.Errorf("Play() = %v, want ErrForegroundMismatch", err)
	}

	for _, c := range inj.snapshot() {
		if c.kind == "press" {
			t.Errorf("unexpected press before foreground guard passed: %+v", c)
		}
	}
}

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func assertCalls(t *testing.T, got, want []injCall) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("calls = %+v, want %+v", got, want)
	}
	for i := range got {
		if got[i] != want[i] {
			t.Errorf("call %d = %+v, want %+v", i, got[i], want[i])
		}
	}
}
