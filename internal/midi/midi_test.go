package midi

import (
	"path/filepath"
	"testing"

	"gitlab.com/gomidi/midi/v2"
	"gitlab.com/gomidi/midi/v2/smf"
)

func buildTestFile(t *testing.T) string {
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

	// Track 1 ("Piano"): C4 held for 480 ticks (a quarter of a 960-tick
	// quarter note, i.e. 0.25s at 120bpm), then sustain on.
	var notes smf.Track
	notes.Add(0, smf.MetaTrackSequenceName("Piano"))
	notes.Add(0, midi.NoteOn(0, 60, 100))
	notes.Add(480, midi.NoteOff(0, 60))
	notes.Add(0, midi.ControlChange(0, 64, 127))
	notes.Close(0)
	if err := s.Add(notes); err != nil {
		t.Fatalf("adding notes track: %v", err)
	}

	// Track 2 (unnamed): a note starting at tick 0 (t=0s) that ends before
	// track 1's note-off, to verify events from different tracks are merged
	// in time order rather than left grouped by track.
	var other smf.Track
	other.Add(0, midi.NoteOn(1, 64, 90))
	other.Add(240, midi.NoteOff(1, 64)) // 0.125s
	other.Close(0)
	if err := s.Add(other); err != nil {
		t.Fatalf("adding second notes track: %v", err)
	}

	if err := s.WriteFile(path); err != nil {
		t.Fatalf("writing test midi file: %v", err)
	}

	return path
}

func TestLoad(t *testing.T) {
	path := buildTestFile(t)

	song, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	events := song.Events

	if len(events) != 5 {
		t.Fatalf("got %d events, want 5: %+v", len(events), events)
	}

	// Expected time order: both note-ons at t=0, then track-2 note-off at
	// t=0.125s, then track-1 note-off + sustain-on at t=0.25s.
	const epsilon = 1e-6

	wantTimes := []float64{0, 0, 0.125, 0.25, 0.25}
	for i, ev := range events {
		if diff := ev.Time - wantTimes[i]; diff < -epsilon || diff > epsilon {
			t.Errorf("event %d time = %v, want %v", i, ev.Time, wantTimes[i])
		}
	}

	if events[0].Type != NoteOn || events[0].Note != 60 || events[0].Velocity != 100 {
		t.Errorf("event 0 = %+v, want NoteOn note 60 vel 100", events[0])
	}
	if events[1].Type != NoteOn || events[1].Note != 64 || events[1].Channel != 1 {
		t.Errorf("event 1 = %+v, want NoteOn note 64 channel 1", events[1])
	}
	if events[2].Type != NoteOff || events[2].Note != 64 {
		t.Errorf("event 2 = %+v, want NoteOff note 64", events[2])
	}
	if events[3].Type != NoteOff || events[3].Note != 60 {
		t.Errorf("event 3 = %+v, want NoteOff note 60", events[3])
	}
	if events[4].Type != Sustain || !events[4].On {
		t.Errorf("event 4 = %+v, want Sustain On", events[4])
	}
}

func TestLoadParts(t *testing.T) {
	path := buildTestFile(t)

	song, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	if len(song.Parts) != 2 {
		t.Fatalf("got %d parts, want 2: %+v", len(song.Parts), song.Parts)
	}

	want := []Part{
		{Track: 1, Channel: 0, Name: "Piano", NoteCount: 1},
		{Track: 2, Channel: 1, Name: "Track 2", NoteCount: 1},
	}
	for i, p := range want {
		if song.Parts[i] != p {
			t.Errorf("part %d = %+v, want %+v", i, song.Parts[i], p)
		}
	}
}

