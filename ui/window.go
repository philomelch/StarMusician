// Package ui is the Fyne GUI: a thin layer that wires widgets to the
// already-built, headless engine.Player. It contains no playback logic of
// its own — parsing, key mapping, injection, scheduling, and the panic-stop
// hotkey all already exist in internal/*; this package only presents them.
package ui

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strconv"
	"strings"
	"sync"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/storage"
	"fyne.io/fyne/v2/widget"

	"github.com/philomelch/StarMusician/internal/engine"
	"github.com/philomelch/StarMusician/internal/hotkey"
	"github.com/philomelch/StarMusician/internal/input"
	"github.com/philomelch/StarMusician/internal/keymap"
	"github.com/philomelch/StarMusician/internal/library"
	"github.com/philomelch/StarMusician/internal/midi"
)

// defaultCountdown mirrors the CLI's default and is used whenever the
// countdown entry can't be parsed as a non-negative integer.
const defaultCountdown = 3

// stopKeyNames are the choices offered in the stop-hotkey dropdown, in
// display order. Esc is included but not the default — see
// internal/hotkey.DefaultKey for why F9 is preferred.
var stopKeyNames = []string{"Esc", "F8", "F9", "F10"}

// transposeOptions are the choices offered in the transpose dropdown, in
// display order.
var transposeOptions = []struct {
	label     string
	semitones int
}{
	{"None", 0},
	{"1 octave down (-12)", -12},
	{"Half octave down (-6)", -6},
	{"Half octave up (+6)", 6},
	{"1 octave up (+12)", 12},
}

func transposeLabels() []string {
	labels := make([]string, len(transposeOptions))
	for i, o := range transposeOptions {
		labels[i] = o.label
	}
	return labels
}

func semitonesForTransposeLabel(label string) int {
	for _, o := range transposeOptions {
		if o.label == label {
			return o.semitones
		}
	}
	return 0
}

// appWindow holds every widget and all mutable state for the single main
// window. Widget callbacks all run on Fyne's single callback goroutine
// (Fyne 2.6+), so fields are only ever touched from there — except player,
// guarded by mu, which is also read/written from the Play goroutine and the
// hotkey listener's own goroutine.
type appWindow struct {
	win fyne.Window

	fileSelect      *widget.Select
	refreshBtn      *widget.Button
	browseBtn       *widget.Button
	partsBox        *fyne.Container // holds one widget.Check per song.Parts entry
	windowEntry     *widget.Entry
	countdownEntry  *widget.Entry
	stopKeySelect   *widget.Select
	transposeSelect *widget.Select
	playBtn         *widget.Button
	stopBtn         *widget.Button
	progress        *widget.ProgressBar
	status          *widget.Label

	filePaths []string // parallel to fileSelect.Options
	song      *midi.Song
	selected  []bool // parallel to song.Parts / partsBox.Objects: which parts are toggled on

	mu     sync.Mutex
	player *engine.Player // non-nil exactly while a Play() call is in flight

	hk *hotkey.Listener
}

// Build constructs the main window's content and wires every widget to the
// engine, then returns the controller (returned mainly so cmd/starmusician-gui
// and tests can hold a reference; most callers can ignore it).
func Build(win fyne.Window) *appWindow {
	w := &appWindow{win: win}

	w.fileSelect = widget.NewSelect(nil, w.onFileChanged)
	w.fileSelect.PlaceHolder = "Choose a .mid file..."

	w.refreshBtn = widget.NewButton("Refresh", w.refreshFiles)
	w.browseBtn = widget.NewButton("Browse...", w.browseFile)

	w.partsBox = container.NewVBox()
	partsScroll := container.NewVScroll(w.partsBox)
	partsScroll.SetMinSize(fyne.NewSize(0, 160))

	w.windowEntry = widget.NewEntry()
	w.windowEntry.SetText("Blue Protocol")

	w.countdownEntry = widget.NewEntry()
	w.countdownEntry.SetText(strconv.Itoa(defaultCountdown))

	w.stopKeySelect = widget.NewSelect(stopKeyNames, w.onStopKeyChanged)
	// Set the field directly rather than SetSelected: SetSelected would fire
	// onStopKeyChanged (and thus startHotkey) here, before w.status exists
	// yet, and would also register the hotkey a second time redundantly
	// alongside the explicit startHotkey call below.
	w.stopKeySelect.Selected = "F9"

	w.transposeSelect = widget.NewSelect(transposeLabels(), nil)
	w.transposeSelect.Selected = "None"

	w.playBtn = widget.NewButton("Play", w.onPlay)
	w.playBtn.Disable()
	w.stopBtn = widget.NewButton("Stop", w.onStop)
	w.stopBtn.Disable()

	w.progress = widget.NewProgressBar()
	w.status = widget.NewLabel("Load a .mid file to begin.")

	settings := widget.NewForm(
		widget.NewFormItem("Game window title", w.windowEntry),
		widget.NewFormItem("Countdown (s)", w.countdownEntry),
		widget.NewFormItem("Stop hotkey", w.stopKeySelect),
		widget.NewFormItem("Transpose", w.transposeSelect),
	)

	content := container.NewVBox(
		container.NewBorder(nil, nil, nil, container.NewHBox(w.refreshBtn, w.browseBtn), w.fileSelect),
		widget.NewLabel("Instrument parts (check the ones to play together):"),
		partsScroll,
		settings,
		container.NewHBox(w.playBtn, w.stopBtn),
		w.progress,
		w.status,
	)
	win.SetContent(content)
	win.Resize(fyne.NewSize(480, 420))

	w.refreshFiles()
	w.startHotkey(hotkey.DefaultKey)

	win.SetCloseIntercept(func() {
		w.stopActive()
		if w.hk != nil {
			if err := w.hk.Close(); err != nil {
				// Nowhere left to show this to the user (the window is
				// closing right now) — at least don't discard it silently.
				fmt.Println("warning: could not fully release the stop hotkey:", err)
			}
		}
		win.Close()
	})

	return w
}

