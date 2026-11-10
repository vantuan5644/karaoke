# Karaoke: plan and decisions

Started 2026-11-10. Goal (user): "a karaoke app, web-based preferred, most basic function is
having a youtube search, other karaoke source will be nice to have." The same day: "build this
as a deployable app, runnable on any machine", in its own private repo.

## Why build one

The prior-art search (web and GitHub, 2026-11-10) turned up working apps. The user chose to
build our own after seeing them:

| App | YouTube | Other sources | Notes |
| --- | --- | --- | --- |
| PiKaraoke (vicwomg, 931 stars, 1.23.0 on 2026-08-28) | yt-dlp, downloads before playing | local files | Python, QR remote, key change, Docker image |
| Karaoke Eternal (bhj, 926 stars, v2.1.0 on 2026-11-04) | none (provider API is new, no YouTube provider) | MP3+G, MP4 library | ISC, most polished queue |
| pwa-karaoke (nedpals, 14 stars) | yt-dlp | none | FastAPI + React, rooms, synced displays |
| Syng (christofsteel, 47 stars) | yes | S3, files | AGPL, plays through an mpv desktop client |
| kbox (mdz, 3 stars) | Data API key + IFrame | none | FastAPI |

## Web-based, with a server

The UI is all web: the phone remote and the TV player are pages, and nothing is installed on
either. A small server sits behind them because a page alone cannot do the core job:
YouTube's search is not callable from another site's page, the Data API needs a key that a page
would expose (and its quota is about 100 searches a day), 40% of the best karaoke videos only
play from a download, and the phones and the TV need something to share the queue through. A
public cloud host would fare worse: YouTube bot-checks yt-dlp from datacenter addresses, and
serving downloads publicly is a copyright problem. So: one binary (or container) on any
computer at home.

## What was measured before writing code

- **Keyless YouTube search works.** InnerTube `youtubei/v1/search` (the `WEB` client, filter
  `EgIQAQ%3D%3D` = videos) answered 20 `videoRenderer` results in 0.8 s, Vietnamese queries
  included, with a continuation token for more.
- **Many top karaoke videos refuse to embed.** In a real Chromium with the IFrame API, 3 of
  the top 7 "bohemian rhapsody karaoke" results failed with error 150: Sing King, Sing2Piano
  and karaoke SESH.
- **oEmbed predicts it, mostly.** `https://www.youtube.com/oembed?url=...` answered 401 for
  exactly those three and 200 for the eight that played (11 of 11). Later the same day a video
  oEmbed still called embeddable (Starlight Karaoke, `xyF04rAhHaQ`) failed with 150 in one
  session and played in another, which is why the TV reports a refused embed and the server
  then downloads it. The `player` endpoint is no use for this: without proof-of-origin tokens
  it answers UNPLAYABLE for everything.
- **yt-dlp covers the rest.** A 6:15 Sing King video came down as 720p H.264/AAC MP4 (8.7 MB)
  in 2.3 s. Once, through the server, the same video failed with HTTP 403 and then worked by
  hand, so downloads are tried three times. yt-dlp needs a JavaScript runtime for YouTube:
  deno by default, or `--js-runtimes node`.

## Design

- **One room.** In-memory state (now playing, upcoming, history, settings), written to
  `state.json` on every change and pushed to every page over Server-Sent Events.
- **Playback per entry is decided on the server** (`internal/server/snapshot.go`): a
  downloaded copy first, then the embedded player, else wait for the download.
- **Advancing is idempotent.** "Next" names the entry it is leaving, so two TVs, or a TV and a
  phone pressing skip at once, advance once.
- **Phone input is rebuilt, not trusted**: video IDs are checked, the thumbnail is rebuilt from
  the ID, the embed check is redone, text is trimmed and capped, and cross-site writes are
  refused.

## Voice and audio

The user asked for all four, PC with a USB mic first (2026-11-10):