func TestLoadPartNamePrefersProgramChangeOverTrackName(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.mid")

	s := smf.NewSMF1()
	var meta smf.Track
	meta.Add(0, smf.MetaTempo(120))
	meta.Close(0)
	if err := s.Add(meta); err != nil {
		t.Fatalf("adding meta track: %v", err)
	}

	// A generically/unhelpfully named track ("MIDI out 1", mirroring what
	// real export tools often produce) that also declares its actual GM
	// instrument via Program Change — the instrument name should win.
	var track smf.Track
	track.Add(0, smf.MetaTrackSequenceName("MIDI out 1"))
	track.Add(0, midi.ProgramChange(0, 26)) // 26 = ElectricGuitarJazz
	track.Add(0, midi.NoteOn(0, 60, 100))
	track.Add(480, midi.NoteOff(0, 60))
	track.Close(0)
	if err := s.Add(track); err != nil {
		t.Fatalf("adding track: %v", err)
	}

	if err := s.WriteFile(path); err != nil {
		t.Fatalf("writing test midi file: %v", err)
	}

	song, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(song.Parts) != 1 {
		t.Fatalf("got %d parts, want 1: %+v", len(song.Parts), song.Parts)
	}
	if got, want := song.Parts[0].Name, "Electric Guitar Jazz"; got != want {
		t.Errorf("part name = %q, want %q (Program Change should win over the track name)", got, want)
	}
}

func TestLoadPartNameDrumChannel(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.mid")

	s := smf.NewSMF1()
	var meta smf.Track
	meta.Add(0, smf.MetaTempo(120))
	meta.Close(0)
	if err := s.Add(meta); err != nil {
		t.Fatalf("adding meta track: %v", err)
	}

	var track smf.Track
	track.Add(0, midi.NoteOn(gmDrumChannel, 36, 100)) // channel 10 (0-indexed 9): GM drums
	track.Add(480, midi.NoteOff(gmDrumChannel, 36))
	track.Close(0)
	if err := s.Add(track); err != nil {
		t.Fatalf("adding drum track: %v", err)
	}

	if err := s.WriteFile(path); err != nil {
		t.Fatalf("writing test midi file: %v", err)
	}

	song, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(song.Parts) != 1 {
		t.Fatalf("got %d parts, want 1: %+v", len(song.Parts), song.Parts)
	}
	if got, want := song.Parts[0].Name, "Drums"; got != want {
		t.Errorf("part name = %q, want %q", got, want)
	}
}

