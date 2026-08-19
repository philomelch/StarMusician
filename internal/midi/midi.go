// Package midi parses Standard MIDI Files into a flat, time-sorted list of
// events with absolute timestamps in seconds. It knows nothing about any
// particular game or keyboard layout.
package midi

import (
	"fmt"
	"sort"
	"unicode"

	"gitlab.com/gomidi/midi/v2/gm"
	"gitlab.com/gomidi/midi/v2/smf"
)

// gmDrumChannel is the MIDI channel (0-indexed) General MIDI reserves for
// percussion; Program Change on this channel selects a drum kit, not a
// melodic instrument, so it's labeled directly rather than looked up in the
// GM instrument table.
const gmDrumChannel = 9

// EventType identifies the kind of a flattened MIDI event.
type EventType int

const (
	NoteOn EventType = iota
	NoteOff
	Sustain
)

// SustainController is the MIDI CC number for the sustain (damper) pedal.
const SustainController = 64

// Event is a single flattened, absolutely-timed MIDI event.
type Event struct {
	Time     float64 // seconds from the start of the file
	Type     EventType
	Track    int
	Channel  uint8
	Note     uint8 // valid for NoteOn / NoteOff
	Velocity uint8 // valid for NoteOn
	On       bool  // valid for Sustain: true if the pedal is depressed (CC value >= 64)
}

// Part identifies one instrument part within a Song: everything on a given
// track+channel. Most multi-instrument files put one instrument per track
// (often named via a MetaTrackName meta event), but some put multiple
// instruments on different channels of the same track, so a part is keyed
// on the (Track, Channel) pair rather than on either alone.
type Part struct {
	Track     int
	Channel   uint8
	Name      string // see nameForPart: GM instrument name, else MetaTrackName, else "Track N"
	NoteCount int
}

// partKey identifies a part before its Name is known (Name requires having
// already scanned every event in the file, so it can't be part of the map
// key used while collecting per-part data during the parse).
type partKey struct {
	track   int
	channel uint8
}

// Song is a fully parsed MIDI file: every event flattened and time-sorted
// across all tracks, plus the distinct instrument Parts found within it so a
// caller can choose which one to actually play (only one instrument can
// sound through the game's physical keyboard at a time).
type Song struct {
	Events []Event
	Parts  []Part // sorted by NoteCount descending
}

// Filter returns the subset of s.Events belonging to p, preserving time
// order.
func (s *Song) Filter(p Part) []Event {
	return s.FilterParts([]Part{p})
}

// FilterParts returns the subset of s.Events belonging to any of parts,
// preserving time order — e.g. playing two instrument parts together
// through the game's single physical keyboard.
func (s *Song) FilterParts(parts []Part) []Event {
	keys := make(map[partKey]bool, len(parts))
	for _, p := range parts {
		keys[partKey{track: p.Track, channel: p.Channel}] = true
	}

	var out []Event
	for _, ev := range s.Events {
		if keys[partKey{track: ev.Track, channel: ev.Channel}] {
			out = append(out, ev)
		}
	}
	return out
}

// Transpose returns a copy of events with every NoteOn/NoteOff event's Note
// shifted by semitones (positive = up, negative = down); Sustain events pass
// through unchanged since they carry no pitch. A note that would land
// outside the valid MIDI range [0,127] is dropped rather than wrapping or
// producing a wrong pitch — the caller's key-mapping profile will typically
// drop it anyway (BPSR's instrument only reaches a 60-semitone window to
// begin with), but Transpose doesn't rely on that; it's correct on its own.
func Transpose(events []Event, semitones int) []Event {
	out := make([]Event, 0, len(events))
	for _, ev := range events {
		if ev.Type == Sustain || semitones == 0 {
			out = append(out, ev)
			continue
		}
		n := int(ev.Note) + semitones
		if n < 0 || n > 127 {
			continue
		}
		ev.Note = uint8(n)
		out = append(out, ev)
	}
	return out
}

