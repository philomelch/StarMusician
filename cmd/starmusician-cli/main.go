// Command starmusician-cli is the CLI entry point for StarMusician: it
// loads a .mid file and plays it into the focused BPSR window. It exists to
// verify the engine mechanics before the Fyne GUI is built on top of the
// same engine.Player.
package main

import (
	"bufio"
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/philomelch/StarMusician/internal/engine"
	"github.com/philomelch/StarMusician/internal/hotkey"
	"github.com/philomelch/StarMusician/internal/input"
	"github.com/philomelch/StarMusician/internal/keymap"
	"github.com/philomelch/StarMusician/internal/library"
	"github.com/philomelch/StarMusician/internal/midi"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

func run() error {
	var (
		windowSubstr = flag.String("window", "Blue Protocol", "case-insensitive substring the game window's title must contain (verify against the actual in-game title)")
		countdown    = flag.Int("countdown", 3, "seconds to wait (giving you time to focus the game) before playback starts")
		stopKeyName  = flag.String("stopkey", "f9", "global panic-stop hotkey: esc, f8, f9, or f10")
		midiDir      = flag.String("midi-dir", "midi", "directory (relative to this executable) to look for .mid files in when no file is given")
		trackFlag    = flag.Int("track", 0, "1-based instrument part to play (see the printed list); 0 = auto-pick when there's only one, otherwise prompt")
		transpose    = flag.Int("transpose", 0, "semitones to shift every note by (+12/-12 = one octave, +6/-6 = half an octave)")
	)
	flag.Parse()

	stopKey, err := parseStopKey(*stopKeyName)
	if err != nil {
		return err
	}

	path, err := resolveMidiPath(flag.Args(), *midiDir)
	if err != nil {
		return err
	}

	fmt.Printf("Loading %s...\n", path)
	song, err := midi.Load(path)
	if err != nil {
		return fmt.Errorf("loading %s: %w", path, err)
	}
	if len(song.Events) == 0 {
		return fmt.Errorf("%s has no playable events", path)
	}

	part, err := selectPart(song.Parts, *trackFlag)
	if err != nil {
		return err
	}
	fmt.Printf("Playing part %q (%d notes).\n", part.Name, part.NoteCount)

	events := song.Filter(part)
	if len(events) == 0 {
		return fmt.Errorf("part %q has no events", part.Name)
	}
	if *transpose != 0 {
		events = midi.Transpose(events, *transpose)
		if len(events) == 0 {
			return fmt.Errorf("transposing by %+d semitones leaves no playable notes", *transpose)
		}
	}
	fmt.Printf("Loaded %d events, %.1fs.\n", len(events), events[len(events)-1].Time)

	player := engine.New(
		keymap.BPSR(),
		input.New(),
		engine.WithForegroundChecker(engine.NewWindowTitleChecker(*windowSubstr)),
		engine.WithCountdown(*countdown),
		engine.WithCountdownCallback(func(secondsLeft int) {
			fmt.Printf("Starting in %d...\n", secondsLeft)
		}),
	)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	listener, err := hotkey.Start(stopKey, func() {
		fmt.Printf("\n[%s] stop hotkey pressed, stopping.\n", *stopKeyName)
		player.Stop()
	})
	if err != nil {
		return fmt.Errorf("registering stop hotkey: %w", err)
	}
	defer listener.Close()

	// Best-effort: also release everything and stop cleanly on Ctrl+C.
	sig := make(chan os.Signal, 1)
	signal.Notify(sig, os.Interrupt)
	go func() {
		if _, ok := <-sig; ok {
			player.Stop()
			cancel()
		}
	}()
	defer signal.Stop(sig)

	fmt.Printf("Press %s at any time to stop playback.\n", strings.ToUpper(*stopKeyName))

	err = player.Play(ctx, events)
	switch {
	case err == nil:
		fmt.Println("Done.")
		return nil
	case errors.Is(err, context.Canceled):
		fmt.Println("Stopped.")
		return nil
	case errors.Is(err, engine.ErrForegroundMismatch):
		return fmt.Errorf("game window not focused (looking for a title containing %q) — focus BPSR and try again", *windowSubstr)
	default:
		return err
	}
}

func parseStopKey(name string) (hotkey.Key, error) {
	switch strings.ToLower(name) {
	case "esc", "escape":
		return hotkey.KeyEscape, nil
	case "f8":
		return hotkey.KeyF8, nil
	case "f9":
		return hotkey.KeyF9, nil
	case "f10":
		return hotkey.KeyF10, nil
	default:
		return 0, fmt.Errorf("unknown -stopkey %q (want esc, f8, f9, or f10)", name)
	}
}

// resolveMidiPath returns the .mid file to play: the single positional
// argument if given, otherwise a file picked from midiDir (relative to the
// running executable, so the portable build finds ./midi regardless of the
// caller's working directory).
func resolveMidiPath(args []string, midiDir string) (string, error) {
	if len(args) > 1 {
		return "", fmt.Errorf("expected at most one .mid file argument, got %d", len(args))
	}
	if len(args) == 1 {
		return args[0], nil
	}

	dir, err := library.ExecutableRelativeDir(midiDir)
	if err != nil {
		return "", err
	}

	files, err := library.ListMIDIFiles(dir)
	if err != nil {
		return "", err
	}
	if len(files) == 0 {
		return "", fmt.Errorf("no .mid files found in %s; pass a file path instead", dir)
	}
	if len(files) == 1 {
		return files[0], nil
	}

	labels := make([]string, len(files))
	for i, f := range files {
		labels[i] = filepath.Base(f)
	}
	i, err := promptForChoice("Multiple .mid files found:", labels)
	if err != nil {
		return "", err
	}
	return files[i], nil
}

// selectPart picks which instrument part to play: trackFlag (1-based) if
// given, the sole part if there's only one, or an interactive prompt
// otherwise.
func selectPart(parts []midi.Part, trackFlag int) (midi.Part, error) {
	if len(parts) == 0 {
		return midi.Part{}, errors.New("no instrument parts found in this file")
	}
	if trackFlag > 0 {
		if trackFlag > len(parts) {
			return midi.Part{}, fmt.Errorf("-track %d is out of range (this file has %d parts)", trackFlag, len(parts))
		}
		return parts[trackFlag-1], nil
	}
	if len(parts) == 1 {
		return parts[0], nil
	}

	labels := make([]string, len(parts))
	for i, p := range parts {
		labels[i] = fmt.Sprintf("%s (%d notes)", p.Name, p.NoteCount)
	}
	i, err := promptForChoice("Multiple instrument parts found:", labels)
	if err != nil {
		return midi.Part{}, err
	}
	return parts[i], nil
}

// promptForChoice prints label followed by a numbered list of options and
// reads a 1-based selection from stdin, returning its 0-based index.
func promptForChoice(label string, options []string) (int, error) {
	fmt.Println(label)
	for i, o := range options {
		fmt.Printf("  %d) %s\n", i+1, o)
	}
	fmt.Print("Choose a number: ")

	scanner := bufio.NewScanner(os.Stdin)
	if !scanner.Scan() {
		return 0, fmt.Errorf("no selection made")
	}
	n, err := strconv.Atoi(strings.TrimSpace(scanner.Text()))
	if err != nil || n < 1 || n > len(options) {
		return 0, fmt.Errorf("invalid selection %q", scanner.Text())
	}
	return n - 1, nil
}
