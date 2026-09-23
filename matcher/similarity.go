package matcher

import "strings"

// levenshteinSimilarity returns a 0..1 similarity score based on edit
// distance, normalized by the length of the longer string.
func levenshteinSimilarity(a, b string) float64 {
	if a == "" && b == "" {
		return 1
	}
	dist := levenshteinDistance(a, b)
	maxLen := len(a)
	if len(b) > maxLen {
		maxLen = len(b)
	}
	if maxLen == 0 {
		return 1
	}
	return 1 - float64(dist)/float64(maxLen)
}

// levenshteinDistance computes classic single-character edit distance
// between two strings, operating on runes. Uses a single-row DP table.
func levenshteinDistance(a, b string) int {
	ra, rb := []rune(a), []rune(b)
	if len(ra) == 0 {
		return len(rb)
	}
	if len(rb) == 0 {
		return len(ra)
	}

	prev := make([]int, len(rb)+1)
	curr := make([]int, len(rb)+1)
	for j := range prev {
		prev[j] = j
	}

	for i := 1; i <= len(ra); i++ {
		curr[0] = i
		for j := 1; j <= len(rb); j++ {
			cost := 1
			if ra[i-1] == rb[j-1] {
				cost = 0
			}
			del := prev[j] + 1
			ins := curr[j-1] + 1
			sub := prev[j-1] + cost
			curr[j] = min(del, ins, sub)
		}
		prev, curr = curr, prev
	}
	return prev[len(rb)]
}

// jaroWinkler returns the Jaro-Winkler similarity of two strings, a value
// in [0, 1] where 1 means identical. This is a standard, dependency-free
// implementation: it rewards strings that share characters within a small
// window and share a common prefix, which suits short misheard words like
// "honky"/"hanky" or "women"/"woman".
func jaroWinkler(a, b string) float64 {
	ra, rb := []rune(a), []rune(b)
	if len(ra) == 0 && len(rb) == 0 {
		return 1
	}
	if len(ra) == 0 || len(rb) == 0 {
		return 0
	}

	matchDist := max(len(ra), len(rb))/2 - 1
	if matchDist < 0 {
		matchDist = 0
	}

	aMatched := make([]bool, len(ra))
	bMatched := make([]bool, len(rb))

	matches := 0
	for i := range ra {
		start := max(0, i-matchDist)
		end := min(len(rb), i+matchDist+1)
		for j := start; j < end; j++ {
			if bMatched[j] || ra[i] != rb[j] {
				continue
			}
			aMatched[i] = true
			bMatched[j] = true
			matches++
			break
		}
	}

	if matches == 0 {
		return 0
	}

	transpositions := 0
	k := 0
	for i := range ra {
		if !aMatched[i] {
			continue
		}
		for !bMatched[k] {
			k++
		}
		if ra[i] != rb[k] {
			transpositions++
		}
		k++
	}
	transpositions /= 2

	m := float64(matches)
	jaro := (m/float64(len(ra)) + m/float64(len(rb)) + (m-float64(transpositions))/m) / 3

	// Winkler prefix bonus: boost strings sharing a common prefix (up to 4
	// runes), scaled by a fixed 0.1 factor.
	prefix := 0
	for i := 0; i < min(4, min(len(ra), len(rb))); i++ {
		if ra[i] != rb[i] {
			break
		}
		prefix++
	}

	return jaro + float64(prefix)*0.1*(1-jaro)
}

// bestTokenMatch returns the highest Jaro-Winkler similarity between token
// and any token in candidates.
func bestTokenMatch(token string, candidates []string) float64 {
	best := 0.0
	for _, c := range candidates {
		if s := jaroWinkler(token, c); s > best {
			best = s
		}
	}
	return best
}

// tokenOverlap scores how many of titleTokens have a close match somewhere
// in windowTokens, as a fraction in [0, 1]. Matching is fuzzy (not exact
// equality) so that "women" vs "woman" still counts as a match.
const tokenMatchThreshold = 0.85

func tokenOverlap(titleTokens, windowTokens []string) float64 {
	if len(titleTokens) == 0 {
		return 0
	}
	matched := 0
	for _, t := range titleTokens {
		if bestTokenMatch(t, windowTokens) >= tokenMatchThreshold {
			matched++
		}
	}
	return float64(matched) / float64(len(titleTokens))
}

// joinTokens is a small helper to rebuild a normalized string from tokens,
// used when constructing sliding-window candidate strings.
func joinTokens(tokens []string) string {
	return strings.Join(tokens, " ")
}
