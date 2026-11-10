# Setting up your voice

The mic panel on the TV (the mic button, or **V**) and the mic card on a phone have the same
knobs. Set them in this order, one at a time, while you sing a line you know well.

**1. Level first: Mic boost.** If the meter barely moves when you sing, raise Boost until your
loudest notes land around two thirds of the meter, never at the far right. The tone knobs and
auto-tune work better on a healthy level. Boost raises the voice going in; **Mic volume** sets
how loud it comes out of the speakers.

**2. Echo and Reverb.** *Dry*, *KTV* and *Hall* are the starting points: Dry is almost none,
KTV the karaoke-machine echo (Echo 55, Reverb 25), Hall a big room (Echo 15, Reverb 60).

**3. Tone: Warmth and Bright.**
- **Warmth** boosts the low mids, up to +6 dB at 100. It's for a voice that sounds thin or
  small. Start around 30 to 50. Too much sounds boomy, especially with reverb, so leave it at
  0 if you hold the mic very close.
- **Bright** adds presence and air, up to +8 dB at 100. It's for a dull or muffled voice,
  which cheap USB mics often give. Start around 20 to 40. Too much makes "s" sounds harsh.

**4. Double.** It adds two slightly late, drifting copies of your voice, one on each side,
which sounds fuller, like a double-tracked recording. Use 20 to 40 for a natural thickening.
Above about 60 it starts to sound like a chorus effect.

**5. Auto-tune.**
- **Natural** (Tune 70, Speed 35): pulls held notes most of the way onto pitch and keeps
  slides and vibrato. Good for ballads.
- **Strong** (Tune 100, Speed 65): every held note lands exactly on pitch, and slides move in
  steps, but vibrato survives. Obvious, polished, pop-style correction.
- **Robot** (Tune 100, Speed 100): jumps from note to note at once and irons the vibrato out,
  the effect sound.
- To set it by hand, **Tune** is how far notes are pulled (0 is off) and **Speed** how fast.
  If Natural sounds too loose, raise Speed; if Strong sounds robotic, lower it.
- **Notes:** *The song's key* is the best choice for most songs: a note you sing off goes to
  the nearest note in the key, never to one outside it. Use *Every semitone* for songs that
  change key part way, since one key is used for the whole song.

Auto-tune is hard to hear on your own voice, because you also hear yourself directly, inside
your head and in the room. To check it, put Robot on and sing a slow slide from low to high,
like a siren: it should come out as clear steps, not a smooth slide. Headphones make the
difference much easier to hear.

**Starting points:**

| Situation | Warmth | Bright | Double | Auto-tune |
|---|---|---|---|---|
| Thin or quiet voice | 40 | 20 | 20 | Natural |
| Dull, cheap mic | 10 | 50 | 20 | Natural |
| Ballad | 30 | 20 | 25 | Natural, song's key, Hall |
| Upbeat pop | 20 | 30 | 35 | Strong, song's key, KTV |
| Just for fun | — | — | — | Robot |

## How it works

**The chain.** Your voice goes through Boost, a high-pass filter for rumble and pops, the
pitch correction, the tone (Warmth, a little less boom, a little more presence, Bright), a
gentle compressor, Mic volume, Double, the echo and reverb, and a limiter. With *Low latency*
on, it all runs on the server through PipeWire, about 5 ms from mic to speaker; in the
browser it is 50 to 70 ms.

**The tone knobs** are fixed effects. Warmth and Bright are tone controls: Warmth lifts
around 220 Hz for body, and Bright lifts above 5 kHz for presence. Double mixes in two copies
of your voice, 14 and 23 ms late, each drifting slightly in time, one to each side. That
makes it sound like two takes sung together.

**Tune (pitch correction).** Every 5 ms it detects the note you're singing in the last 16 ms
of your voice, using a method called YIN. It then works out how far you are from the nearest
allowed note and re-pitches your voice toward it. Re-pitching works by cutting the voice into
short slices, two vibrations long, and laying them closer together or further apart. That
keeps your voice sounding like you instead of a chipmunk. It runs 8 ms behind you, because it
needs a little of your voice to look at, and that is all it adds while it is on. Tune sets
how far it pulls and Speed how fast: the pull glides over 0.4 s at Speed 0, 0.1 s at 50 and
1 ms at 100. It keeps the note it is on until you are clearly nearer another one, so a note
sung between two doesn't flip back and forth.

**The song's key.** With Notes on *The song's key*, the server listens to the song itself
(the first 6 minutes, with ffmpeg) and works out which notes it uses, in about a fifth of a
second. A YouTube song is downloaded for this, which needs yt-dlp too, and only while the mic
is on. The key follows a key change once the music has it: a video playing embedded keeps its
own key until its copy is downloaded, and Tune waits with it.

**The feedback guard.** If the speakers' sound feeds back into the mic, it howls: one pure
tone that holds still and gets loud. When the guard hears that for a full second, it turns
Mic volume down to 60%, at most every 3 seconds, and not below 25%; turn it back up once the
mic is moved or the speakers are lower. A held note, however loud, boosted or pitch
corrected, has its octave beside it, which a howl doesn't, so singing doesn't set it off.
Headphones, or a mic pointed away from the speakers, stop feedback altogether.