1. **Mic in the TV page** (done, `web/static/mic.js`). `getUserMedia` with echo cancellation, noise suppression and auto
   gain off (they are tuned for calls and treat a held note as noise; Jeff Kaufman measured
   Chrome going from about 67 ms to about 19 ms end to end with them off and `latency: 0`).
   The chain: high-pass 80 to 100 Hz, compressor about 3:1, EQ (cut 200 to 500 Hz, lift 3 to
   5 kHz), then the karaoke echo (100 to 250 ms, feedback well below 1, high-passed around
   250 Hz) and a reverb (0.6 to 1.4 s, 20 to 50 ms pre-delay), and a limiter. Mic and music
   have their own levels. The page shows its measured latency; under 20 to 25 ms feels
   natural. On Linux, PipeWire's quantum adds to it (1024 samples = 21 ms by default). The
   mic needs a secure context: `localhost`, or HTTPS.

   What was tested: in Chromium the track came up with all three call features off and 10 ms
   of reported input latency. With WAV files as the mic, a sung note with vibrato that
   changes every 1.5 s never tripped the feedback guard, and a 0.9-amplitude 1870 Hz tone
   tripped it after one second. The first guard (60% of the energy in one peak for 350 ms)
   would have cut singers' held notes, so it now waits for a loud peak (level 0.5, 70% of the
   energy) that stays in one FFT bin for a full second, then cuts 40%, at most every 3 s and
   never below 25%. The delay probe is unit-tested against a simulated room (8 to 130 ms
   read back within 1 ms). On omarchy the USB PnP mic is the default source, mono at
   48 kHz, the same rate as the page's AudioContext, so nothing is resampled.
2. **Key change** (done) for downloaded songs, in the TV page: signalsmith-stretch 1.3.2
   (MIT, official WASM/AudioWorklet build), vendored in `web/static/vendor`. The library
   loads its worklet from a `blob:` URL, which the page's CSP refuses, so
   `tools/signalsmith-worklet.mjs` saves that module as a file and a node test checks the two
   still agree. The key is per queue entry (`Entry.Key`, -6 to +6); a non-zero key makes the
   server download the song and plays the copy, and the TV switches from the embed to the copy
   at the same position. The shifter sits between a `MediaElementSource` and the speakers only
   while the key is off zero. Formant compensation is left off: most karaoke tracks are
   instrumental, and it is a voice correction.

   What was tested: in Chromium, under the page's CSP, a 440 Hz tone shifted +2 semitones
   peaked at 495.1 Hz (493.9 expected; one bin of a 32768-point FFT is 1.5 Hz), with 120 ms of
   reported latency. On a real song playing embedded, +2 from the phone switched the TV to the
   download from the same position; +1 then applied live without a restart, and 0 bypassed
   the shifter.
3. **Vocal removal** for downloaded songs, on the server: a RoFormer model (BS-RoFormer or
   Mel-Band RoFormer lead for vocals) through python-audio-separator (MIT), on a GPU when
   there is one. Upstream Demucs was archived.
4. **Hardware mics** stay the zero-latency option: a mixer or karaoke speaker, with the
   computer's audio into its line-in.
