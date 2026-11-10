package lyrics

import (
	"bufio"
	"context"
	"os"
	"strconv"
	"strings"
	"testing"
)

// TestLiveMatchesRealKaraokeTitles asks the real LRCLIB about every title in
// testdata/karaoke-titles.tsv. It is off by default (network); LRCLIB_LIVE=1 runs it.
func TestLiveMatchesRealKaraokeTitles(t *testing.T) {
	if os.Getenv("LRCLIB_LIVE") == "" {
		t.Skip("set LRCLIB_LIVE=1 to ask lrclib.net")
	}
	f, err := os.Open("testdata/karaoke-titles.tsv")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	c := New("karaoke-selfhosted/test")
	right, total := 0, 0
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := scanner.Text()
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		cols := strings.SplitN(line, "\t", 3)
		want, title := cols[0], cols[2]
		duration, _ := strconv.ParseFloat(cols[1], 64)
		total++
		got, ok, err := c.Match(context.Background(), title, duration)
		switch {
		case err != nil:
			t.Errorf("%s: %v", title, err)
		case !ok:
			t.Logf("MISS  %-70s  parts=%q", title, Parts(title))
		case !strings.Contains(Fold(got.Track), want):
			t.Logf("WRONG %-70s  -> %s / %s", title, got.Track, got.Artist)
		default:
			right++
			if os.Getenv("LRCLIB_LIVE") == "2" {
				t.Logf("OK    %-70s  -> %s / %s (%.0fs, %d lines)", title, got.Track, got.Artist, got.Duration, strings.Count(got.Lyrics, "\n")+1)
			}
		}
	}
	t.Logf("%d of %d titles matched the right song", right, total)
	if right*100 < total*90 {
		t.Errorf("only %d of %d right", right, total)
	}
}
