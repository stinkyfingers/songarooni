package matcher

import (
	"strings"
	"unicode"
)

// contractions expands a small set of common contractions before
// punctuation is stripped, so "let's" becomes "let us" rather than "lets"
// losing the word boundary. Kept deliberately small: this is here to help
// tokenization, not to be a general English contraction dictionary.
var contractions = map[string]string{
	"let's":   "let us",
	"we're":   "we are",
	"we'll":   "we will",
	"gonna":   "going to",
	"wanna":   "want to",
	"it's":    "it is",
	"that's":  "that is",
	"here's":  "here is",
	"what's":  "what is",
	"can't":   "cannot",
	"don't":   "do not",
	"doesn't": "does not",
}

// normalize lowercases, expands common contractions, strips punctuation,
// and collapses whitespace. It intentionally does not stem words: stemming
// with only ~100 short song titles risks making distinct titles collide.
func normalize(s string) string {
	s = strings.ToLower(s)

	words := strings.Fields(s)
	for i, w := range words {
		if expanded, ok := contractions[w]; ok {
			words[i] = expanded
		}
	}
	s = strings.Join(words, " ")

	var b strings.Builder
	b.Grow(len(s))
	for _, r := range s {
		switch {
		case unicode.IsLetter(r) || unicode.IsDigit(r):
			b.WriteRune(r)
		case unicode.IsSpace(r):
			b.WriteRune(' ')
		default:
			// Drop punctuation entirely (treat it as a separator so
			// "don't" without contraction expansion still splits sensibly,
			// and stray marks like commas/periods disappear).
			b.WriteRune(' ')
		}
	}

	return strings.Join(strings.Fields(b.String()), " ")
}

// tokenize splits already-normalized text into words.
func tokenize(s string) []string {
	if s == "" {
		return nil
	}
	return strings.Fields(s)
}
