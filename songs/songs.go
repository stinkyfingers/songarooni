// Package songs loads the list of known song titles from a plain text file.
package songs

import (
	"bufio"
	"fmt"
	"os"
	"strings"
)

// Song is a single entry from the song list file.
type Song struct {
	// Title is the original, unmodified title as written in the file.
	Title string
	// Aliases are optional alternate spellings/names for the same song.
	// The file format for aliases is not yet defined; this exists so the
	// matcher and file format can grow without an API change.
	Aliases []string
}

// LoadFile reads a song list from path, one title per line.
//
// Blank lines and lines beginning with '#' are ignored so the file can
// contain comments and spacing for readability.
func LoadFile(path string) ([]Song, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("open song list %q: %w", path, err)
	}
	defer f.Close()

	var out []Song
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		out = append(out, Song{Title: line})
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("read song list %q: %w", path, err)
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("song list %q contains no titles", path)
	}
	return out, nil
}
