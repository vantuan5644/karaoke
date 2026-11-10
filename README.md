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
- **Your own karaoke files** beside YouTube: point it at a folder of videos or MP3+G discs
  and the phone's search finds them first, accents or not.
- **Key change**, up or down six semitones from the phone, at the same tempo.
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
| `-library` | | A folder of karaoke files to search beside YouTube; repeat for more. |
| `-lyrics` | `true` | Look lyrics up on lrclib.net; `-lyrics=false` never asks it. |
| `-public-url` | | The address the QR code shows. Its host name is also allowed (below). |
| `-allow-host` | | More host names to answer to, comma-separated, e.g. `karaoke.example.com`. |
| `-tls-cert`, `-tls-key` | | Serve HTTPS. |
| `-native-mic` | `true` | Run the mic through PipeWire on this computer when the TV page is here too (Linux). `false`: always in the browser. |
| `-mic-buffer` | `128` | Samples per audio cycle for the native mic (2.7 ms). Try `256` if the voice crackles. |
| `-mic-output` | | PipeWire sink for the native mic, by node name (default: the system's default output). |

Settings on the phone (gear icon): **Download every song** plays everything from the server's
copy (no YouTube ads; each song waits for its download), and the length of the "up next" card.

## One computer, no phone

Open only the TV page and press **/** (or the search button in the corner) to search on the
TV itself. Type, press **Enter** to search and **↓** to pick, then **Enter** adds the song
(**Shift+Enter** sings it next), under the name in the Singer box. **→** moves to the queue,
where **Delete** removes a song and **Shift+↑/↓** moves it. **Esc** closes the search. Start the
server with `-listen 127.0.0.1:8765` to keep it on this computer only.

## Singing through the computer

Plug a USB mic into the computer that shows the TV page, click the mic button at the bottom
left (or press **V**), and switch it on. The voice goes through the chain a karaoke machine
uses: a high-pass filter for rumble and pops, a little EQ (less boom, more presence), a gentle
compressor, the karaoke echo, a reverb, and a limiter. **Echo** and **Reverb** are the two
knobs that matter, with *Dry*, *KTV* and *Hall* as starting points. Phones get a mic card
under the song for turning it on, setting levels and picking a preset mid-song.

**Voice** has four more knobs, all off to start:

- **Warmth** adds body in the low mids (a shelf at 220 Hz, up to +6 dB).
- **Bright** adds presence and air (a shelf at 5 kHz, up to +8 dB), for a voice that sounds dull.
- **Double** adds a second voice beside the first, two copies 14 and 23 ms late that drift a
  little in time, one to each side: a fuller sound, like a singer and their double take.
- **Tune** is pitch correction ("auto-tune"). It finds the note you sing and pulls it toward the
  nearest semitone. Around 40 to 60 it sounds natural: a note sung a little flat or sharp comes
  out on pitch, and slides and vibrato survive. At 100 it snaps at once, the robotic effect.
  While it is on, your voice comes back 8 ms later.

### Low latency: the mic outside the browser

A browser is slow at this: from the mic, through the page, to the speakers takes 60 to 70 ms
on Linux, late enough to hear as an echo of yourself. So when the TV page is open on the
server's own computer and the server finds PipeWire (most Linux desktops), the server runs the
mic itself: `pw-record` from the mic, the same chain in Go, `pw-play` to the speakers, about
5 ms in all. The panel says which it is using, and **Low latency** switches back to the browser.
Measured on one desktop with a virtual mic looped back to itself: 5 ms through the server,
51 to 57 ms through Chromium. The mic and speakers add their own few milliseconds to both.

On a Mac, Windows, or a TV browser on another machine, the browser runs the mic as before. A
mic left on by a closed TV page turns itself off within 20 s, and only a page on the server's
own computer can reach this mic (phones change it through the TV page).

- **The browser's mic only works on `localhost` or HTTPS**, a browser rule. Opening the TV page
  on the server's own computer (`http://localhost:8765/tv`) is fine. For a TV browser on
  another machine, serve HTTPS (`-tls-cert`/`-tls-key`, or `tailscale serve`).
- **Delay.** **Measure delay** plays three clicks through the speakers and times their return
  through the mic. Under about 25 ms feels natural. In the browser on Linux, PipeWire's buffer
  adds to it; try `pw-metadata -n settings 0 clock.force-quantum 256` (and `0` to undo).
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
When nothing fits, it tries the title with one word left out, so an uploader's misspelling
("Anh Đau Từ Khúc Em Đi" for "Anh Đau Từ Lúc Em Đi") still finds the song. On 96 real karaoke
titles (English and Vietnamese) it picked the right song every time. When it is wrong,
**Wrong lyrics?** on the phone searches LRCLIB by hand, starting from the cleaned title with
its accents; a search that finds nothing is loosened the same way. *No lyrics* turns them off
for that song. With `-lyrics=false` the server never contacts LRCLIB.

## Your own karaoke files

```sh
karaoke -library ~/Karaoke            # repeat -library for more folders
```

The server reads the folder and its subfolders for:

- **MP3+G**: an audio file (`.mp3`, `.m4a`, `.ogg`, `.flac`, `.wav`...) beside a `.cdg` of
  the same name, as karaoke discs are ripped. The TV draws the graphics in step with the
  music.
- **Videos**: `.mp4`, `.webm`, `.m4v`, and `.mkv` or `.mov` when what's inside is H.264, VP9
  or AV1 (what a browser can play).
- **Audio** on its own: a backing track, shown as a card with its name (the lyrics panel
  helps here).

Names are read as `Artist - Title`, with a disc code in front (`SC8125-01 - Queen - Bohemian
Rhapsody`) dropped. Searching matches the start of each word, without accents ("nang am"
finds "Nắng Ấm Xa Dần"), across artist, title and folder names, and library hits show above
YouTube's on the phone. The folders are read again every two minutes, so new files show up
without a restart. Zipped MP3+G (`.zip`) is not read yet: unzip it first. With `ffprobe`
(part of ffmpeg) installed, a song's length is measured when it is queued. Library files never
leave the machine: the TV plays them from the server.

## Key change

The **♭/♯** buttons on the phone's now-playing card (or **[** and **]** on the TV) move the
song up or down a semitone, as far as six either way. The TV shifts the sound itself with
[Signalsmith Stretch](https://signalsmith-audio.co.uk/code/stretch/) in an AudioWorklet, so the
tempo stays the same. Library files change key at once. A YouTube song needs its downloaded copy, because YouTube's embedded player
keeps its sound to itself: a song playing embedded keeps going in the original key while the
copy downloads (a few seconds), then carries on from the same spot in the new key. In the new
key the sound trails the picture by about 0.12 s, the shifter's processing time; in the
original key it bypasses the shifter. The key belongs to that turn in the queue, so the next
singer starts in the original key. For a YouTube song, downloads must be on (`yt-dlp` found).

## How playback is chosen

For each song the server decides, and the TV follows:

1. A downloaded copy, if there is one: it starts at once and has no ads. A song in another key
   always waits for one.
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
embed check), `internal/media` (yt-dlp downloads and the cache), `internal/library` (scanning and searching folders), `internal/lyrics` (LRCLIB and
matching titles to songs; `LRCLIB_LIVE=1 go test ./internal/lyrics` checks the real titles),
`internal/voice` (the native mic: the vocal chain, pitch correction and the PipeWire pipeline),
`web/static/vendor` (Signalsmith Stretch and cdgraphics; see `THIRD_PARTY.md`), `internal/server` (HTTP API,
live events over SSE, QR codes), `web/` (the phone remote `index.html` and the TV `tv.html`,
plain ES modules, no build step). Design notes and measurements: `docs/plan.md`.
