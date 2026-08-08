// Package library locates .mid files for the portable app: it looks next
// to the running executable (in a "midi" subdirectory by default) rather
// than the caller's working directory, so the app finds its own files
// regardless of how or from where it's launched. Shared by the CLI and the
// GUI so both browse the exact same file set the exact same way.
package library

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// ExecutableRelativeDir resolves dir relative to the running executable's
// directory, so a portable build finds its own files regardless of the
// caller's working directory. An absolute dir is returned unchanged.
func ExecutableRelativeDir(dir string) (string, error) {
	if filepath.IsAbs(dir) {
		return dir, nil
	}
	exe, err := os.Executable()
	if err != nil {
		return "", fmt.Errorf("locating executable directory: %w", err)
	}
	return filepath.Join(filepath.Dir(exe), dir), nil
}

// ListMIDIFiles returns the sorted, full paths of every .mid file (matched
// case-insensitively) directly inside dir. A missing dir yields an empty
// list rather than an error, since "no midi folder yet" is a normal state
// for a fresh portable install.
func ListMIDIFiles(dir string) ([]string, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("reading %s: %w", dir, err)
	}

	var files []string
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		if strings.EqualFold(filepath.Ext(e.Name()), ".mid") {
			files = append(files, filepath.Join(dir, e.Name()))
		}
	}
	sort.Strings(files)
	return files, nil
}
