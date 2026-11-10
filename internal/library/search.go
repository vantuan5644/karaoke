package library

import (
	"regexp"
	"sort"
	"strings"

	"github.com/vantuan5644/karaoke/internal/fold"
)

var (
	nonWord = regexp.MustCompile(`[^\p{L}\p{N}]+`)
	// A track number in front of a name: "01. ", "07_", "3) ". A bare number is left alone,
	// since it may be the song ("99 Luftballons").
	trackNumber = regexp.MustCompile(`^\d{1,3}\s*[._)]\s*`)
	// A karaoke disc's code: "SC8125-01", "PHM0705-03", "SF001-05", "ZM12345".
	discCode = regexp.MustCompile(`(?i)^[a-z]{1,6}[\s-]?\d{2,}([\s-]?\d+)*[a-z]?$`)
)

// parseName reads artist and title from a file name: "Queen - Bohemian Rhapsody", with a
// disc code or track number in front ("SC8125-01 - Queen - Bohemian Rhapsody") taken off.
// With no " - " in it the whole name is the title.
func parseName(stem string) (artist, title string) {
	// The track number first: "07_" is one, "07 " (underscores read as spaces) is not.
	s := trackNumber.ReplaceAllString(strings.TrimSpace(stem), "")
	s = strings.ReplaceAll(s, "_", " ")
	var parts []string
	for _, p := range strings.FieldsFunc(s, func(r rune) bool { return r == '–' || r == '—' }) {
		for _, q := range strings.Split(p, " - ") {
			if q = strings.TrimSpace(q); q != "" {
				parts = append(parts, q)
			}
		}
	}
	// A code only counts as one in front of both an artist and a title, so "Prince - 1999"
	// keeps its title.
	if len(parts) >= 3 && discCode.MatchString(parts[0]) {
		parts = parts[1:]
	}
	switch len(parts) {
	case 0:
		return "", strings.TrimSpace(stem)
	case 1:
		return "", parts[0]
	}
	return parts[0], strings.Join(parts[1:], " - ")
}

// words splits text into folded words.
func words(s string) []string {
	return strings.Fields(nonWord.ReplaceAllString(fold.String(s), " "))
}

// Search returns up to limit songs whose artist, title or folder hold every word of the
// query, each word a whole word or the start of one, so "boh rhap" finds Bohemian Rhapsody
// and "nang am" finds Nắng Ấm Xa Dần. Whole words, and the name starting with the query,
// rank first.
func (l *Library) Search(query string, limit int) []Item {
	want := words(query)
	if len(want) == 0 || limit <= 0 {
		return nil
	}
	phrase := strings.Join(want, " ")
	type hit struct {
		it    Item
		score int
	}
	var hits []hit
	l.mu.RLock()
	for _, it := range l.sorted {
		score, ok := 0, true
		for _, w := range want {
			best := 0
			for _, have := range it.words {
				if have == w {
					best = 2
					break
				}
				if strings.HasPrefix(have, w) {
					best = 1
				}
			}
			if best == 0 {
				ok = false
				break
			}
			score += best
		}
		if !ok {
			continue
		}
		title := strings.Join(words(it.Title), " ")
		if strings.HasPrefix(title, phrase) || strings.HasPrefix(it.name, phrase) {
			score += 3
		}
		hits = append(hits, hit{l.items[it.ID], score})
	}
	l.mu.RUnlock()
	sort.SliceStable(hits, func(i, j int) bool { return hits[i].score > hits[j].score })
	if len(hits) > limit {
		hits = hits[:limit]
	}
	out := make([]Item, len(hits))
	for i, h := range hits {
		out[i] = h.it
	}
	return out
}
