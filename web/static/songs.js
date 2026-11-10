// What the phone and the TV's search share: asking the server, and describing a song.
import { api, el, fmtTime } from './live.js';

export const FORMATS = { cdg: 'MP3+G', video: 'Video', audio: 'Audio' };
export const LIBRARY_LIMIT = 40; // the server's most library hits per search

// canPlay says whether this server can play a song: library files and embeddable videos
// always, the rest only when it can download them.
export function canPlay(song, features) {
  return song.source === 'library' || Boolean(song.embeddable) || Boolean(features?.download);
}

// thumb is a song's picture: YouTube's thumbnail, or for a file on the server's computer, a
// tile saying what kind of file it is.
export function thumb(song) {
  if (song.thumb) return el('img', { src: song.thumb, alt: '', loading: 'lazy', width: 112, height: 63 });
  return el('div', { class: `thumb file-${song.format || 'video'}`, 'aria-hidden': 'true' }, FORMATS[song.format] || 'File');
}

// songMeta is the line under a song's title: artist, length, views (YouTube) or folder.
export function songMeta(song) {
  return [song.artist, fmtTime(song.duration), song.views, song.where].filter(Boolean).join(' · ');
}

// badge is a short note on a song that won't play straight away, or null.
export function badge(song, features) {
  if (song.source === 'library' || song.embeddable) return null;
  return canPlay(song, features)
    ? { kind: 'dl', text: 'Downloads first', title: "This video can't play embedded, so the server downloads it" }
    : { kind: 'err', text: "Can't play here", title: 'It refuses to embed, and the server has no yt-dlp' };
}

// searchLibrary returns the server's own files matching query (none without a library).
export async function searchLibrary(query, features) {
  if (!features?.library || !query.trim()) return [];
  const page = await api(`/api/search?${new URLSearchParams({ source: 'library', q: query })}`);
  return page.results || [];
}

// searchYouTube returns one page of YouTube results: a new search, or the page after more.
export function searchYouTube({ query, more, karaoke }) {
  const params = new URLSearchParams({ source: 'youtube' });
  if (more) params.set('more', more);
  else params.set('q', query);
  if (karaoke) params.set('karaoke', '1');
  return api(`/api/search?${params}`);
}

// queueSong adds a song under a singer's name; next puts it first in line.
export function queueSong(song, singer, next) {
  return api('/api/queue', { method: 'POST', body: { song, singer, next } });
}

// addedMessage says where a song just queued landed.
export function addedMessage(added) {
  const n = added.ahead;
  if (n === 0) return `Up now: ${added.entry.song.title}`;
  if (n === 1) return 'Added: it sings next';
  return `Added: ${n - 1} song${n === 2 ? '' : 's'} before it`;
}
