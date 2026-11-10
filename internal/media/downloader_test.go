package media

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// The test binary doubles as a fake yt-dlp: with FAKE_YTDLP set it behaves like yt-dlp for
// one video and exits, before the testing package ever looks at its arguments.
func TestMain(m *testing.M) {
	if mode := os.Getenv("FAKE_YTDLP"); mode != "" {
		os.Exit(fakeYtdlp(mode, os.Args[1:]))
	}
	os.Exit(m.Run())
}

func fakeYtdlp(mode string, args []string) int {
	var template, id string
	for i, a := range args {
		if a == "-o" && i+1 < len(args) {
			template = args[i+1]
		}
		if a == "--" && i+1 < len(args) {
			id = args[i+1]
		}
	}
	if log := os.Getenv("FAKE_YTDLP_LOG"); log != "" {
		f, _ := os.OpenFile(log, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
		fmt.Fprintln(f, strings.Join(args, " "))
		f.Close()
	}
	switch mode {
	case "fail":
		fmt.Fprintln(os.Stderr, "[youtube] "+id+": Downloading webpage")
		fmt.Fprintln(os.Stderr, "ERROR: [youtube] "+id+": Video unavailable")
		return 1
	case "hang":
		time.Sleep(time.Minute)
		return 1
	case "nofile":
		return 0
	}
	for _, p := range []string{"  0.0%", " 50.0%", "100.0%", "  3.0%", "100.0%"} {
		fmt.Println("KDL " + p)
	}
	out := strings.NewReplacer("%(id)s", id, "%(ext)s", "mp4").Replace(template)
	if err := os.WriteFile(out, []byte(strings.Repeat("v", 1000)), 0o644); err != nil {
		fmt.Fprintln(os.Stderr, "ERROR: "+err.Error())
		return 1
	}
	return 0
}

func newTest(t *testing.T, mode string, cfg Config) (*Downloader, *atomic.Int32) {
	t.Helper()
	t.Setenv("FAKE_YTDLP", mode)
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	var changes atomic.Int32
	cfg.Bin = self
	if cfg.Dir == "" {
		cfg.Dir = t.TempDir()
	}
	cfg.OnChange = func() { changes.Add(1) }
	if cfg.Backoff == 0 {
		cfg.Backoff = time.Millisecond
	}
	d, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(d.Close)
	return d, &changes
}

func waitFor(t *testing.T, d *Downloader, id, state string) Status {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if s := d.Status(id); s.State == state {
			return s
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("%s never reached %q, last %+v", id, state, d.Status(id))
	return Status{}
}

func TestDownloadsWhatIsWanted(t *testing.T) {
	log := filepath.Join(t.TempDir(), "args.log")
	t.Setenv("FAKE_YTDLP_LOG", log)
	d, changes := newTest(t, "ok", Config{Args: []string{"--js-runtimes", "node"}})
	d.Want([]string{"aaaaaaaaaaa"})
	waitFor(t, d, "aaaaaaaaaaa", "ready")
	path, ok := d.Path("aaaaaaaaaaa")
	if !ok || filepath.Base(path) != "aaaaaaaaaaa.mp4" {
		t.Fatalf("Path = %q, %v", path, ok)
	}
	if changes.Load() < 2 {
		t.Errorf("OnChange ran %d times, want at least queued and ready", changes.Load())
	}
	args, _ := os.ReadFile(log)
	for _, want := range []string{"--js-runtimes node -- aaaaaaaaaaa", "--merge-output-format mp4", "height<=720"} {
		if !strings.Contains(string(args), want) {
			t.Errorf("yt-dlp was run with %q, missing %q", args, want)
		}
	}
	if _, err := os.Stat(filepath.Join(filepath.Dir(path), tmpDir, "aaaaaaaaaaa")); !os.IsNotExist(err) {
		t.Error("the partial folder was left behind")
	}
}

func TestFailureKeepsYtdlpsReasonAndIsNotRetriedAtOnce(t *testing.T) {
	log := filepath.Join(t.TempDir(), "args.log")
	t.Setenv("FAKE_YTDLP_LOG", log)
	d, _ := newTest(t, "fail", Config{Attempts: 3})
	d.Want([]string{"bbbbbbbbbbb"})
	s := waitFor(t, d, "bbbbbbbbbbb", "failed")
	if !strings.Contains(s.Error, "Video unavailable") {
		t.Errorf("error = %q, want yt-dlp's reason", s.Error)
	}
	if runs, _ := os.ReadFile(log); strings.Count(string(runs), "\n") != 3 {
		t.Errorf("yt-dlp ran %d times, want 3 tries", strings.Count(string(runs), "\n"))
	}
	d.Want([]string{"bbbbbbbbbbb"})
	if got := d.Status("bbbbbbbbbbb").State; got != "failed" {
		t.Errorf("wanting it again right away gave %q, want it left failed", got)
	}
}

func TestNoFileIsAFailure(t *testing.T) {
	d, _ := newTest(t, "nofile", Config{})
	d.Want([]string{"ccccccccccc"})
	if s := waitFor(t, d, "ccccccccccc", "failed"); !strings.Contains(s.Error, "without a video file") {
		t.Errorf("error = %q", s.Error)
	}
}

func TestQueuesBeyondParallelAndDropsWhatIsNoLongerWanted(t *testing.T) {
	d, _ := newTest(t, "hang", Config{Parallel: 1})
	d.Want([]string{"running0001", "queued00001", "queued00002"})
	waitFor(t, d, "running0001", "downloading")
	if got := d.Status("queued00001").State; got != "queued" {
		t.Fatalf("second video = %q, want queued", got)
	}
	d.Want([]string{"running0001", "queued00002"})
	if got := d.Status("queued00001").State; got != "" {
		t.Errorf("a video no longer wanted is still %q", got)
	}
	if got := d.Status("queued00002").State; got != "queued" {
		t.Errorf("a video still wanted is %q", got)
	}
}

func TestTimeoutStopsADownload(t *testing.T) {
	d, _ := newTest(t, "hang", Config{Timeout: 200 * time.Millisecond, Attempts: 1})
	d.Want([]string{"slowvideo01"})
	if s := waitFor(t, d, "slowvideo01", "failed"); !strings.Contains(s.Error, "stopped") {
		t.Errorf("error = %q", s.Error)
	}
}

func TestPicksUpFilesAlreadyCachedAndClearsPartials(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "cached00001.mp4"), []byte("x"), 0o644)
	os.WriteFile(filepath.Join(dir, "notes.txt"), []byte("x"), 0o644)
	os.MkdirAll(filepath.Join(dir, tmpDir, "half0000001"), 0o755)
	d, _ := newTest(t, "ok", Config{Dir: dir})
	if got := d.Status("cached00001").State; got != "ready" {
		t.Errorf("cached file = %q, want ready", got)
	}
	if got := d.Status("notes").State; got != "" {
		t.Errorf("a non-video file counted as %q", got)
	}
	if _, err := os.Stat(filepath.Join(dir, tmpDir)); !os.IsNotExist(err) {
		t.Error("the partial folder survived New")
	}
}

func TestTrimEvictsTheOldestUnwantedFile(t *testing.T) {
	dir := t.TempDir()
	old := time.Now().Add(-time.Hour)
	for i, id := range []string{"old00000001", "old00000002"} {
		p := filepath.Join(dir, id+".mp4")
		os.WriteFile(p, []byte(strings.Repeat("o", 1000)), 0o644)
		at := old.Add(time.Duration(i) * time.Minute)
		os.Chtimes(p, at, at)
	}
	// Room for two files: fetching a third must evict the oldest that is not wanted.
	d, _ := newTest(t, "ok", Config{Dir: dir, MaxBytes: 2000})
	d.Want([]string{"old00000001", "new00000001"})
	waitFor(t, d, "new00000001", "ready")
	if d.Status("old00000002").State != "" {
		t.Error("the unwanted old file was kept")
	}
	if d.Status("old00000001").State != "ready" {
		t.Error("a wanted file was evicted")
	}
}
