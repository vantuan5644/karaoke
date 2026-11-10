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
| `-ytdlp-args` | | Extra yt-dlp arguments, e.g. `--cookies-from-browser firefox`. |
| `-max-height` | `720` | Tallest video to download. |
| `-region` | | YouTube search region, e.g. `US`, `VN`. |
| `-public-url` | | The address the QR code shows. |
| `-tls-cert`, `-tls-key` | | Serve HTTPS. |

Settings on the phone (gear icon): **Download every song** plays everything from the server's
copy (no YouTube ads; each song waits for its download), and the length of the "up next" card.

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
Writes from other websites are refused (`Sec-Fetch-Site`), and every request body is
validated. Downloads are for playing at home; respect YouTube's terms and the rights holders.

## Develop

```sh
make test     # Go tests with the race detector, plus the web UI's node tests
make check    # tests, gofmt, go vet
make dist     # binaries for every platform in dist/
```

Layout: `internal/room` (queue and its saved state), `internal/youtube` (keyless search and the
embed check), `internal/media` (yt-dlp downloads and the cache), `internal/server` (HTTP API,
live events over SSE, QR codes), `web/` (the phone remote `index.html` and the TV `tv.html`,
plain ES modules, no build step). Design notes and measurements: `docs/plan.md`.
