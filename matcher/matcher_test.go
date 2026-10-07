package matcher

import (
	"testing"
)

func testSongList() []string {
	return []string{
		"Honky Tonk Women",
		"Honky Tonk Man",
		"Honky Tonk Blues",
		"Folsom Prison Blues",
		"Friends in Low Places",
		"Take It Easy",
		"The Weight",
		"Chattahoochie",
	}
}

func TestMatch_Confident(t *testing.T) {
	m := New(testSongList(), DefaultConfig())

	cases := []struct {
		transcript string
		want       string
	}{
		{"hey guys let's play honky tonk woman next", "Honky Tonk Women"},
		{"okay we're gonna do Folsom Prison Blues", "Folsom Prison Blues"},
		{"this one's take it easy", "Take It Easy"},
		{"hanky tonk women", "Honky Tonk Women"},
		{"honky tonk woman", "Honky Tonk Women"},
		{"folsom prison blues next", "Folsom Prison Blues"},
		{"friends in low places guys", "Friends in Low Places"},
	}

	for _, c := range cases {
		t.Run(c.transcript, func(t *testing.T) {
			res := m.Match(c.transcript)
			if !res.Matched {
				t.Fatalf("Match(%q) = no match, want %q (best=%q score=%.3f second=%q score=%.3f)",
					c.transcript, c.want, res.Best.Title, res.Best.Score, res.Second.Title, res.Second.Score)
			}
			if res.Best.Title != c.want {
				t.Fatalf("Match(%q) = %q (score %.3f), want %q", c.transcript, res.Best.Title, res.Best.Score, c.want)
			}
		})
	}
}

func TestMatch_NoConfidentMatch(t *testing.T) {
	m := New(testSongList(), DefaultConfig())

	cases := []string{
		"let's do something different",
		"can we get some more reverb on that monitor please",
		"thank you very much good night everybody",
	}

	for _, transcript := range cases {
		t.Run(transcript, func(t *testing.T) {
			res := m.Match(transcript)
			if res.Matched {
				t.Fatalf("Match(%q) = %q (score %.3f), want no match", transcript, res.Best.Title, res.Best.Score)
			}
		})
	}
}

// TestMatch_MarginPreventsFalsePositive exercises the case the plan calls
// out explicitly: several similar titles ("Honky Tonk Women/Man/Blues")
// must not cause a confident match unless one clearly wins.
func TestMatch_MarginPreventsFalsePositive(t *testing.T) {
	m := New(testSongList(), DefaultConfig())

	res := m.Match("honky tonk")
	if res.Matched {
		t.Fatalf("ambiguous transcript %q matched %q; margin should have blocked it (best=%.3f second=%.3f)",
			"honky tonk", res.Best.Title, res.Best.Score, res.Second.Score)
	}
}

// TestMatch_PhoneticNearMissRanksBest exercises a real-world failure mode:
// Whisper misheard "Chattahoochie" as this phonetically close but very
// differently spelled phrase. phoneticKey's consonant-skeleton comparison
// (see phonetic.go) is what lets the right title still rank first by a
// clear margin, even though a single word fragmenting into four wrong
// ones is extreme enough that it doesn't clear the default confidence
// threshold on its own — the --prompt biasing wired up in speech/whisper.go
// is what should keep Whisper from mangling it this badly in practice.
func TestMatch_PhoneticNearMissRanksBest(t *testing.T) {
	m := New(testSongList(), DefaultConfig())

	res := m.Match("chart a who chi")
	if res.Best.Title != "Chattahoochie" {
		t.Fatalf("Best.Title = %q (score %.3f), want %q", res.Best.Title, res.Best.Score, "Chattahoochie")
	}
	const minMargin = 0.15
	if margin := res.Best.Score - res.Second.Score; margin < minMargin {
		t.Fatalf("margin over second place = %.3f, want >= %.3f (best=%.3f second=%q %.3f)",
			margin, minMargin, res.Best.Score, res.Second.Title, res.Second.Score)
	}
}

func TestMatch_Deterministic(t *testing.T) {
	m := New(testSongList(), DefaultConfig())
	const transcript = "hey guys let's play honky tonk woman next"

	first := m.Match(transcript)
	for i := 0; i < 10; i++ {
		again := m.Match(transcript)
		if again != first {
			t.Fatalf("Match is not deterministic: run %d = %+v, want %+v", i, again, first)
		}
	}
}

func TestMatch_EmptySongList(t *testing.T) {
	m := New(nil, DefaultConfig())
	res := m.Match("honky tonk women")
	if res.Matched {
		t.Fatal("expected no match against an empty song list")
	}
}
