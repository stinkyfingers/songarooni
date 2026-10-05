package songs

import (
	"os"
	"path/filepath"
	"testing"
)

func writeTemp(t *testing.T, contents string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "songs.csv")
	if err := os.WriteFile(path, []byte(contents), 0o644); err != nil {
		t.Fatalf("write temp song list: %v", err)
	}
	return path
}

func TestLoadFile(t *testing.T) {
	// Includes a title with an embedded, quoted comma — a naive
	// strings.Split(line, ",") would mis-parse this row.
	path := writeTemp(t, "title,path\n"+
		"Honky Tonk Women,honky_tonk_women\n"+
		"Folsom Prison Blues,folsom_prison_blues\n"+
		"\"I Like It, I Love It\",i_like_it_i_love_it\n")

	got, err := LoadFile(path)
	if err != nil {
		t.Fatalf("LoadFile: %v", err)
	}

	want := map[string]string{
		"Honky Tonk Women":     "honky_tonk_women",
		"Folsom Prison Blues":  "folsom_prison_blues",
		"I Like It, I Love It": "i_like_it_i_love_it",
	}
	if len(got) != len(want) {
		t.Fatalf("got %d songs, want %d: %+v", len(got), len(want), got)
	}
	for title, wantPath := range want {
		if gotPath := got[title]; gotPath != wantPath {
			t.Errorf("songs[%q] = %q, want %q", title, gotPath, wantPath)
		}
	}
}

func TestLoadFile_DuplicateTitlesLastWins(t *testing.T) {
	path := writeTemp(t, "title,path\n"+
		"Goodbye Earl,goodbye_earl\n"+
		"Goodbye Earl,goodbye_earl\n")

	got, err := LoadFile(path)
	if err != nil {
		t.Fatalf("LoadFile: %v", err)
	}
	if got["Goodbye Earl"] != "goodbye_earl" {
		t.Errorf("songs[Goodbye Earl] = %q, want %q", got["Goodbye Earl"], "goodbye_earl")
	}
}

func TestLoadFileEmpty(t *testing.T) {
	path := writeTemp(t, "title,path\n")
	if _, err := LoadFile(path); err == nil {
		t.Fatal("expected error for song list with no titles")
	}
}

func TestLoadFileMissing(t *testing.T) {
	if _, err := LoadFile(filepath.Join(t.TempDir(), "nope.csv")); err == nil {
		t.Fatal("expected error for missing file")
	}
}