// refreshFiles re-scans the portable app's ./midi directory.
func (w *appWindow) refreshFiles() {
	dir, err := library.ExecutableRelativeDir("midi")
	if err != nil {
		w.status.SetText("error: " + err.Error())
		return
	}
	files, err := library.ListMIDIFiles(dir)
	if err != nil {
		w.status.SetText("error: " + err.Error())
		return
	}

	w.filePaths = files
	labels := make([]string, len(files))
	for i, f := range files {
		labels[i] = filepath.Base(f)
	}
	w.fileSelect.SetOptions(labels)

	if len(files) == 0 {
		w.status.SetText(fmt.Sprintf("No .mid files found in %s — use Browse... to pick one.", dir))
	}
}

// browseFile opens a native file dialog for picking a .mid file outside the
// portable app's own ./midi directory.
func (w *appWindow) browseFile() {
	d := dialog.NewFileOpen(func(reader fyne.URIReadCloser, err error) {
		if err != nil || reader == nil {
			return
		}
		defer reader.Close()
		w.loadFile(reader.URI().Path())
	}, w.win)
	d.SetFilter(storage.NewExtensionFileFilter([]string{".mid"}))
	d.Show()
}

// onFileChanged handles a selection from the ./midi dropdown.
func (w *appWindow) onFileChanged(label string) {
	for i, l := range w.fileSelect.Options {
		if l == label {
			w.loadFile(w.filePaths[i])
			return
		}
	}
}

// loadFile parses path and populates the instrument-part picker, auto
// selecting the part with the most notes.
func (w *appWindow) loadFile(path string) {
	song, err := midi.Load(path)
	if err != nil {
		w.status.SetText("error: " + err.Error())
		w.song = nil
		w.setParts(nil)
		return
	}
	w.song = song
	w.setParts(song.Parts)

	if len(song.Parts) == 0 {
		w.status.SetText(fmt.Sprintf("%s has no playable notes.", filepath.Base(path)))
		return
	}
	w.status.SetText(fmt.Sprintf("Loaded %s.", filepath.Base(path)))
}

// setParts rebuilds the instrument-parts checklist for the given parts, one
// widget.Check per part, auto-checking the part with the most notes so
// there's always a sensible default to play immediately after loading a
// file. Any number of parts can be checked at once — they're all played
// together through the game's single physical keyboard.
func (w *appWindow) setParts(parts []midi.Part) {
	w.selected = make([]bool, len(parts))

	objects := make([]fyne.CanvasObject, len(parts))
	for i, p := range parts {
		label := fmt.Sprintf("%s (%d notes)", p.Name, p.NoteCount)
		chk := widget.NewCheck(label, func(checked bool) {
			w.selected[i] = checked
			w.updatePlayEnabled()
		})
		if i == 0 {
			// Set the field directly, not via SetChecked: SetChecked would
			// fire OnChanged synchronously, which is redundant here since
			// w.selected[0] is set directly below, and — as with the
			// stop-hotkey Select during Build — risks running before
			// everything it touches exists.
			chk.Checked = true
			w.selected[0] = true
		}
		objects[i] = chk
	}
	w.partsBox.Objects = objects
	w.partsBox.Refresh()

	w.updatePlayEnabled()
}

// anySelected reports whether at least one instrument part is checked.
func (w *appWindow) anySelected() bool {
	for _, s := range w.selected {
		if s {
			return true
		}
	}
	return false
}

// updatePlayEnabled reflects whether we're playing (mutex-guarded, may be
// set from another goroutine) into every widget's enabled state.
func (w *appWindow) updatePlayEnabled() {
	playing := w.isPlaying()

	if !playing && w.song != nil && w.anySelected() {
		w.playBtn.Enable()
	} else {
		w.playBtn.Disable()
	}

	setPartsEnabled := func(enabled bool) {
		for _, obj := range w.partsBox.Objects {
			chk := obj.(*widget.Check)
			if enabled {
				chk.Enable()
			} else {
				chk.Disable()
			}
		}
	}

	if playing {
		w.stopBtn.Enable()
		w.fileSelect.Disable()
		w.refreshBtn.Disable()
		w.browseBtn.Disable()
		// Not disabling the settings that don't affect the ability to stop
		// (window title, countdown, transpose) — only fileSelect/parts
		// (would swap out the song mid-play from under the running Play()
		// goroutine) and stopKeySelect (switching it mid-play risks a brief
		// window with no hotkey registered at all if the new key fails to
		// register; see onStopKeyChanged).
		w.stopKeySelect.Disable()
		setPartsEnabled(false)
	} else {
		w.stopBtn.Disable()
		w.fileSelect.Enable()
		w.refreshBtn.Enable()
		w.browseBtn.Enable()
		w.stopKeySelect.Enable()
		setPartsEnabled(true)
	}
}

