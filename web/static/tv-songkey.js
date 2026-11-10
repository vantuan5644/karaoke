// The song's key, for pitch correction that pulls a singer to the song's own notes rather
// than to any semitone. The server hears the key in the song's sound (GET /api/songkey); this
// asks about the song playing and the next one, and asks again while a YouTube song is
// downloading to be heard.
import { api } from './live.js';
import { keyName, notesFor } from './mic-core.js';

const RETRY_MS = 3000;

export class SongKeys {
  constructor({ onChange = () => {} } = {}) {
    this.known = new Map(); // qid -> { key, pending, error, at }
    this.asking = new Set();
    this.onChange = onChange;
    this.entries = [];
  }

  // follow keeps the keys of these entries (the song playing, the next) known.
  follow(entries) {
    this.entries = entries.filter(Boolean);
    const wanted = new Set(this.entries.map((e) => e.qid));
    for (const qid of this.known.keys()) if (!wanted.has(qid)) this.known.delete(qid);
    let waiting = false;
    for (const { qid } of this.entries) {
      const k = this.known.get(qid);
      if (this.asking.has(qid)) continue;
      if (k?.pending) waiting = true;
      if (k && !(k.pending && Date.now() - k.at >= RETRY_MS)) continue;
      this.ask(qid);
    }
    clearTimeout(this.timer);
    if (waiting) this.timer = setTimeout(() => this.follow(this.entries), RETRY_MS);
  }

  // stop forgets everything and asks no more (pitch correction no longer follows the song).
  stop() {
    clearTimeout(this.timer);
    this.entries = [];
  }

  async ask(qid) {
    this.asking.add(qid);
    let answer;
    try {
      answer = await api(`/api/songkey?qid=${encodeURIComponent(qid)}`);
    } catch (err) {
      answer = { error: err.message };
    }
    this.asking.delete(qid);
    if (!this.entries.some((e) => e.qid === qid)) return; // gone from the queue meanwhile
    this.known.set(qid, { key: answer.key || null, pending: Boolean(answer.pending), error: answer.error || '', at: Date.now() });
    if (answer.pending) this.follow(this.entries); // keeps the retry going
    this.onChange(qid);
  }

  get(qid) {
    return this.known.get(qid) || null;
  }
}

// shiftedKey is a key moved by a key change, as { name, tonic, minor }.
export function shiftedKey(key, semitones) {
  const tonic = (((key.tonic + Math.round(semitones || 0)) % 12) + 12) % 12;
  return { tonic, minor: key.minor, name: keyName(tonic, key.minor) };
}

// keysWanted is the entries whose keys pitch correction needs (the song playing and the
// next), or null when it isn't following the song.
export function keysWanted(settings, state) {
  if (settings.scale !== 'song' || !settings.tune || !state?.features?.keys) return null;
  return [state.current, state.queue?.[0]];
}

// tuneNotes is what pitch correction pulls to now: the notes (12 bits, 0 for every
// semitone), what the mic panel says about them, and the song's key as it sounds now.
export function tuneNotes(settings, state, keys) {
  const cur = state?.current;
  const info = cur ? keys.get(cur.qid) : null;
  return {
    notes: notesFor(settings.scale, info?.key, cur?.key || 0),
    text: songKeyText({ scale: settings.scale, tune: settings.tune, hasKeys: Boolean(state?.features?.keys), cur, info }),
    name: settings.scale === 'song' && info?.key ? shiftedKey(info.key, cur?.key).name : '',
  };
}

// songKeyText says what pitch correction is pulling to, for the mic panel.
export function songKeyText({ scale, tune, hasKeys, cur, info }) {
  if (scale !== 'song') return '';
  if (!hasKeys) return "This server can't hear a song's key (it needs ffmpeg), so every semitone counts for now.";
  if (!tune) return "Turn Tune up to pull your voice to the song's notes.";
  if (!cur) return "The song's key is found when a song plays.";
  if (!info || info.pending) return info?.pending ? 'Downloading the song to hear its key…' : "Listening for the song's key…";
  if (!info.key) return `${info.error || 'No key found'} Every semitone counts for now.`;
  if (cur.key) return `The song is in ${info.key.name}; with its key change, ${shiftedKey(info.key, cur.key).name}.`;
  return `The song is in ${info.key.name}: Tune pulls to its notes.`;
}
