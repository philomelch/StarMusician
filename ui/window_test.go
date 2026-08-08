package ui

import (
	"path/filepath"
	"testing"

	"fyne.io/fyne/v2/test"
	"fyne.io/fyne/v2/widget"
	gomidi "gitlab.com/gomidi/midi/v2"
	"gitlab.com/gomidi/midi/v2/smf"

	"github.com/philomelch/StarMusician/internal/engine"
	"github.com/philomelch/StarMusician/internal/hotkey"
	"github.com/philomelch/StarMusician/internal/input"
	"github.com/philomelch/StarMusician/internal/keymap"
)

// buildTestSongFile writes a tiny two-part .mid file (matching the fixture
// pattern used in internal/midi's own tests) so window tests can exercise
// loadFile without touching the real filesystem's ./midi directory.
func buildTestSongFile(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "test.mid")

	s := smf.NewSMF1()

	var meta smf.Track
	meta.Add(0, smf.MetaTempo(120))
	meta.Close(0)
	if err := s.Add(meta); err != nil {
		t.Fatalf("adding meta track: %v", err)
	}

	var piano smf.Track
	piano.Add(0, smf.MetaTrackSequenceName("Piano"))
	piano.Add(0, gomidi.NoteOn(0, 60, 100))
	piano.Add(480, gomidi.NoteOff(0, 60))
	piano.Close(0)
	if err := s.Add(piano); err != nil {
		t.Fatalf("adding piano track: %v", err)
	}

	var guitar smf.Track
	guitar.Add(0, gomidi.NoteOn(1, 64, 90))
	guitar.Add(240, gomidi.NoteOff(1, 64))
	guitar.Close(0)
	if err := s.Add(guitar); err != nil {
		t.Fatalf("adding guitar track: %v", err)
	}

	if err := s.WriteFile(path); err != nil {
		t.Fatalf("writing test midi file: %v", err)
	}
	return path
}

// newTestWindow builds a window against Fyne's headless test app/driver (no
// real display needed). Build() registers a real global F9 hotkey via
// internal/hotkey, so cleanup closes it — otherwise a later test's Build()
// would fail to (re-)register the same key.
func newTestWindow(t *testing.T) *appWindow {
	t.Helper()
	a := test.NewApp()
	t.Cleanup(a.Quit)
	win := a.NewWindow("test")
	w := Build(win)
	t.Cleanup(func() {
		if w.hk != nil {
			w.hk.Close()
		}
	})
	return w
}

func TestBuildInitialStateHasNothingLoaded(t *testing.T) {
	w := newTestWindow(t)

	if !w.playBtn.Disabled() {
		t.Error("playBtn should start disabled with nothing loaded")
	}
	if len(w.partsBox.Objects) != 0 {
		t.Error("part checklist should start empty with nothing loaded")
	}
	if !w.stopBtn.Disabled() {
		t.Error("stopBtn should start disabled")
	}
}

func TestLoadFilePopulatesPartsAndAutoSelectsFirst(t *testing.T) {
	w := newTestWindow(t)
	path := buildTestSongFile(t)

	w.loadFile(path)

	if len(w.partsBox.Objects) != 2 {
		t.Fatalf("got %d part checkboxes, want 2: %v", len(w.partsBox.Objects), w.partsBox.Objects)
	}
	if !w.selected[0] || w.selected[1] {
		t.Errorf("selected = %v, want only part 0 auto-checked (highest note count)", w.selected)
	}
	if w.playBtn.Disabled() {
		t.Error("playBtn should be enabled once a file with parts is loaded")
	}
	for i, obj := range w.partsBox.Objects {
		if obj.(*widget.Check).Disabled() {
			t.Errorf("checkbox %d should be enabled once a file with parts is loaded", i)
		}
	}
}

func TestCheckingAnotherPartAddsToSelection(t *testing.T) {
	w := newTestWindow(t)
	w.loadFile(buildTestSongFile(t))

	if len(w.partsBox.Objects) != 2 {
		t.Fatalf("test setup: want 2 part checkboxes, got %d", len(w.partsBox.Objects))
	}

	second := w.partsBox.Objects[1].(*widget.Check)
	second.SetChecked(true)
	if !w.selected[0] || !w.selected[1] {
		t.Errorf("selected = %v, want both parts checked", w.selected)
	}
	if w.playBtn.Disabled() {
		t.Error("playBtn should stay enabled with multiple parts checked")
	}

	first := w.partsBox.Objects[0].(*widget.Check)
	first.SetChecked(false)
	if w.selected[0] || !w.selected[1] {
		t.Errorf("selected = %v, want only part 1 checked", w.selected)
	}
	if w.playBtn.Disabled() {
		t.Error("playBtn should stay enabled with at least one part checked")
	}

	second.SetChecked(false)
	if w.anySelected() {
		t.Errorf("selected = %v, want none checked", w.selected)
	}
	if !w.playBtn.Disabled() {
		t.Error("playBtn should be disabled once nothing is checked")
	}
}

