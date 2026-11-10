# Karaoke

A self-hosted karaoke night in a browser. Run one small program on a computer, open its TV
page on the screen everyone watches, and guests scan the QR code to search YouTube and queue
songs from their phones. Nothing to install on phones or the TV, no account, no API key.

- **YouTube search** without an API key, with "karaoke" added to the query so sing-along
  versions come first.
- **Videos that refuse to embed still play.** About 40% of the top karaoke results (Sing King,
  for one) block YouTube's embedded player. The server spots them before they are queued and
  downloads them with [yt-dlp](https://github.com/yt-dlp/yt-dlp) while they wait their turn.
- **One shared queue**, live on every phone: add under your name, sing next, reorder, remove,
  skip, pause, volume.
- **The TV page** shows an "up next" card with the singer's name between songs, the song full
  screen, and who is next near the end.
- **Lyrics beside the video**, plain text with no timing, found on [LRCLIB](https://lrclib.net)
  from the video's title (Vietnamese included), with a pick-your-own search on the phone when
  the match is wrong.
- **One binary** for Linux, macOS and Windows, with the web UI inside it, or a Docker image.

## Run it

Download the binary for your system from the releases page, or build it (Go 1.23+):

```sh
go build -o karaoke .
./karaoke -open        # starts on :8765 and opens the TV page
```

Then:

1. On the computer connected to the TV, open `http://localhost:8765/tv` and click **Start**
   (browsers only play sound after a click). Press **F** for full screen.
2. Guests scan the QR code on the TV, or open the address it shows, e.g.
   `http://192.168.1.20:8765`, on a phone on the same network.

For videos that refuse to embed, install **yt-dlp**, **ffmpeg** and **deno** (yt-dlp needs a
JavaScript runtime for YouTube; node works too). Without them the app still runs and plays
every video that embeds; the rest show "Can't play here".

```sh
# Arch:   pacman -S yt-dlp ffmpeg deno
# macOS:  brew install yt-dlp ffmpeg deno
# Windows: winget install yt-dlp.yt-dlp Gyan.FFmpeg DenoLand.Deno
```

### Docker

```sh
docker compose up -d       # or: docker build -t karaoke . && docker run -p 8765:8765 -v karaoke:/data karaoke
```

The image brings yt-dlp, deno and ffmpeg. Because the container can't see your LAN address,
set it for the QR code with `-public-url http://192.168.1.20:8765` (see `compose.yaml`).

## Options

| Flag | Default | |
| --- | --- | --- |
| `-listen` | `:8765` | Address to listen on. `127.0.0.1:8765` keeps it to this computer. |
| `-open` | off | Open the TV page in the default browser on start. |
| `-data` | user config dir | Where the queue and settings are saved (`state.json`). |
| `-cache` | user cache dir | Where downloaded videos go. |
| `-cache-gb` | `20` | Cache size; the least recently played videos go first. |
| `-ytdlp` | `yt-dlp` | Path to yt-dlp, or `off`. |
| `-ytdlp-args` | | Extra yt-dlp arguments, quoted like a shell: `--cookies "C:\My Files\cookies.txt"`. |
| `-max-height` | `720` | Tallest video to download. |
| `-region` | | YouTube search region, e.g. `US`, `VN`. |
| `-lyrics` | `true` | Look lyrics up on lrclib.net; `-lyrics=false` never asks it. |
| `-public-url` | | The address the QR code shows. Its host name is also allowed (below). |
| `-allow-host` | | More host names to answer to, comma-separated, e.g. `karaoke.example.com`. |
| `-tls-cert`, `-tls-key` | | Serve HTTPS. |

Settings on the phone (gear icon): **Download every song** plays everything from the server's
copy (no YouTube ads; each song waits for its download), and the length of the "up next" card.

## Singing through the computer

Plug a USB mic into the computer that shows the TV page, click the mic button at the bottom
left (or press **V**), and switch it on. The page runs the voice through the chain a karaoke
machine uses: a high-pass filter for rumble and pops, a little EQ (less boom, more presence),
a gentle compressor, the karaoke echo, a reverb, and a limiter. **Echo** and **Reverb** are
the two knobs that matter, with *Dry*, *KTV* and *Hall* as starting points. Phones get a mic
card under the song for turning it on, setting levels and picking a preset mid-song.

- **The mic only works on `localhost` or HTTPS**, a browser rule. Opening the TV page on the
  server's own computer (`http://localhost:8765/tv`) is fine. For a TV browser on another
  machine, serve HTTPS (`-tls-cert`/`-tls-key`, or `tailscale serve`).
- **Delay.** You hear yourself a little late, because the sound goes through the computer.
  **Measure delay** plays three clicks through the speakers and times their return through
  the mic. Under about 25 ms feels natural. On Linux, PipeWire's buffer adds to it; if the
  figure is high, try `pw-metadata -n settings 0 clock.force-quantum 256` (and `0` to undo).
- **Feedback.** Headphones, or a mic pointed away from the speakers, prevent it. If it starts
  to howl (a loud tone that holds one pitch for a second), the page turns the mic down.
- The browser's call processing (echo cancellation, noise suppression, auto gain) is off: it
  treats a held note as noise and pumps the level.
- For no delay at all, use a mixer or a karaoke speaker with the mics plugged into it, and the
  computer's sound into its line-in.

## Lyrics

Press **L** on the TV (or the *On TV* switch in the phone's **Lyrics** tab) for a panel with
the song's words beside the video. They are plain text with no timing, sized so the whole song
fits when it can (one column, then two, then a wider panel); a longer song scrolls with
**Page Up/Down** or the phone's arrows. The phone's Lyrics tab shows the same words.

They come from LRCLIB, a free, open lyrics database. The server takes the karaoke dressing off
the video's title ("Bohemian Rhapsody - Queen | Karaoke Version | KaraFun" becomes "bohemian
rhapsody queen"), searches, and picks the record whose name the title contains, preferring the
song over remixes, a length close to the video's, and accented lyrics for an accented title.
On 84 real karaoke titles (English and Vietnamese) it picked the right song every time. When it
is wrong, **Wrong lyrics?** on the phone searches LRCLIB by hand; *No lyrics* turns them off
for that song. With `-lyrics=false` the server never contacts LRCLIB.

## How playback is chosen

For each song the server decides, and the TV follows:

1. A downloaded copy, if there is one: it starts at once and has no ads.
2. Otherwise YouTube's embedded player, if the video allows it. Before a song is queued the
   server asks YouTube's oEmbed endpoint, which answers 401 for videos that block embedding.
3. Otherwise the TV waits while yt-dlp downloads it (720p MP4, usually a few seconds). If the
   embedded player refuses a video anyway (error 101/150), the TV asks for the download.

## Network and privacy

The server has no accounts: anyone who can reach it can queue and skip, which is the point at
a party. Keep it on your home network (or a VPN such as Tailscale), not the open internet.
Other websites' pages can't use it either: writes must be JSON (which a cross-site page can't
send without permission the server never gives), no page can frame it, and requests that name
a host the server doesn't know are refused, which stops DNS rebinding. Addresses, `localhost`,
bare machine names and home or VPN names (`.local`, `.lan`, `.home.arpa`, `.ts.net`) always
work; add any other name with `-allow-host`. Every request body is validated. Downloads are for playing at home; respect YouTube's terms and the rights holders.

## Develop

```sh
make test     # Go tests with the race detector, plus the web UI's node tests
make check    # tests, gofmt, go vet
make dist     # binaries for every platform in dist/
```

Layout: `internal/room` (queue and its saved state), `internal/youtube` (keyless search and the
embed check), `internal/media` (yt-dlp downloads and the cache), `internal/lyrics` (LRCLIB and
matching titles to songs; `LRCLIB_LIVE=1 go test ./internal/lyrics` checks the real titles), `internal/server` (HTTP API,
live events over SSE, QR codes), `web/` (the phone remote `index.html` and the TV `tv.html`,
plain ES modules, no build step). Design notes and measurements: `docs/plan.md`.