5. **The mic outside the browser** (done, `internal/voice`). Also on 2026-11-10: "my voice is
   late". Measured electronically on omarchy (Chromium's output into a silent PipeWire null
   sink, its mic on that sink's monitor, the page's own delay probe): 69 to 73 ms round trip
   with the page's `latencyHint: 'interactive'`, 67 to 68 at `latencyHint: 0`, 61 to 65 with
   `PULSE_LATENCY_MSEC=5` as well. Chromium's output reports 30 to 38 ms of latency on its
   own; a page cannot get under about 60 ms. A plain `pw-record | pw-play` loop at 128 samples
   was 22.7 ms with stdio buffering, 5.3 ms with `stdbuf -o0`/`-i0`; `pw-loopback`, 2.7 ms.
   So when the TV page is on the server's computer (loopback, or one of its own addresses,
   and no proxy header), the server runs the mic: `pw-record` (mono f32), the chain in Go,
   `pw-play` (stereo). With the two streams in different driver groups (the mic's and the
   speakers'), `pw-play` missed nearly every cycle (546 errors in 1.5 s); the same
   `node.group` puts them under one driver, and then 0 errors in 10 s. A chunk is dropped
   when more than a cycle and a half waits in `pw-play`'s pipe for four writes running
   (FIONREAD), so a stall cannot leave the voice late for good. Through a virtual mic
   looped back to itself, the engine's click came round every 5.3 ms, and its own delay
   probe measured 5 ms where Chromium, through the same loop, measured 51 to 57. The whole
   chain with every knob on takes about 28 ms of CPU per second of audio (3 of them for
   flushing numbers too small to hear to zero: a review found that a minute of silence left
   the reverb full of subnormals, 40% slower to process on a Ryzen, worse on Intel). An unattended mic (no
   request from the TV page for 20 s) turns itself off.
6. **Voice tuning** (done), both ways the user asked for: tone (Warmth: a 220 Hz low shelf
   up to +6 dB; Bright: a 5 kHz high shelf up to +8 dB; Double: two copies 14 and 23 ms late,
   drifting 2.5 to 3 ms at 0.31 and 0.47 Hz, one per side) and Tune, pitch correction: YIN at
   16 kHz every 5 ms (70 to 800 Hz, threshold 0.15), then TD-PSOLA with Hann grains two periods
   long, read 8 ms behind the voice and lined up a whole period apart, so the voice keeps its
   formants. The knob sets how much of the way to the nearest semitone (all of it from 60)
   and how fast (0.2 s at 20, 5 ms at 100); a note is held until the singer is 0.65 semitone
   from it. It is in Go for the native mic and in an AudioWorklet (`tune-worklet.js`) for the
   browser, with the same tests: 452 Hz and 430 Hz come out within 4 cents of 440 at 100, 200
   Hz goes to 196, the level stays within 1 dB, and noise passes through only delayed. The
   Go chain matches the browser's loudness: Chrome's compressor adds makeup gain (0.6 of
   what it takes off at full scale, 9.6 dB here), so the Go one does too, and both feedback
   guards now listen before the compressor, which held a howl under their level threshold.

## Lyrics panel (done)

Asked for on 2026-11-10: "allow matching lyrics, as a side panel, only lyric, no need time
syncing, sometimes its better for singing." `internal/lyrics` matches a video title to an LRCLIB
record; the TV shows it in a right-hand panel (`web/static/tv-lyrics.js`), the phone in a tab
with a search to pick another record (`web/static/remote-lyrics.js`). The pick is stored on the
queue entry (`Song.LyricsID`: an LRCLIB id, 0 automatic, -1 none), so every screen follows it.

Measured: LRCLIB's search returns full plain lyrics in each result (one request per song), finds
Vietnamese songs from unaccented queries, and needs every word to match (one stray channel word
took 20 hits to 3 or 0), so the matcher retries with the title's parts and with words dropped
from its end. It answered 429 to about 150 requests in 5 s, so the client spaces requests
250 ms apart and honours Retry-After. On 84 real karaoke titles from our own search (14
songs, 5 Vietnamese) it matched the right song 84 times; some LRCLIB records are typed without
Vietnamese accents, so accented titles prefer accented lyrics. Fitting at 1080p: Bohemian
Rhapsody (50 lines) fits whole in two columns at half the screen; Shape of You (92 lines) and
Vietnamese songs, whose long lines wrap, fall back to one column that scrolls, with a fade at
the bottom while there is more.

Later on 2026-11-10: "Karaoke Anh Đau Từ Khúc Em Đi" found no lyrics, and the phone's search box
offered "anh dau tu khuc em di quoc thien nhac tre". Of 112 Vietnamese titles from our search,
95 matched; the misses were the uploader's misspelling (Khúc for Lúc: LRCLIB needs every word,
so nothing came back), channel tags after "Karaoke" left joined to the song's name ("... Karaoke
Tone Nữ Beat Live Band Dễ Hát"), and "Karaoke Sóng Gió" losing "Sóng" to the tag "karaoke
song". Now the title splits at each tag as at a separator, a round of looser queries (each part
with one word left out, or shortened) runs only when the plain ones find nothing, and the search
box starts from the cleaned title as written ("Anh Đau Từ Khúc Em Đi - Quốc Thiên"); a search
by hand that finds nothing is loosened the same way. The 12 misses joined the live test: 96 of
96, at 147 LRCLIB requests against 151 before.

## Other sources (after that)

- *Library* (done): `-library DIR`. `internal/library` walks the folders (skipping hidden and
  NAS thumbnail folders), pairs audio with a `.cdg` of the same name as MP3+G, else takes a
  video, else plain audio; reads `Artist - Title` from the name, dropping a disc code in front
  only when an artist and title follow it (so "Prince - 1999" keeps its title); and searches
  folded words by prefix. IDs are a hash of folder and path, so a saved queue finds its songs
  after a restart. Files are served by ID only (`/media/lib/{id}`, `/cdg`), never by a path
  from the request. The TV draws CD+G with cdgraphics 7.0.0 (vendored; the renderer Karaoke
  Eternal uses) rather than our own decoder: 7 KB, no dependencies, and it re-renders from the
  start on a backward seek. Not done: zipped MP3+G, and watching the folders (a rescan every
  2 minutes instead).

  What was tested: a generated CD+G (blue, then a red block drawn over 1 to 2 s, then green
  over 3 to 4 s) read back from the TV's canvas as blue at 0.3 s, red at 2.4 s, green after a
  seek to 4.5 s, red again after a seek back to 2.5 s; a key change delayed the graphics by the
  shifter's 120 ms. A video in a folder named "Việt" was found by "nang am" and played at its
  size; plain audio showed its card.
- *Lyrics songs*: any song with lyrics on LRCLIB, played over a YouTube backing track of
  matching length, or over the song itself with the vocals removed.
