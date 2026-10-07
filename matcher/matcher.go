// Package matcher identifies which known song title (if any) is being
// referred to inside a noisy, imperfect speech transcript.
//
// It is deliberately independent of audio capture and speech recognition:
// it operates purely on strings, so it can be developed and tested without
// a microphone or Whisper.
package matcher

// Candidate is one scored title from a Match call.
type Candidate struct {
	Title string
	Score float64
}

// Result is the outcome of matching a transcript against the song list.
type Result struct {
	// Transcript is the raw input text that was matched.
	Transcript string
	// Best is the highest scoring candidate, if any songs were supplied.
	Best Candidate
	// Second is the next-highest scoring candidate, used to compute the
	// margin required for a confident match.
	Second Candidate
	// Matched reports whether Best clears both the score and margin
	// thresholds and should be treated as an identified song.
	Matched bool
}

// Config holds the tunable thresholds for deciding whether a candidate
// match is confident enough to report. Both a minimum score and a minimum
// margin over the second-best candidate are required, because a false
// positive (announcing the wrong song) is worse than reporting no match.
type Config struct {
	// ScoreThreshold is the minimum score Best must reach, in [0, 1].
	ScoreThreshold float64
	// MarginThreshold is the minimum Best-Second score gap required.
	MarginThreshold float64
}

// DefaultConfig returns reasonable starting thresholds, tuned against the
// examples in plans/init.md. Callers should expose these as CLI flags so
// they can be adjusted without a rebuild.
func DefaultConfig() Config {
	return Config{
		ScoreThreshold:  0.81,
		MarginThreshold: 0.08,
	}
}

// Matcher scores transcripts against a fixed list of song titles.
type Matcher struct {
	cfg    Config
	titles []preparedTitle
}

type preparedTitle struct {
	title    string
	tokens   []string
	phonetic string
}

// New builds a Matcher for the given song titles and thresholds.
func New(titles []string, cfg Config) *Matcher {
	prepared := make([]preparedTitle, 0, len(titles))
	for _, title := range titles {
		tokens := tokenize(normalize(title))
		prepared = append(prepared, preparedTitle{
			title:    title,
			tokens:   tokens,
			phonetic: phoneticJoin(tokens),
		})
	}
	return &Matcher{cfg: cfg, titles: prepared}
}

// Match scores transcript against every known title and reports whether a
// confident match was found.
func (m *Matcher) Match(transcript string) Result {
	res := Result{Transcript: transcript}
	if len(m.titles) == 0 {
		return res
	}

	transcriptTokens := tokenize(normalize(transcript))

	var best, second Candidate
	for _, t := range m.titles {
		score := scoreTitle(t, transcriptTokens)
		c := Candidate{Title: t.title, Score: score}
		switch {
		case c.Score > best.Score:
			second = best
			best = c
		case c.Score > second.Score:
			second = c
		}
	}

	res.Best = best
	res.Second = second
	res.Matched = best.Title != "" &&
		best.Score >= m.cfg.ScoreThreshold &&
		(best.Score-second.Score) >= m.cfg.MarginThreshold
	return res
}

// scoreTitle finds the best-scoring word window of transcriptTokens for a
// single title, per the sliding-window approach described in
// plans/init.md: a title is compared against likely substrings of the
// transcript rather than the transcript as a whole.
func scoreTitle(t preparedTitle, transcriptTokens []string) float64 {
	if len(t.tokens) == 0 || len(transcriptTokens) == 0 {
		return 0
	}

	titleStr := joinTokens(t.tokens)
	best := 0.0

	for _, size := range windowSizes(len(t.tokens), len(transcriptTokens)) {
		for start := 0; start+size <= len(transcriptTokens); start++ {
			window := transcriptTokens[start : start+size]
			windowStr := joinTokens(window)

			score := 0.4*jaroWinkler(titleStr, windowStr) +
				0.2*tokenOverlap(t.tokens, window) +
				0.15*levenshteinSimilarity(titleStr, windowStr) +
				0.25*levenshteinSimilarity(t.phonetic, phoneticJoin(window))

			if score > best {
				best = score
			}
		}
	}

	// An exact (post-normalization) substring match is unambiguous: the
	// spoken words for the title appear verbatim in the transcript.
	if containsSubsequence(transcriptTokens, t.tokens) {
		best = max(best, 0.97)
	}

	return best
}

// windowSizes returns the window lengths to try: the title's own token
// count, plus one shorter and one longer, to tolerate Whisper dropping or
// inserting a word (e.g. trailing "next"). Sizes are clamped to the
// transcript length.
//
// Titles of 1-2 tokens get extra slack above that, up to 4: a single
// unusual word (e.g. "Chattahoochie") can come out of Whisper split into
// several small mistranscribed words, which the usual +1 slack can't
// reach — longer titles rarely fragment that badly, so they don't need
// the wider search.
func windowSizes(titleLen, transcriptLen int) []int {
	minSize := titleLen - 1
	maxSize := titleLen + 1
	if titleLen <= 2 && maxSize < 4 {
		maxSize = 4
	}

	sizes := make([]int, 0, maxSize-minSize+1)
	for size := minSize; size <= maxSize; size++ {
		if size >= 1 && size <= transcriptLen {
			sizes = append(sizes, size)
		}
	}
	return sizes
}

// containsSubsequence reports whether title appears verbatim, in order, as
// a contiguous run within transcript.
func containsSubsequence(transcript, title []string) bool {
	if len(title) == 0 || len(title) > len(transcript) {
		return false
	}
	for start := 0; start+len(title) <= len(transcript); start++ {
		match := true
		for i, w := range title {
			if transcript[start+i] != w {
				match = false
				break
			}
		}
		if match {
			return true
		}
	}
	return false
}
