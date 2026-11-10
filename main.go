// Karaoke is a self-hosted karaoke server: phones search YouTube and queue songs, a TV or
// PC browser plays them. One binary, no database, no API key.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"runtime"
	"strings"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/vantuan5644/karaoke/internal/library"
	"github.com/vantuan5644/karaoke/internal/lyrics"
	"github.com/vantuan5644/karaoke/internal/media"
	"github.com/vantuan5644/karaoke/internal/room"
	"github.com/vantuan5644/karaoke/internal/server"
	"github.com/vantuan5644/karaoke/internal/voice"
	"github.com/vantuan5644/karaoke/internal/youtube"
	"github.com/vantuan5644/karaoke/web"
)

var version = "dev" // set by the release build

type options struct {
	listen    string
	dataDir   string
	cacheDir  string
	cacheGB   float64
	ytdlp     string
	ytdlpArgs string
	maxHeight int
	region    string
	publicURL string
	allowHost string
	lyrics    bool
	library   []string
	tlsCert   string
	tlsKey    string
	open      bool
	nativeMic bool
	micBuffer int
	micOutput string
}

func main() {
	opts := parseFlags()
	if err := run(opts); err != nil {
		log.Fatal(err)
	}
}

func parseFlags() options {
	var o options
	flag.StringVar(&o.listen, "listen", ":8765", "address to listen on; :8765 lets phones on the network join")
	flag.StringVar(&o.dataDir, "data", defaultDir(os.UserConfigDir), "folder for the queue and settings")
	flag.StringVar(&o.cacheDir, "cache", defaultDir(os.UserCacheDir), "folder for downloaded videos")
	flag.Float64Var(&o.cacheGB, "cache-gb", 20, "largest the download cache grows, in GB (0: no limit)")
	flag.StringVar(&o.ytdlp, "ytdlp", "yt-dlp", `yt-dlp to download videos that refuse to embed; "off" to play embedded videos only`)
	flag.StringVar(&o.ytdlpArgs, "ytdlp-args", "", `extra yt-dlp arguments, quoted like a shell (e.g. --cookies "C:\My Files\cookies.txt")`)
	flag.IntVar(&o.maxHeight, "max-height", 720, "tallest video to download, in pixels")
	flag.StringVar(&o.region, "region", "", "two-letter region for YouTube search, e.g. US or VN (default: YouTube guesses)")
	flag.StringVar(&o.publicURL, "public-url", "", "the address the TV's QR code shows, if not this machine's LAN address")
	flag.BoolVar(&o.lyrics, "lyrics", true, "look songs' lyrics up on lrclib.net for the TV's lyrics panel (-lyrics=false: never ask it)")
	flag.Func("library", "a folder of karaoke files (videos, MP3+G, audio) to search beside YouTube; repeat for more", func(dir string) error {
		o.library = append(o.library, dir)
		return nil
	})
	flag.StringVar(&o.allowHost, "allow-host", "", "more host names to answer to, comma-separated (addresses, localhost, bare machine names, .local/.lan/.home.arpa/.ts.net and -public-url's always work)")
	flag.StringVar(&o.tlsCert, "tls-cert", "", "serve HTTPS with this certificate (the TV page needs HTTPS for a mic, unless it is on localhost)")
	flag.StringVar(&o.tlsKey, "tls-key", "", "the key for -tls-cert")
	flag.BoolVar(&o.open, "open", false, "open the TV page in the default browser once the server is up")
	flag.BoolVar(&o.nativeMic, "native-mic", true, "when the TV page is on this computer, run the mic through PipeWire here: about 5 ms from mic to speaker instead of a browser's 60-70 (Linux; false: always the browser)")
	flag.IntVar(&o.micBuffer, "mic-buffer", voice.DefaultBuffer, "samples per audio cycle for -native-mic; try 256 if the voice crackles")
	flag.StringVar(&o.micOutput, "mic-output", "", "PipeWire sink for -native-mic, by node name (default: the system's default output)")
	showVersion := flag.Bool("version", false, "print the version and exit")
	flag.Parse()
	if *showVersion {
		fmt.Println(version)
		os.Exit(0)
	}
	return o
}

