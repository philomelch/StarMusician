package library

import (
	"os"
	"path/filepath"
	"testing"
)

func TestListMIDIFilesFiltersByExtensionCaseInsensitively(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "b.mid"))
	writeFile(t, filepath.Join(dir, "a.MID"))
	writeFile(t, filepath.Join(dir, "notes.txt"))
	if err := os.Mkdir(filepath.Join(dir, "subdir.mid"), 0o755); err != nil {
		t.Fatal(err)
	}

	files, err := ListMIDIFiles(dir)
	if err != nil {
		t.Fatalf("ListMIDIFiles: %v", err)
	}

	want := []string{filepath.Join(dir, "a.MID"), filepath.Join(dir, "b.mid")}
	if len(files) != len(want) {
		t.Fatalf("files = %v, want %v", files, want)
	}
	for i := range want {
		if files[i] != want[i] {
			t.Errorf("files[%d] = %q, want %q", i, files[i], want[i])
		}
	}
}

func TestListMIDIFilesMissingDirReturnsEmptyNotError(t *testing.T) {
	files, err := ListMIDIFiles(filepath.Join(t.TempDir(), "does-not-exist"))
	if err != nil {
		t.Fatalf("ListMIDIFiles on missing dir: %v", err)
	}
	if len(files) != 0 {
		t.Errorf("files = %v, want none", files)
	}
}

func TestExecutableRelativeDirPassesThroughAbsolutePaths(t *testing.T) {
	abs := t.TempDir()
	got, err := ExecutableRelativeDir(abs)
	if err != nil {
		t.Fatalf("ExecutableRelativeDir: %v", err)
	}
	if got != abs {
		t.Errorf("got %q, want %q", got, abs)
	}
}

func TestExecutableRelativeDirJoinsRelativeToExecutable(t *testing.T) {
	exe, err := os.Executable()
	if err != nil {
		t.Fatalf("os.Executable: %v", err)
	}
	want := filepath.Join(filepath.Dir(exe), "midi")

	got, err := ExecutableRelativeDir("midi")
	if err != nil {
		t.Fatalf("ExecutableRelativeDir: %v", err)
	}
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func writeFile(t *testing.T, path string) {
	t.Helper()
	if err := os.WriteFile(path, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
}
