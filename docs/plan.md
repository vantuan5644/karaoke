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

## Other sources (after that)

- *Library*: a folder of karaoke files (MP4/WebM, MP3+CDG pairs), searched accent-insensitively
  ("nang am" finds "Nắng Ấm"); CDG drawn on a canvas by our own decoder.
- *Lyrics songs*: any song with lyrics on LRCLIB, played over a YouTube backing track of
  matching length, or over the song itself with the vocals removed.
