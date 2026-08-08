package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/philomelch/StarMusician/internal/hotkey"
	"github.com/philomelch/StarMusician/internal/midi"
)

func TestParseStopKey(t *testing.T) {
	cases := map[string]hotkey.Key{
		"esc": hotkey.KeyEscape, "Escape": hotkey.KeyEscape, "ESC": hotkey.KeyEscape,
		"f8": hotkey.KeyF8, "F9": hotkey.KeyF9, "f10": hotkey.KeyF10,
	}
	for input, want := range cases {
		got, err := parseStopKey(input)
		if err != nil {
			t.Errorf("parseStopKey(%q): %v", input, err)
			continue
		}
		if got != want {
			t.Errorf("parseStopKey(%q) = %v, want %v", input, got, want)
		}
	}
}

func TestParseStopKeyRejectsUnknown(t *testing.T) {
	if _, err := parseStopKey("alt"); err == nil {
		t.Fatal("parseStopKey(\"alt\"): want error (Alt must never be registerable), got nil")
	}
}

func TestResolveMidiPathExplicitArgTakesPrecedence(t *testing.T) {
	got, err := resolveMidiPath([]string{"song.mid"}, t.TempDir())
	if err != nil {
		t.Fatalf("resolveMidiPath: %v", err)
	}
	if got != "song.mid" {
		t.Errorf("got %q, want %q", got, "song.mid")
	}
}

func TestResolveMidiPathTooManyArgs(t *testing.T) {
	if _, err := resolveMidiPath([]string{"a.mid", "b.mid"}, t.TempDir()); err == nil {
		t.Fatal("resolveMidiPath with two args: want error, got nil")
	}
}

func TestResolveMidiPathAutoSelectsSoleFile(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "only.mid"))

	got, err := resolveMidiPath(nil, dir)
	if err != nil {
		t.Fatalf("resolveMidiPath: %v", err)
	}
	if got != filepath.Join(dir, "only.mid") {
		t.Errorf("got %q, want %q", got, filepath.Join(dir, "only.mid"))
	}
}

func TestResolveMidiPathNoFilesFound(t *testing.T) {
	if _, err := resolveMidiPath(nil, t.TempDir()); err == nil {
		t.Fatal("resolveMidiPath with an empty midi dir: want error, got nil")
	}
}

func TestSelectPartNoPartsIsError(t *testing.T) {
	if _, err := selectPart(nil, 0); err == nil {
		t.Fatal("selectPart with no parts: want error, got nil")
	}
}

func TestSelectPartAutoSelectsSolePart(t *testing.T) {
	only := midi.Part{Track: 1, Channel: 0, Name: "Piano", NoteCount: 10}
	got, err := selectPart([]midi.Part{only}, 0)
	if err != nil {
		t.Fatalf("selectPart: %v", err)
	}
	if got != only {
		t.Errorf("got %+v, want %+v", got, only)
	}
}

func TestSelectPartExplicitFlagTakesPrecedence(t *testing.T) {
	parts := []midi.Part{
		{Track: 1, Channel: 0, Name: "Piano", NoteCount: 10},
		{Track: 2, Channel: 1, Name: "Guitar", NoteCount: 5},
	}
	got, err := selectPart(parts, 2)
	if err != nil {
		t.Fatalf("selectPart: %v", err)
	}
	if got != parts[1] {
		t.Errorf("got %+v, want %+v", got, parts[1])
	}
}

func TestSelectPartFlagOutOfRange(t *testing.T) {
	parts := []midi.Part{{Track: 1, Channel: 0, Name: "Piano", NoteCount: 10}}
	if _, err := selectPart(parts, 5); err == nil {
		t.Fatal("selectPart with -track out of range: want error, got nil")
	}
}

func writeFile(t *testing.T, path string) {
	t.Helper()
	if err := os.WriteFile(path, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
}
