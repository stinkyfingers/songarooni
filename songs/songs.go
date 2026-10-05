// Package songs loads the known song list — title plus its slideshow
// subdirectory name — from a CSV file.
package songs

import (
	"encoding/csv"
	"fmt"
	"os"
)

// LoadFile reads a CSV file with a header row (e.g. "title,path")
// followed by rows mapping a song title to the slideshow subdirectory
// name to use for it, e.g.:
//
//	title,path
//	Mustang Sally,mustang_sally
//	"I Like It, I Love It",i_like_it_i_love_it
//
// A real CSV parser is used deliberately rather than splitting lines on
// commas: some titles contain commas themselves and are quoted.
//
// The returned map is the single source of truth for both the matcher
// (its keys are the known titles) and the slideshow (title -> the
// subdirectory to pull images from).
func LoadFile(path string) (map[string]string, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("open song list %q: %w", path, err)
	}
	defer f.Close()

	r := csv.NewReader(f)
	r.FieldsPerRecord = 2

	records, err := r.ReadAll()
	if err != nil {
		return nil, fmt.Errorf("parse song list %q: %w", path, err)
	}
	if len(records) == 0 {
		return nil, fmt.Errorf("song list %q is empty", path)
	}

	songs := make(map[string]string, len(records)-1)
	for _, record := range records[1:] { // skip header row
		title, subdir := record[0], record[1]
		songs[title] = subdir
	}
	if len(songs) == 0 {
		return nil, fmt.Errorf("song list %q contains no titles", path)
	}
	return songs, nil
}
