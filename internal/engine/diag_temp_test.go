package engine

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/philomelch/StarMusician/internal/keymap"
	"github.com/philomelch/StarMusician/internal/midi"
)

// recordingInjector logs every press/release with a running held-count per
// key so we can spot double-presses (retrigger without release) or keys
// still held at the very end.
type recTestInjector struct {
	held map[keymap.Key]int
	log  []string
}

func (r *recTestInjector) Press(vk uint16, ext bool) error {
	k := keymap.Key{VK: vk, Extended: ext}
	r.held[k]++
	r.log = append(r.log, fmt.Sprintf("PRESS vk=%#x held=%d", vk, r.held[k]))
	return nil
}
func (r *recTestInjector) Release(vk uint16, ext bool) error {
	k := keymap.Key{VK: vk, Extended: ext}
	if r.held[k] <= 0 {
		r.log = append(r.log, fmt.Sprintf("RELEASE vk=%#x (WAS NOT HELD)", vk))
		return nil
	}
	r.held[k]--
	r.log = append(r.log, fmt.Sprintf("RELEASE vk=%#x held=%d", vk, r.held[k]))
	return nil
}
func (r *recTestInjector) ReleaseAll() error {
	r.held = make(map[keymap.Key]int)
	return nil
}

func TestDiagOneFileDetail(t *testing.T) {
	path := `D:\projects\go\StarMusician\midi\夜に駆ける - YOASOBI (Piano Cover).mid.mid`
	song, err := midi.Load(path)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	part := song.Parts[0]
	events := song.Filter(part)
	t.Logf("part: %+v, events: %d", part, len(events))

	rec := &recTestInjector{held: make(map[keymap.Key]int)}
	p := &Player{
		profile:     keymap.BPSR(),
		inj:         rec,
		active: make(map[noteKey][]keymap.Resolution),
	}

	target := noteKey{track: 0, channel: 0, note: 52}
	for i, ev := range events {
		nk := noteKey{track: ev.Track, channel: ev.Channel, note: ev.Note}
		if nk == target && (ev.Type == midi.NoteOn || ev.Type == midi.NoteOff) {
			t.Logf("event[%d] t=%.3f BEFORE: type=%v currentShift=%v active=%d",
				i, ev.Time, ev.Type, p.currentShift, len(p.active[nk]))
		}
		if err := p.dispatch(ev); err != nil {
			t.Fatalf("dispatch: %v", err)
		}
		if nk == target && (ev.Type == midi.NoteOn || ev.Type == midi.NoteOff) {
			t.Logf("event[%d] t=%.3f AFTER:  active=%d", i, ev.Time, len(p.active[nk]))
		}
	}

	t.Logf("engine active leftover:")
	for nk, stack := range p.active {
		t.Logf("  %+v stackLen=%d", nk, len(stack))
	}
}

func TestDiagAllFiles(t *testing.T) {
	dir := `D:\projects\go\StarMusician\midi`
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("readdir: %v", err)
	}
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		path := filepath.Join(dir, e.Name())
		song, err := midi.Load(path)
		if err != nil {
			t.Logf("%s: load error: %v", e.Name(), err)
			continue
		}
		for _, part := range song.Parts {
			events := song.Filter(part)

			rec := &recTestInjector{held: make(map[keymap.Key]int)}
			p := &Player{
				profile: keymap.BPSR(),
				inj:     rec,
				active:  make(map[noteKey][]keymap.Resolution),
			}

			toggles := 0
			cutNotes := 0
			maxStack := 0
			for _, ev := range events {
				if ev.Type == midi.NoteOn {
					if res, ok := p.profile.Resolve(ev.Note, p.currentShift); ok && res.Shift != p.currentShift {
						toggles++
						cutNotes += len(p.active)
					}
				}
				if err := p.dispatch(ev); err != nil {
					t.Fatalf("%s part %+v: dispatch: %v", e.Name(), part, err)
				}
				for _, stack := range p.active {
					if len(stack) > maxStack {
						maxStack = len(stack)
					}
				}
			}

			leftoverActive := len(p.active)
			leftoverHeld := 0
			for _, c := range rec.held {
				if c > 0 {
					leftoverHeld += c
				}
			}

			if leftoverActive > 0 || leftoverHeld > 0 || maxStack > 2 {
				t.Logf("%s part=%q(track=%d,ch=%d,notes=%d): events=%d toggles=%d cut=%d maxStack=%d leftoverActive=%d leftoverHeld=%d",
					e.Name(), part.Name, part.Track, part.Channel, part.NoteCount, len(events), toggles, cutNotes, maxStack, leftoverActive, leftoverHeld)
			}
		}
	}
}
