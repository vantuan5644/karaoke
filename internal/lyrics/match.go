package lyrics

import (
	"regexp"
	"strings"
	"unicode"

	"github.com/vantuan5644/karaoke/internal/fold"
)

// Fold lowercases s and takes the accents off; see fold.String.
func Fold(s string) string { return fold.String(s) }

var (
	bracketed = regexp.MustCompile(`\([^)]*\)|\[[^\]]*\]|【[^】]*】|「[^」]*」|\{[^}]*\}|"[^"]*karaoke[^"]*"`)
	// Where a title splits into artist and song: " - ", " | ", " / ", "//", and a dash that
	// touches only one word ("Roads -John Denver"). A dash inside a word ("M-TP") is not one.
	separator = regexp.MustCompile(`\s*\|\s*|\s*//\s*|\s+[-–—/:]+\s*|\s*[-–—/]+\s+`)
	// What karaoke channels add to a title, in folded form. Single words that could be part
	// of a song's name ("new", "song", "from") are left alone: the scoring tolerates them.
	noise = regexp.MustCompile(`\b(` + strings.Join([]string{
		`www\b.*$`, `\S+\.(com|net|vn)\b`,
		`karaoke( version| songs?)?( from zoom karaoke)?`, `karafun`, `zoom karaoke`, `sing king`,
		`(with )?(backing vocals|lyrics on screen|guide melody|no guide melody)`,
		`instrumentals?`, `no vocals?`, `sing ?along`, `unofficial`,
		`official( music| lyric)?( video| audio| mv)?`, `music video`, `lyrics? video`, `lyrics?`, `mv`,
		`(full )?(hd|4k|hq)`, `full beat`, `beat( chuan| goc| phoi chuan)?`, `phoi be`, `co be`,
		`tone (nu|nam)`, `ha \d+ tone`, `(higher|lower|female|femaler|male|original) key`,
		`(piano|acoustic) karaoke`, `guitar solo`, `remake`, `(original )?version`,
	}, "|") + `)\b`)
	punctuation = regexp.MustCompile(`[^\p{L}\p{N}]+`)
	hashtag     = regexp.MustCompile(`#\S+`)
)

// Parts takes the karaoke dressing off a video title and returns what is left, split where
// the title separates artist from song: "Queen - Bohemian Rhapsody (Karaoke Version)" gives
// "queen" and "bohemian rhapsody". The parts are folded.
func Parts(title string) []string {
	s := bracketed.ReplaceAllString(hashtag.ReplaceAllString(Fold(title), " "), " ")
	var parts []string
	for _, seg := range separator.Split(s, -1) {
		seg = noise.ReplaceAllString(seg, " ")
		seg = strings.Join(strings.Fields(punctuation.ReplaceAllString(seg, " ")), " ")
		if seg != "" {
			parts = append(parts, seg)
		}
	}
	return parts
}

func tokens(s string) map[string]bool {
	set := map[string]bool{}
	for _, w := range strings.Fields(punctuation.ReplaceAllString(Fold(s), " ")) {
		set[w] = true
	}
	return set
}

// covered is the share of a's words that are also in b.
func covered(a, b map[string]bool) float64 {
	if len(a) == 0 {
		return 0
	}
	n := 0
	for w := range a {
		if b[w] {
			n++
		}
	}
	return float64(n) / float64(len(a))
}

// Below minTrack, the title doesn't name the track; at goodEnough a search stops looking.
const (
	minTrack   = 0.6
	minScore   = 0.55
	goodEnough = 0.85
)

// score rates how well a track fits a title's words and the video's length (0 to 1).
// Most of it is whether the title names the track: a remix or a "DJ Version" of the song
// has words the title lacks, so the plain song wins. Then whether the track and artist
// together account for the title's words, so a song called "Queen" doesn't win for
// "Queen - Bohemian Rhapsody". Length counts a little: karaoke versions run a few seconds to
// half a minute off the record. And when the title has accents (Vietnamese, say), lyrics
// someone typed without them lose a little: they are hard to sing from.
func score(t Track, title map[string]bool, duration float64, accented bool) float64 {
	if t.Instrumental || strings.TrimSpace(t.Lyrics) == "" {
		return 0
	}
	track := tokens(t.Track)
	named := covered(track, title)
	if named < minTrack {
		return 0
	}
	both := tokens(t.Track + " " + t.Artist)
	explained := covered(title, both)
	length := 0.0
	if duration > 0 && t.Duration > 0 {
		d := duration - t.Duration
		if d < 0 {
			d = -d
		}
		length = max(0, 1-d/45)
	}
	s := 0.55*named + 0.35*explained + 0.10*length
	if accented && plainASCII(t.Lyrics) {
		s -= 0.1
	}
	return s
}

func plainASCII(s string) bool {
	for _, r := range s {
		if r > unicode.MaxASCII {
			return false
		}
	}
	return true
}

// hasAccents says whether s has letters that Fold changes beyond case.
func hasAccents(s string) bool {
	return Fold(s) != strings.ToLower(s)
}

// queries lists what to search for, best guess first: the cleaned title, then each part on
// its own, then the title with words dropped from its end (channels append their own names).
func queries(parts []string) []string {
	joined := strings.Join(parts, " ")
	out := []string{joined}
	seen := map[string]bool{joined: true}
	add := func(q string) {
		if q != "" && !seen[q] {
			seen[q] = true
			out = append(out, q)
		}
	}
	if len(parts) > 1 {
		for _, p := range parts {
			if len(strings.Fields(p)) >= 1 {
				add(p)
			}
		}
	}
	words := strings.Fields(joined)
	for n := len(words) - 1; n >= 2 && len(words)-n <= 4; n-- {
		add(strings.Join(words[:n], " "))
	}
	return out
}