func TestLoadFileWithNoNotesDisablesPlay(t *testing.T) {
	w := newTestWindow(t)

	dir := t.TempDir()
	path := filepath.Join(dir, "empty.mid")
	s := smf.NewSMF1()
	var meta smf.Track
	meta.Add(0, smf.MetaTempo(120))
	meta.Close(0)
	if err := s.Add(meta); err != nil {
		t.Fatalf("adding meta track: %v", err)
	}
	if err := s.WriteFile(path); err != nil {
		t.Fatalf("writing empty midi file: %v", err)
	}

	w.loadFile(path)

	if !w.playBtn.Disabled() {
		t.Error("playBtn should stay disabled for a file with no instrument parts")
	}
	if len(w.partsBox.Objects) != 0 {
		t.Error("part checklist should stay empty for a file with no instrument parts")
	}
}

func TestLoadFileMissingPathShowsErrorAndClearsState(t *testing.T) {
	w := newTestWindow(t)
	w.loadFile(buildTestSongFile(t)) // load something first
	w.loadFile(filepath.Join(t.TempDir(), "does-not-exist.mid"))

	if w.song != nil {
		t.Error("song should be cleared after a failed load")
	}
	if !w.playBtn.Disabled() {
		t.Error("playBtn should be disabled after a failed load")
	}
}

func TestUpdatePlayEnabledReflectsPlayingState(t *testing.T) {
	w := newTestWindow(t)
	w.loadFile(buildTestSongFile(t))

	if w.playBtn.Disabled() {
		t.Fatal("test setup: playBtn should be enabled before simulating playback")
	}

	// Simulate an in-flight Play() the way onPlay() would, without actually
	// injecting keystrokes (engine.New with a real Injector is safe to
	// construct and never call Press/Release/ReleaseAll on).
	w.mu.Lock()
	w.player = engine.New(keymap.BPSR(), input.New())
	w.mu.Unlock()
	w.updatePlayEnabled()

	if !w.playBtn.Disabled() {
		t.Error("playBtn should be disabled while playing")
	}
	if w.stopBtn.Disabled() {
		t.Error("stopBtn should be enabled while playing")
	}
	if !w.fileSelect.Disabled() {
		t.Error("fileSelect should be disabled while playing")
	}
	if !w.refreshBtn.Disabled() {
		t.Error("refreshBtn should be disabled while playing (swapping the loaded file mid-play would desync the running Play() call)")
	}
	if !w.browseBtn.Disabled() {
		t.Error("browseBtn should be disabled while playing")
	}
	if !w.stopKeySelect.Disabled() {
		t.Error("stopKeySelect should be disabled while playing (switching it risks a window with no hotkey registered)")
	}
	for i, obj := range w.partsBox.Objects {
		if !obj.(*widget.Check).Disabled() {
			t.Errorf("checkbox %d should be disabled while playing", i)
		}
	}

	w.mu.Lock()
	w.player = nil
	w.mu.Unlock()
	w.updatePlayEnabled()

	if w.playBtn.Disabled() {
		t.Error("playBtn should be re-enabled once playback ends")
	}
	if !w.stopBtn.Disabled() {
		t.Error("stopBtn should be disabled once playback ends")
	}
	if w.refreshBtn.Disabled() || w.browseBtn.Disabled() || w.stopKeySelect.Disabled() {
		t.Error("refreshBtn/browseBtn/stopKeySelect should be re-enabled once playback ends")
	}
}

func TestStopActiveIsNilSafe(t *testing.T) {
	w := newTestWindow(t)
	w.stopActive() // must not panic with no active player
}

func TestSemitonesForTransposeLabel(t *testing.T) {
	cases := map[string]int{
		"None":                  0,
		"1 octave down (-12)":   -12,
		"Half octave down (-6)": -6,
		"Half octave up (+6)":   6,
		"1 octave up (+12)":     12,
		"not a real option":     0,
	}
	for label, want := range cases {
		if got := semitonesForTransposeLabel(label); got != want {
			t.Errorf("semitonesForTransposeLabel(%q) = %d, want %d", label, got, want)
		}
	}
}

func TestBuildDefaultsTransposeToNone(t *testing.T) {
	w := newTestWindow(t)
	if w.transposeSelect.Selected != "None" {
		t.Errorf("transposeSelect.Selected = %q, want %q", w.transposeSelect.Selected, "None")
	}
}

func TestOnStopKeyChangedKeepsOldHotkeyIfNewRegistrationFails(t *testing.T) {
	w := newTestWindow(t) // registers F9 by default

	// Occupy F10 system-wide from a separate listener (its own dedicated OS
	// thread, per internal/hotkey) so the window's attempt to switch to it
	// is guaranteed to fail with a real registration conflict.
	blocker, err := hotkey.Start(hotkey.KeyF10, func() {})
	if err != nil {
		t.Skipf("could not occupy F10 to set up this test: %v", err)
	}
	t.Cleanup(func() { blocker.Close() })

	oldListener := w.hk
	if oldListener == nil {
		t.Fatal("test setup: window should already have a hotkey registered")
	}

	w.onStopKeyChanged("F10")

	if w.hk != oldListener {
		t.Error("w.hk changed even though the new registration should have failed — the previous hotkey must stay active rather than leaving none registered")
	}
}

func TestParseStopKeyName(t *testing.T) {
	cases := map[string]bool{"Esc": true, "esc": true, "F8": true, "F9": true, "F10": true, "Alt": false, "": false}
	for name, wantOK := range cases {
		_, ok := parseStopKeyName(name)
		if ok != wantOK {
			t.Errorf("parseStopKeyName(%q) ok = %v, want %v", name, ok, wantOK)
		}
	}
}
