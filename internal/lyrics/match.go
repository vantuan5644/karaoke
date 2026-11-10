package lyrics

import (
	"regexp"
	"slices"
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
	// What karaoke channels add to a title, in folded form. The title splits where one is, as
	// at a separator: "Anh Đau Từ Lúc Em Đi Karaoke Tone Nữ Beat Live Band" is the song, then
	// the channel's tags. Single words that could be part of a song's name ("new", "song",
	// "from", "cover", "remix") are left alone: the scoring tolerates them.
	noise = regexp.MustCompile(`\b(` + strings.Join([]string{
		`www\b.*$`, `\S+\.(com|net|vn)\b`,
		`karaoke( version| songs)?( from zoom karaoke)?`, `karafun`, `zoom karaoke`, `sing king`,
		`(with )?(backing vocals|lyrics on screen|guide melody|no guide melody)`,
		`instrumentals?`, `no vocals?`, `sing ?along`, `unofficial`,
		`official( music| lyric)?( video| audio| mv)?`, `music video`, `lyrics? video`, `lyrics?`, `mv`,
		`(full )?(hd|4k|hq)`, `full beat`, `beat( chuan| goc| phoi( chuan)?| moi)?`, `phoi be`, `co be`,
		`tone (nu|nam)`, `ha \d+ tone`, `(higher|lower|female|femaler|male|original) key`,
		`(piano|acoustic) karaoke`, `guitar solo`, `remake`, `(original )?version`,
		`live band`, `nhac (song|tre)`, `de hat`, `hay nhat`,
	}, "|") + `)\b`)
	punctuation = regexp.MustCompile(`[^\p{L}\p{N}]+`)
	hashtag     = regexp.MustCompile(`#\S+`)
	cuts        = []*regexp.Regexp{hashtag, bracketed, separator, noise}
)

// A piece is one part of a cleaned title: folded, and as the title wrote it.
type piece struct{ folded, text string }

// pieces takes the karaoke dressing off a title and splits what is left where the title
// separates artist from song. The dressing is found in the folded title, so its patterns
// need no accents, and each piece is then cut from the title as written.
func pieces(title string) []piece {
	folded, from := foldIndexed(title)
	var spans [][]int
	for _, re := range cuts {
		spans = append(spans, re.FindAllStringIndex(folded, -1)...)
	}
	slices.SortFunc(spans, func(a, b []int) int { return a[0] - b[0] })
	var out []piece
	at := 0
	keep := func(end int) {
		words := strings.Fields(punctuation.ReplaceAllString(folded[at:end], " "))
		if len(words) > 0 {
			text := strings.TrimFunc(title[from[at]:from[end]], notWordy)
			out = append(out, piece{strings.Join(words, " "), strings.Join(strings.Fields(text), " ")})
		}
	}
	for _, span := range spans {
		if span[0] > at {
			keep(span[0])
		}
		at = max(at, span[1])
	}
	if at < len(folded) {
		keep(len(folded))
	}
	return out
}

// foldIndexed folds s a letter at a time and says, for each byte of the result, where in s
// it came from; from[len(folded)] is len(s).
func foldIndexed(s string) (folded string, from []int) {
	var b strings.Builder
	from = make([]int, 0, len(s)+1)
	for i, r := range s {
		f := fold.String(string(r))
		b.WriteString(f)
		for range len(f) {
			from = append(from, i)
		}
	}
	return b.String(), append(from, len(s))
}

func notWordy(r rune) bool {
	return !unicode.IsLetter(r) && !unicode.IsNumber(r) && !unicode.IsMark(r)
}

// Parts takes the karaoke dressing off a video title and returns what is left, split where
// the title separates artist from song: "Queen - Bohemian Rhapsody (Karaoke Version)" gives
// "queen" and "bohemian rhapsody". The parts are folded.
func Parts(title string) []string {
	var parts []string
	for _, p := range pieces(title) {
		parts = append(parts, p.folded)
	}
	return parts
}

// Clean is the title without its karaoke dressing, accents and capitals kept: what someone
// would type to look the song up. "Karaoke Anh Đau Từ Khúc Em Đi - Quốc Thiên | Beat Chuẩn"
// gives "Anh Đau Từ Khúc Em Đi - Quốc Thiên".
func Clean(title string) string {
	var texts []string
	for _, p := range pieces(title) {
		texts = append(texts, p.text)
	}
	return strings.Join(texts, " - ")
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

// maxQueries is the most searches one title costs LRCLIB.
const maxQueries = 16

// queries lists what to search for, best guess first. plain is the cleaned title, then each
// part on its own, then the title with words dropped from its end (channels append their own
// names). LRCLIB finds only records with every word searched for, so loose, for when plain
// finds nothing, has each part with one word left out (an uploader's misspelling: "Khúc" for
// "Lúc") and with words dropped from its end.
func queries(parts []string) (plain, loose []string) {
	seen := map[string]bool{}
	add := func(to *[]string, qs ...string) {
		for _, q := range qs {
			if q != "" && !seen[q] && len(plain)+len(loose) < maxQueries {
				seen[q] = true
				*to = append(*to, q)
			}
		}
	}
	joined := strings.Join(parts, " ")
	add(&plain, joined)
	if len(parts) > 1 {
		add(&plain, parts...)
	}
	add(&plain, shorter(joined)...)
	for _, p := range parts {
		add(&loose, leaveOneOut(p)...)
		add(&loose, shorter(p)...)
	}
	return plain, loose
}

// shorter is s with one to four words dropped from its end, longest first, keeping two.
func shorter(s string) []string {
	words := strings.Fields(s)
	var out []string
	for n := len(words) - 1; n >= 2 && len(words)-n <= 4; n-- {
		out = append(out, strings.Join(words[:n], " "))
	}
	return out
}

// leaveOneOut is s with each of its words left out in turn, for three to eight words: fewer
// would leave a query that matches anything, more would cost too many searches.
func leaveOneOut(s string) []string {
	words := strings.Fields(s)
	if len(words) < 3 || len(words) > 8 {
		return nil
	}
	out := make([]string, 0, len(words))
	for i := range words {
		out = append(out, strings.Join(slices.Concat(words[:i], words[i+1:]), " "))
	}
	return out
}
