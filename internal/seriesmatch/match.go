// Package seriesmatch contains title and position matching helpers shared by
// Audiobookshelf import and user-triggered series linking.
package seriesmatch

import (
	"math"
	"strconv"
	"strings"
	"unicode"

	fuzzy "github.com/creditx/go-fuzzywuzzy"
	"github.com/vavallee/bindery/internal/indexer"
	"golang.org/x/text/unicode/norm"
)

func SamePosition(a, b string) bool {
	a = strings.TrimSpace(a)
	b = strings.TrimSpace(b)
	if a == "" || b == "" {
		return false
	}
	if a == b {
		return true
	}
	af, aerr := strconv.ParseFloat(a, 64)
	bf, berr := strconv.ParseFloat(b, 64)
	return aerr == nil && berr == nil && math.Abs(af-bf) < 0.001
}

func NormalizeSeriesName(name string) string {
	normalized := indexer.NormalizeTitleForDedup(strings.TrimSpace(name))
	if normalized == "" {
		return ""
	}
	suffixes := map[string]struct{}{
		"series":     {},
		"trilogy":    {},
		"saga":       {},
		"chronicles": {},
		"cycle":      {},
		"books":      {},
		"novels":     {},
	}
	words := strings.Fields(normalized)
	if len(words) > 1 {
		if _, ok := suffixes[words[len(words)-1]]; ok {
			words = words[:len(words)-1]
		}
	}
	return strings.Join(words, " ")
}

func TitleScore(a, b string) int {
	cleanA := CleanTitle(a)
	cleanB := CleanTitle(b)
	if cleanA == "" || cleanB == "" {
		return 0
	}
	return max(
		safeFuzzyScore(func(a, b string) int { return fuzzy.TokenSetRatio(a, b) }, cleanA, cleanB),
		safeFuzzyScore(func(a, b string) int { return fuzzy.TokenSortRatio(a, b) }, cleanA, cleanB),
		safeFuzzyScore(fuzzy.Ratio, cleanA, cleanB),
		safeFuzzyScore(fuzzy.PartialRatio, cleanA, cleanB),
	)
}

func safeFuzzyScore(score func(string, string) int, a, b string) (value int) {
	defer func() {
		if recover() != nil {
			value = 0
		}
	}()
	return score(a, b)
}

func CleanTitle(title string) string {
	title = norm.NFC.String(strings.TrimSpace(title))
	title = strings.ToLower(title)
	if title == "" {
		return ""
	}
	var b strings.Builder
	for _, r := range title {
		switch {
		case unicode.IsLetter(r), unicode.IsDigit(r):
			b.WriteRune(r)
		case unicode.IsSpace(r):
			b.WriteRune(' ')
		default:
			b.WriteRune(' ')
		}
	}
	noise := map[string]struct{}{
		"a":     {},
		"an":    {},
		"the":   {},
		"novel": {},
		"book":  {},
	}
	words := strings.Fields(b.String())
	out := words[:0]
	for _, word := range words {
		if _, ok := noise[word]; ok {
			continue
		}
		out = append(out, word)
	}
	return strings.Join(out, " ")
}