// Load reads a .mid file and returns its note-on, note-off, and sustain
// events flattened across all tracks and sorted by absolute time, along with
// the instrument parts found within it.
// Overlapping onsets of the same pitch
// (see mergeOverlappingNotes) are collapsed before Parts' note counts are
// tallied, so NoteCount reflects what will actually be dispatched.
func Load(path string) (*Song, error) {
	reader := smf.ReadTracks(path)
	if err := reader.Error(); err != nil {
		return nil, fmt.Errorf("reading midi file %q: %w", path, err)
	}

	var events []Event
	trackNames := make(map[int]string)
	programs := make(map[partKey]uint8)

	reader.Do(func(te smf.TrackEvent) {
		msg := te.Message
		t := microToSec(te.AbsMicroSeconds)

		var name string
		if msg.GetMetaTrackName(&name) {
			trackNames[te.TrackNo] = name
			return
		}

		var channel, program uint8
		if msg.GetProgramChange(&channel, &program) {
			programs[partKey{track: te.TrackNo, channel: channel}] = program
			return
		}

		var note, velocity uint8
		if msg.GetNoteStart(&channel, &note, &velocity) {
			events = append(events, Event{Time: t, Type: NoteOn, Track: te.TrackNo, Channel: channel, Note: note, Velocity: velocity})
			return
		}
		if msg.GetNoteEnd(&channel, &note) {
			events = append(events, Event{Time: t, Type: NoteOff, Track: te.TrackNo, Channel: channel, Note: note})
			return
		}

		var controller, value uint8
		if msg.GetControlChange(&channel, &controller, &value) && controller == SustainController {
			events = append(events, Event{Time: t, Type: Sustain, Track: te.TrackNo, Channel: channel, On: value >= 64})
		}
	})

	if err := reader.Error(); err != nil {
		return nil, fmt.Errorf("reading midi file %q: %w", path, err)
	}

	sort.SliceStable(events, func(i, j int) bool { return events[i].Time < events[j].Time })
	events = mergeOverlappingNotes(events)

	noteCounts := make(map[partKey]int)
	for _, ev := range events {
		if ev.Type == NoteOn {
			noteCounts[partKey{track: ev.Track, channel: ev.Channel}]++
		}
	}

	parts := make([]Part, 0, len(noteCounts))
	for pk, count := range noteCounts {
		program, hasProgram := programs[pk]
		name := nameForPart(pk, program, hasProgram, trackNames[pk.track])
		parts = append(parts, Part{Track: pk.track, Channel: pk.channel, Name: name, NoteCount: count})
	}
	sort.Slice(parts, func(i, j int) bool {
		if parts[i].NoteCount != parts[j].NoteCount {
			return parts[i].NoteCount > parts[j].NoteCount
		}
		if parts[i].Track != parts[j].Track {
			return parts[i].Track < parts[j].Track
		}
		return parts[i].Channel < parts[j].Channel
	})

	return &Song{Events: events, Parts: parts}, nil
}

// nameForPart picks the most descriptive label available for a part.
// Program Change (which selects the actual General MIDI sound a channel
// plays) takes priority over the file's own track-name metadata: track
// names are frequently unhelpful export artifacts (e.g. "MIDI out 1" naming
// the output port rather than the instrument), whereas Program Change says
// what the part actually sounds like.
func nameForPart(pk partKey, program uint8, hasProgram bool, trackName string) string {
	if pk.channel == gmDrumChannel {
		return "Drums"
	}
	if hasProgram {
		return humanizeCamelCase(gm.Instr(program).String())
	}
	if trackName != "" {
		return trackName
	}
	return fmt.Sprintf("Track %d", pk.track)
}

// humanizeCamelCase turns the gm package's CamelCase instrument names (e.g.
// "AcousticGrandPiano") into spaced, readable text ("Acoustic Grand Piano").
func humanizeCamelCase(s string) string {
	var b []rune
	runes := []rune(s)
	for i, r := range runes {
		if i > 0 && unicode.IsUpper(r) {
			prev := runes[i-1]
			if unicode.IsLower(prev) || unicode.IsDigit(prev) {
				b = append(b, ' ')
			}
		}
		b = append(b, r)
	}
	return string(b)
}

func microToSec(micro int64) float64 {
	return float64(micro) / 1_000_000
}

// noteKey identifies one sounding pitch instance for mergeOverlappingNotes:
// the same (Track, Channel, Note) triple the engine later uses to track held
// notes.
type noteKey struct {
	track   int
	channel uint8
	note    uint8
}

// mergeOverlappingNotes collapses overlapping onsets of the same pitch — a
// NoteOn for a (track, channel, note) that arrives while an earlier onset of
// that exact same pitch is still sounding — into a single sustained
// NoteOn/NoteOff pair spanning the earliest onset to the last release,
// dropping the redundant NoteOn/NoteOff pairs in between.
func mergeOverlappingNotes(events []Event) []Event {
	depth := make(map[noteKey]int)
	out := make([]Event, 0, len(events))
	for _, ev := range events {
		if ev.Type != NoteOn && ev.Type != NoteOff {
			out = append(out, ev)
			continue
		}
		nk := noteKey{track: ev.Track, channel: ev.Channel, note: ev.Note}
		switch ev.Type {
		case NoteOn:
			if depth[nk] == 0 {
				out = append(out, ev)
			}
			depth[nk]++
		case NoteOff:
			if depth[nk] == 0 {
				out = append(out, ev) // stray: no open onset to close
				continue
			}
			depth[nk]--
			if depth[nk] == 0 {
				out = append(out, ev)
			}
		}
	}
	return out
}