func defaultDir(base func() (string, error)) string {
	dir, err := base()
	if err != nil {
		dir = "."
	}
	return filepath.Join(dir, "karaoke")
}

func run(o options) error {
	if (o.tlsCert == "") != (o.tlsKey == "") {
		return errors.New("-tls-cert and -tls-key go together")
	}
	if err := os.MkdirAll(o.dataDir, 0o755); err != nil {
		return err
	}
	rm, err := room.OpenRoom(filepath.Join(o.dataDir, "state.json"))
	if err != nil {
		return fmt.Errorf("opening the saved queue: %w", err)
	}
	yt := youtube.New()
	yt.Region = strings.ToUpper(o.region)

	cfg := server.Config{Room: rm, YouTube: yt, Web: web.Files, Version: version}
	if o.lyrics {
		cfg.Lyrics = lyrics.New("karaoke/" + version + " (self-hosted)")
	}
	var lib *library.Library
	if len(o.library) > 0 {
		if lib, err = openLibrary(o.library); err != nil {
			return err
		}
		cfg.Library = lib
	}
	// Downloads can report progress before the server exists, from their own goroutines.
	var live atomic.Pointer[server.Server]
	dl, err := newDownloader(o, func() {
		if srv := live.Load(); srv != nil {
			srv.DownloadChanged()
		}
	})
	if err != nil {
		return err
	}
	if dl != nil {
		cfg.Downloader = dl
		defer dl.Close()
	}
	if engine := newVoice(o); engine != nil {
		cfg.Voice = engine
		defer engine.Close()
	}

	listener, err := net.Listen("tcp", o.listen)
	if err != nil {
		return err
	}
	port := listener.Addr().(*net.TCPAddr).Port
	secure := o.tlsCert != ""
	cfg.URLs = append(publicURLs(o.publicURL), server.LocalURLs(port, secure)...)
	cfg.AllowHosts = strings.Split(o.allowHost, ",")
	if u, err := url.Parse(o.publicURL); err == nil && u.Host != "" {
		cfg.AllowHosts = append(cfg.AllowHosts, u.Host)
	}
	srv := server.New(cfg)
	live.Store(srv)

	httpServer := &http.Server{
		Handler:           srv.Handler(),
		ReadHeaderTimeout: 15 * time.Second,
		IdleTimeout:       2 * time.Minute,
		// No write timeout: the event stream and video files are long-lived on purpose.
	}
	stop, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	if lib != nil {
		go lib.Run(stop, libraryRescan)
	}
	go func() {
		<-stop.Done()
		shutdown, done := context.WithTimeout(context.Background(), 5*time.Second)
		defer done()
		httpServer.Shutdown(shutdown)
	}()

	scheme := "http"
	if secure {
		scheme = "https"
	}
	local := fmt.Sprintf("%s://localhost:%d", scheme, port)
	log.Printf("karaoke %s: TV page %s/tv", version, local)
	for _, u := range cfg.URLs {
		log.Printf("phones: %s", u)
	}
	if o.open {
		go openBrowser(local + "/tv")
	}
	if secure {
		err = httpServer.ServeTLS(listener, o.tlsCert, o.tlsKey)
	} else {
		err = httpServer.Serve(listener)
	}
	if errors.Is(err, http.ErrServerClosed) {
		return nil
	}
	return err
}

// newVoice starts the low-latency mic engine when PipeWire is here, or says why not.
func newVoice(o options) *voice.Engine {
	if !o.nativeMic {
		return nil
	}
	opt, err := voice.Find(context.Background())
	if err != nil {
		log.Printf("mic: %v; the TV page's browser runs the mic instead", err)
		return nil
	}
	if o.micBuffer < 32 || o.micBuffer > 2048 {
		log.Printf("mic: -mic-buffer %d is outside 32..2048; using %d", o.micBuffer, voice.DefaultBuffer)
		o.micBuffer = voice.DefaultBuffer
	}
	opt.Buffer, opt.Output = o.micBuffer, o.micOutput
	log.Printf("mic: low-latency PipeWire mic ready for a TV page on this computer (%d-sample cycles)", opt.Buffer)
	return voice.New(opt)
}