func TestHumanizeCamelCase(t *testing.T) {
	cases := map[string]string{
		"AcousticGrandPiano": "Acoustic Grand Piano",
		"ElectricPiano1":     "Electric Piano1", // digit doesn't itself split, but does trigger a split before the next capital
		"Sitar":              "Sitar",
		"":                   "",
	}
	for in, want := range cases {
		if got := humanizeCamelCase(in); got != want {
			t.Errorf("humanizeCamelCase(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestSongFilter(t *testing.T) {
	path := buildTestFile(t)

	song, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	piano := song.Parts[0]
	filtered := song.Filter(piano)

	if len(filtered) != 3 {
		t.Fatalf("got %d events for %+v, want 3 (note-on, note-off, sustain-on): %+v", len(filtered), piano, filtered)
	}
	for _, ev := range filtered {
		if ev.Track != piano.Track || ev.Channel != piano.Channel {
			t.Errorf("filtered event %+v does not belong to part %+v", ev, piano)
		}
	}
	// Filtering must preserve time order.
	for i := 1; i < len(filtered); i++ {
		if filtered[i].Time < filtered[i-1].Time {
			t.Errorf("filtered events out of time order at index %d: %+v", i, filtered)
		}
	}
}

func TestSongFilterPartsUnionsMultiplePartsInTimeOrder(t *testing.T) {
	path := buildTestFile(t)

	song, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(song.Parts) != 2 {
		t.Fatalf("test setup: want 2 parts, got %d", len(song.Parts))
	}

	filtered := song.FilterParts(song.Parts)

	if len(filtered) != len(song.Events) {
		t.Fatalf("FilterParts(all parts) = %d events, want all %d events", len(filtered), len(song.Events))
	}
	for i := 1; i < len(filtered); i++ {
		if filtered[i].Time < filtered[i-1].Time {
			t.Errorf("filtered events out of time order at index %d: %+v", i, filtered)
		}
	}

	// A part not present in the song contributes nothing.
	none := song.FilterParts(nil)
	if len(none) != 0 {
		t.Errorf("FilterParts(nil) = %d events, want 0", len(none))
	}
}

func TestTransposeShiftsNoteEventsOnly(t *testing.T) {
	events := []Event{
		{Type: NoteOn, Note: 60, Time: 0},
		{Type: NoteOff, Note: 60, Time: 0.5},
		{Type: Sustain, On: true, Time: 0.5},
	}

	up := Transpose(events, 12)
	if up[0].Note != 72 || up[1].Note != 72 {
		t.Errorf("Transpose(+12) notes = %d, %d, want 72, 72", up[0].Note, up[1].Note)
	}
	if up[2] != events[2] {
		t.Errorf("Transpose should leave Sustain events untouched, got %+v", up[2])
	}

	down := Transpose(events, -6)
	if down[0].Note != 54 || down[1].Note != 54 {
		t.Errorf("Transpose(-6) notes = %d, %d, want 54, 54", down[0].Note, down[1].Note)
	}
}

func TestTransposeZeroIsNoOp(t *testing.T) {
	events := []Event{{Type: NoteOn, Note: 60}, {Type: Sustain, On: true}}
	got := Transpose(events, 0)
	if len(got) != len(events) || got[0].Note != 60 {
		t.Errorf("Transpose(events, 0) = %+v, want an unchanged copy of %+v", got, events)
	}
}

func TestTransposeDropsNotesOutsideValidMIDIRange(t *testing.T) {
	low := []Event{
		{Type: NoteOn, Note: 3, Time: 0},  // 3 - 12 = -9: invalid, must be dropped
		{Type: NoteOn, Note: 60, Time: 1}, // 60 - 12 = 48: valid, must survive
	}
	down := Transpose(low, -12)
	if len(down) != 1 || down[0].Note != 48 {
		t.Errorf("Transpose(low notes, -12) = %+v, want only the note-48 event to survive", down)
	}

	high := []Event{
		{Type: NoteOn, Note: 120, Time: 0}, // 120 + 12 = 132: invalid, must be dropped
		{Type: NoteOn, Note: 60, Time: 1},  // 60 + 12 = 72: valid, must survive
	}
	up := Transpose(high, 12)
	if len(up) != 1 || up[0].Note != 72 {
		t.Errorf("Transpose(high notes, +12) = %+v, want only the note-72 event to survive", up)
	}
}

func TestLoadMergesOverlappingSamePitchOnsets(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.mid")

	s := smf.NewSMF1()
	var meta smf.Track
	meta.Add(0, smf.MetaTempo(120))
	meta.Close(0)
	if err := s.Add(meta); err != nil {
		t.Fatalf("adding meta track: %v", err)
	}

	// Note 60 struck again (a pedal/legato-overlap artifact) before its first
	// onset's own note-off arrives, then released twice. This should collapse
	// to a single sustained NoteOn (at the first onset) through NoteOff (at
	// the last release) rather than reaching the engine as two distinct
	// onsets needing their own retrigger.
	var track smf.Track
	track.Add(0, midi.NoteOn(0, 60, 100))
	track.Add(120, midi.NoteOn(0, 60, 90)) // re-struck while still sounding
	track.Add(120, midi.NoteOff(0, 60))    // closes the first onset
	track.Add(240, midi.NoteOff(0, 60))    // closes the second onset
	track.Close(0)
	if err := s.Add(track); err != nil {
		t.Fatalf("adding track: %v", err)
	}

	if err := s.WriteFile(path); err != nil {
		t.Fatalf("writing test midi file: %v", err)
	}

	song, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	if len(song.Events) != 2 {
		t.Fatalf("got %d events, want 2 (one merged NoteOn/NoteOff pair): %+v", len(song.Events), song.Events)
	}
	if song.Events[0].Type != NoteOn || song.Events[0].Velocity != 100 || song.Events[0].Time != 0 {
		t.Errorf("event 0 = %+v, want the first onset's NoteOn (vel 100, t=0)", song.Events[0])
	}
	if song.Events[1].Type != NoteOff || song.Events[1].Time <= song.Events[0].Time {
		t.Errorf("event 1 = %+v, want the last release's NoteOff, after the onset", song.Events[1])
	}

	if len(song.Parts) != 1 || song.Parts[0].NoteCount != 1 {
		t.Errorf("parts = %+v, want a single part with NoteCount 1 (the merged onset counts once)", song.Parts)
	}
}

func TestMergeOverlappingNotesHandlesStrayNoteOffAndUnrelatedEvents(t *testing.T) {
	events := []Event{
		{Type: NoteOff, Track: 0, Channel: 0, Note: 60, Time: 0}, // stray: no open onset, must pass through
		{Type: Sustain, On: true, Time: 0.1},
		{Type: NoteOn, Track: 0, Channel: 0, Note: 64, Time: 0.2},
		{Type: NoteOn, Track: 0, Channel: 0, Note: 64, Time: 0.3},  // overlap: same pitch, dropped
		{Type: NoteOn, Track: 0, Channel: 0, Note: 64, Time: 0.35}, // overlap: same pitch, dropped
		{Type: NoteOff, Track: 0, Channel: 0, Note: 64, Time: 0.4}, // closes the 2nd overlap: dropped
		{Type: NoteOff, Track: 0, Channel: 0, Note: 64, Time: 0.5}, // closes the 3rd overlap: dropped
		{Type: NoteOff, Track: 0, Channel: 0, Note: 64, Time: 0.6}, // closes the 1st (original) onset: kept
	}

	got := mergeOverlappingNotes(events)

	want := []Event{
		{Type: NoteOff, Track: 0, Channel: 0, Note: 60, Time: 0},
		{Type: Sustain, On: true, Time: 0.1},
		{Type: NoteOn, Track: 0, Channel: 0, Note: 64, Time: 0.2},
		{Type: NoteOff, Track: 0, Channel: 0, Note: 64, Time: 0.6},
	}
	if len(got) != len(want) {
		t.Fatalf("got %d events, want %d: %+v", len(got), len(want), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("event %d = %+v, want %+v", i, got[i], want[i])
		}
	}
}

func TestMergeOverlappingNotesKeepsDistinctPitchesAndChannelsSeparate(t *testing.T) {
	events := []Event{
		{Type: NoteOn, Track: 0, Channel: 0, Note: 60, Time: 0},
		{Type: NoteOn, Track: 0, Channel: 1, Note: 60, Time: 0.1}, // same pitch, different channel: independent
		{Type: NoteOn, Track: 1, Channel: 0, Note: 60, Time: 0.2}, // same pitch, different track: independent
		{Type: NoteOff, Track: 0, Channel: 0, Note: 60, Time: 0.3},
		{Type: NoteOff, Track: 0, Channel: 1, Note: 60, Time: 0.4},
		{Type: NoteOff, Track: 1, Channel: 0, Note: 60, Time: 0.5},
	}

	got := mergeOverlappingNotes(events)

	if len(got) != len(events) {
		t.Fatalf("got %d events, want %d (nothing should be merged across different tracks/channels): %+v", len(got), len(events), got)
	}
}

func TestLoadMissingFile(t *testing.T) {
	if _, err := Load(filepath.Join(t.TempDir(), "does-not-exist.mid")); err == nil {
		t.Fatal("Load with missing file: want error, got nil")
	}
}
