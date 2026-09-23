package songs

import (
	"os"
	"path/filepath"
	"testing"
)

func writeTemp(t *testing.T, contents string) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "songs.txt")
	if err := os.WriteFile(path, []byte(contents), 0o644); err != nil {
		t.Fatalf("write temp song list: %v", err)
	}
	return path
}

func TestLoadFile(t *testing.T) {
	path := writeTemp(t, "Honky Tonk Women\n\n# a comment\nFolsom Prison Blues\n  Take It Easy  \n")

	got, err := LoadFile(path)
	if err != nil {
		t.Fatalf("LoadFile: %v", err)
	}

	want := []string{"Honky Tonk Women", "Folsom Prison Blues", "Take It Easy"}
	if len(got) != len(want) {
		t.Fatalf("got %d songs, want %d: %+v", len(got), len(want), got)
	}
	for i, w := range want {
		if got[i].Title != w {
			t.Errorf("song %d = %q, want %q", i, got[i].Title, w)
		}
	}
}

func TestLoadFileEmpty(t *testing.T) {
	path := writeTemp(t, "\n# only comments\n\n")
	if _, err := LoadFile(path); err == nil {
		t.Fatal("expected error for song list with no titles")
	}
}

func TestLoadFileMissing(t *testing.T) {
	if _, err := LoadFile(filepath.Join(t.TempDir(), "nope.txt")); err == nil {
		t.Fatal("expected error for missing file")
	}
}