func (w *appWindow) isPlaying() bool {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.player != nil
}

func (w *appWindow) onPlay() {
	if w.song == nil || !w.anySelected() {
		return
	}

	var chosen []midi.Part
	for i, sel := range w.selected {
		if sel {
			chosen = append(chosen, w.song.Parts[i])
		}
	}
	events := w.song.FilterParts(chosen)
	if len(events) == 0 {
		w.status.SetText("Selected part(s) have no events.")
		return
	}

	if semitones := semitonesForTransposeLabel(w.transposeSelect.Selected); semitones != 0 {
		events = midi.Transpose(events, semitones)
		if len(events) == 0 {
			w.status.SetText("Transpose leaves no playable notes.")
			return
		}
	}

	countdown := defaultCountdown
	if n, err := strconv.Atoi(strings.TrimSpace(w.countdownEntry.Text)); err == nil && n >= 0 {
		countdown = n
	}
	windowSubstr := w.windowEntry.Text

	player := engine.New(
		keymap.BPSR(),
		input.New(),
		engine.WithForegroundChecker(engine.NewWindowTitleChecker(windowSubstr)),
		engine.WithCountdown(countdown),
		engine.WithCountdownCallback(func(secondsLeft int) {
			fyne.Do(func() { w.status.SetText(fmt.Sprintf("Starting in %d...", secondsLeft)) })
		}),
		engine.WithProgress(func(p engine.Progress) {
			fyne.Do(func() {
				if p.Total > 0 {
					w.progress.SetValue(p.Time / p.Total)
				}
				w.status.SetText(fmt.Sprintf("Playing... %.1fs / %.1fs", p.Time, p.Total))
			})
		}),
	)

	w.mu.Lock()
	w.player = player
	w.mu.Unlock()
	w.updatePlayEnabled()

	go func() {
		err := player.Play(context.Background(), events)

		w.mu.Lock()
		w.player = nil
		w.mu.Unlock()

		fyne.Do(func() {
			w.updatePlayEnabled()
			w.progress.SetValue(0)
			switch {
			case err == nil:
				w.status.SetText("Done.")
			case errors.Is(err, context.Canceled):
				w.status.SetText("Stopped.")
			case errors.Is(err, engine.ErrForegroundMismatch):
				w.status.SetText(fmt.Sprintf("Game window not focused (looking for a title containing %q).", windowSubstr))
			default:
				w.status.SetText("error: " + err.Error())
			}
		})
	}()
}

func (w *appWindow) onStop() {
	w.stopActive()
}

// stopActive is nil-safe and callable from any goroutine: the Stop button,
// the panic-stop hotkey (its own goroutine), and window close.
func (w *appWindow) stopActive() {
	w.mu.Lock()
	p := w.player
	w.mu.Unlock()
	if p != nil {
		p.Stop()
	}
}

func (w *appWindow) onStopKeyChanged(name string) {
	key, ok := parseStopKeyName(name)
	if !ok {
		return
	}
	// Register the new key BEFORE closing the old one: if registration
	// fails (e.g. the key is already claimed system-wide by another app),
	// the previous hotkey stays active instead of leaving zero panic-stop
	// hotkey registered while a change is in progress.
	l, err := hotkey.Start(key, w.hotkeyTriggered)
	if err != nil {
		w.status.SetText("warning: could not register new stop hotkey, keeping the previous one: " + err.Error())
		return
	}
	old := w.hk
	w.hk = l
	if old != nil {
		if err := old.Close(); err != nil {
			w.status.SetText("warning: could not fully release the previous stop hotkey: " + err.Error())
		}
	}
}

// hotkeyTriggered is the callback for every hotkey.Listener this window
// registers (initial and any later swap via onStopKeyChanged).
func (w *appWindow) hotkeyTriggered() {
	w.stopActive()
	fyne.Do(func() { w.status.SetText("Stopped (hotkey).") })
}

func (w *appWindow) startHotkey(key hotkey.Key) {
	l, err := hotkey.Start(key, w.hotkeyTriggered)
	if err != nil {
		w.hk = nil
		w.status.SetText("warning: could not register stop hotkey: " + err.Error())
		return
	}
	w.hk = l
}

func parseStopKeyName(name string) (hotkey.Key, bool) {
	switch strings.ToLower(name) {
	case "esc":
		return hotkey.KeyEscape, true
	case "f8":
		return hotkey.KeyF8, true
	case "f9":
		return hotkey.KeyF9, true
	case "f10":
		return hotkey.KeyF10, true
	default:
		return 0, false
	}
}
