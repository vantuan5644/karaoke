# Architecture

Karaoke serves one shared room from a Go HTTP server. The phone remote and TV player
are plain HTML, CSS and ES modules embedded into the executable. No database, account,
YouTube API key or frontend build is required.

## State and playback

- `internal/room` owns the queue, history and settings under a mutex, saving changes to
  `state.json`. Advancing names the queue entry being left, so duplicate completion or
  skip requests advance once.
- `internal/server` wires HTTP routes, validates input and publishes snapshots through
  Server-Sent Events. A publishing lock keeps snapshots ordered; a connecting client
  subscribes before its initial snapshot. Slow clients cannot block publication.
- `internal/youtube` performs keyless search and checks oEmbed availability. YouTube can
  change either service or reject a video at playback time, so the TV can request a
  download after an embed fails.
- `internal/media` owns yt-dlp jobs, cancellation, retries and the download cache.
  Queued and playing videos are retained during cache trimming. A cancelled job ends
  its process tree, including download subprocesses.
- `internal/library` scans configured folders, pairs audio with matching CD+G graphics,
  and serves files by generated IDs. Requests cannot supply arbitrary filesystem paths.
  Scans repeat every two minutes. `internal/fold` supports accent-insensitive searching.

Playback prefers an available downloaded copy, otherwise an embeddable YouTube player,
otherwise a pending download. A song already playing keeps its player unless a requested
key change needs the downloaded copy. Queue state survives a restart; playback position
is reported by the TV.

## Audio and lyrics

The TV and phone share microphone settings. On a Linux desktop, a local TV client can
use the native PipeWire engine in `internal/voice`: mono float samples from `pw-record`,
the Go vocal chain, then stereo samples to `pw-play`. Other platforms and remote TV
clients use Web Audio. An unattended native mic shuts down after 20 seconds.

Both vocal chains apply input boost, high-pass filtering, EQ, compression, echo, reverb,
doubling, optional pitch correction and limiting. The feedback detector distinguishes
a lone persistent tone from a voice with octave harmonics. Pitch correction uses YIN
and TD-PSOLA with an 8 ms lookback. Tune controls correction amount; Speed controls
glide time. Allowed notes are a 12-bit mask, optionally supplied by the song's key.

`internal/musickey` uses ffmpeg to decode up to six minutes of audio, estimates chroma
with harmonic reduction and compares major/minor key profiles. It caches by file path
and size, with one analysis at a time and cancellable waiting. A whole-song estimate
cannot follow key changes within the song. YouTube key analysis requires a download.

Signalsmith Stretch changes a backing track's key at the same tempo. Because its
AudioWorklet adds latency, `tv-delay.js` holds video frames back by the shifter and
AudioContext latency; CD+G rendering applies the same delay. The generated worklet is
served as a file to comply with the page's Content-Security-Policy. Its regeneration
command and upstream notices are in [THIRD_PARTY.md](../THIRD_PARTY.md).

`internal/lyrics` matches karaoke video titles to LRCLIB records, removing channel and
karaoke labels, folding accents, and comparing track, artist and duration. It limits
request frequency, respects retry delays and can make looser searches after a miss.
The phone can choose another record or disable lyrics for an entry. `-lyrics=false`
disables LRCLIB requests.

## Network boundaries and data

The HTTP server validates host names, bounds request bodies, requires JSON writes and
refuses cross-site changes. Pages cannot be framed. The native mic checks local-client
addresses and proxy headers. These controls support the trusted-network deployment
described in [SECURITY.md](../SECURITY.md).

The server contacts YouTube for search and embedding checks, LRCLIB for enabled lyric
lookups, and media providers through yt-dlp for downloads. The TV's embedded player and
thumbnails also contact YouTube. Queue entries, singer names and settings are saved
locally; downloaded media stays in the configured cache. Library files are streamed to
connected players. Browser microphone audio is processed in the TV browser; native mic
audio is processed by the server's local audio pipeline.

## Verification and limits

`make check` runs race-enabled Go tests, browser module tests, formatting and vet checks.
Fixtures and fake services keep the default tests independent of public providers.
LRCLIB matching has a separate opt-in live test. Audio tests use synthetic notes, noise,
vibrato and feedback tones; browser tests cover settings, worklets and key coordination.

Development measurements on a Linux desktop found about 5 ms through a virtual native
mic loop and roughly 51-73 ms through Chromium. These are observations from one setup;
real microphones, speakers, OS buffers and browsers change the result. Hardware latency,
video synchronization and provider compatibility need real-device checks.

Local libraries support uncompressed MP3+G, browser-compatible video and plain audio.
Zipped MP3+G, automatic vocal separation and multiple independent rooms are not implemented.