func publicURLs(u string) []string {
	if u = strings.TrimRight(strings.TrimSpace(u), "/"); u != "" {
		return []string{u}
	}
	return nil
}

// newDownloader finds yt-dlp and hands it a JavaScript runtime, which YouTube's player now
// needs solved: yt-dlp looks for deno itself, so only node has to be named.
func newDownloader(o options, onChange func()) (*media.Downloader, error) {
	if o.ytdlp == "off" || o.ytdlp == "" {
		log.Printf("yt-dlp off: only videos that embed will play")
		return nil, nil
	}
	bin, err := exec.LookPath(o.ytdlp)
	if err != nil {
		log.Printf("yt-dlp not found (%v): only videos that embed will play; install yt-dlp to play the rest", err)
		return nil, nil
	}
	args, err := splitArgs(o.ytdlpArgs)
	if err != nil {
		return nil, fmt.Errorf("-ytdlp-args: %w", err)
	}
	if !strings.Contains(o.ytdlpArgs, "--js-runtimes") {
		if _, err := exec.LookPath("deno"); err != nil {
			if node, err := exec.LookPath("node"); err == nil {
				args = append(args, "--js-runtimes", "node:"+node)
			} else {
				log.Printf("neither deno nor node found: yt-dlp may fail on YouTube; install deno")
			}
		}
	}
	dl, err := media.New(media.Config{
		Bin: bin, Args: args, Dir: filepath.Join(o.cacheDir, "youtube"),
		MaxBytes: int64(o.cacheGB * (1 << 30)), MaxHeight: o.maxHeight, OnChange: onChange, Logf: log.Printf,
	})
	if err != nil {
		return nil, fmt.Errorf("download cache: %w", err)
	}
	log.Printf("yt-dlp: %s (cache %s)", bin, filepath.Join(o.cacheDir, "youtube"))
	return dl, nil
}

func openBrowser(url string) {
	time.Sleep(300 * time.Millisecond)
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		cmd = exec.Command("open", url)
	case "windows":
		cmd = exec.Command("rundll32", "url.dll,FileProtocolHandler", url)
	default:
		cmd = exec.Command("xdg-open", url)
	}
	if err := cmd.Start(); err != nil {
		log.Printf("open a browser at %s (%v)", url, err)
	}
}

// splitArgs splits a command line the way a shell would for the simple cases: spaces separate
// arguments, and single or double quotes keep spaces inside one. Backslashes are plain
// characters, so Windows paths need no doubling.
func splitArgs(s string) ([]string, error) {
	var args []string
	var cur strings.Builder
	inArg, quote := false, rune(0)
	for _, c := range s {
		switch {
		case quote != 0:
			if c == quote {
				quote = 0
			} else {
				cur.WriteRune(c)
			}
		case c == '"' || c == '\'':
			quote, inArg = c, true
		case c == ' ' || c == '\t' || c == '\n' || c == '\r':
			if inArg {
				args = append(args, cur.String())
				cur.Reset()
				inArg = false
			}
		default:
			cur.WriteRune(c)
			inArg = true
		}
	}
	if quote != 0 {
		return nil, fmt.Errorf("unclosed %c quote", quote)
	}
	if inArg {
		args = append(args, cur.String())
	}
	return args, nil
}

// libraryRescan is how often the library's folders are read again for new songs.
const libraryRescan = 2 * time.Minute

func openLibrary(dirs []string) (*library.Library, error) {
	lib, err := library.New(dirs)
	if err != nil {
		return nil, err
	}
	if p, err := exec.LookPath("ffprobe"); err == nil {
		lib.FFprobe = p
	}
	start := time.Now()
	n, err := lib.Scan()
	if err != nil {
		log.Printf("library: %v", err)
	}
	log.Printf("library: %d songs in %s (%s)", n, strings.Join(lib.Roots(), ", "), time.Since(start).Round(time.Millisecond))
	return lib, nil
}
