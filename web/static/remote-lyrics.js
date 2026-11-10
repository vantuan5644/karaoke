// The phone's Lyrics tab: the words of the song playing, a switch for the TV's lyrics panel,
// and a way to pick other lyrics when the automatic match is wrong.
import { api, el, fmtTime, lyricLines } from './live.js';

const $ = (s) => document.querySelector(s);

export function initLyrics({ view, attempt, command, toast }) {
  let key = '';
  let answer = null;

  const current = () => view.state?.current || null;

  let failedKey = '';
  let retryAt = 0;

  // load fetches the lyrics while the tab is showing: when the song, or the lyrics picked
  // for it, change, or (force) when the tab opens.
  async function load(force = false) {
    const cur = current();
    const next = cur && view.state?.features?.lyrics ? `${cur.qid}|${cur.song.lyricsId || 0}` : '';
    if ($('#tab-lyrics').hidden) {
      if (next !== key) key = ''; // fetched when the tab opens
      return;
    }
    if (!force && next === key) return;
    if (!force && next === failedKey && Date.now() < retryAt) return;
    if (next !== key) answer = null;
    key = next;
    paint();
    if (!next) return;
    try {
      const got = await api(`/api/lyrics?qid=${encodeURIComponent(cur.qid)}`);
      if (key === next) answer = got;
    } catch (err) {
      if (key === next) {
        answer = { error: err.message };
        key = '';
        failedKey = next;
        retryAt = Date.now() + 15000;
      }
    }
    paint();
  }

  function paint() {
    const cur = current();
    const text = $('#lyrics-text');
    $('#lyrics-tv').checked = Boolean(view.player?.lyrics);
    if (!view.state?.features?.lyrics) {
      $('#lyrics-title').textContent = 'Lyrics are switched off on this server.';
      $('#lyrics-by').textContent = '';
      text.replaceChildren();
      return;
    }
    if (!cur) {
      $('#lyrics-title').textContent = 'Nothing playing.';
      $('#lyrics-by').textContent = '';
      text.replaceChildren();
      return;
    }
    const t = answer?.track;
    $('#lyrics-title').textContent = t ? t.track : cur.song.title;
    $('#lyrics-by').textContent = t ? `${t.artist}${answer.chosen ? ' · picked by hand' : ''}` : '';
    if (!answer) text.replaceChildren(el('p', { class: 'hint', text: 'Looking for the lyrics…' }));
    else if (answer.error) text.replaceChildren(el('p', { class: 'hint', text: `Couldn't get the lyrics: ${answer.error}` }));
    else if (answer.off) text.replaceChildren(el('p', { class: 'hint', text: 'Lyrics are off for this song.' }));
    else if (!t) text.replaceChildren(el('p', { class: 'hint', text: 'No lyrics found. Tap "Wrong lyrics?" to look for them.' }));
    else text.replaceChildren(...lyricLines(t.lyrics));
  }

  async function find(e) {
    e?.preventDefault();
    const q = $('#lyrics-q').value.trim();
    const list = $('#lyrics-results');
    if (!q) return;
    list.hidden = false;
    list.replaceChildren(el('li', { class: 'hint', text: 'Searching…' }));
    const found = await attempt(() => api(`/api/lyrics/search?q=${encodeURIComponent(q)}`));
    if (!found) {
      list.replaceChildren();
      return;
    }
    if (!found.results.length) {
      list.replaceChildren(el('li', { class: 'hint', text: 'Nothing on LRCLIB for that. Try the song name alone.' }));
      return;
    }
    list.replaceChildren(...found.results.map((r) => el('li', { class: 'item' },
      el('div', { class: 'meta' },
        el('div', { class: 'title', text: r.track }),
        el('div', { class: 'sub', text: [r.artist, r.duration ? fmtTime(r.duration) : '', `${r.lines} lines`].filter(Boolean).join(' · ') }),
        el('div', { class: 'sub preview', text: r.preview })),
      el('button', { type: 'button', class: 'primary', onclick: () => pick(r.id, `${r.track}: on the TV now`) }, 'Use'))));
  }

  async function pick(id, message) {
    const cur = current();
    if (!cur) return;
    const ok = await attempt(() => api(`/api/queue/${encodeURIComponent(cur.qid)}/lyrics`, { method: 'POST', body: { id } }));
    if (ok === undefined) return;
    toast(message);
    $('#lyrics-find').hidden = true;
  }

  function openFinder() {
    const finder = $('#lyrics-find');
    finder.hidden = !finder.hidden;
    if (!finder.hidden) {
      $('#lyrics-q').value = answer?.query || current()?.song.title || '';
      $('#lyrics-results').replaceChildren();
      $('#lyrics-q').focus();
    }
  }

  $('#lyrics-tv').onchange = () => command('lyrics-toggle');
  $('#lyrics-up').onclick = () => command('lyrics-scroll', -1);
  $('#lyrics-down').onclick = () => command('lyrics-scroll', 1);
  $('#lyrics-change').onclick = openFinder;
  $('#lyrics-form').onsubmit = find;
  $('#lyrics-auto').onclick = () => pick(0, 'Back to the automatic lyrics');
  $('#lyrics-none').onclick = () => pick(-1, 'No lyrics for this song');

  return {
    // render follows the room; shown says the tab just opened.
    render: (shown) => load(shown === true).then(paint),
    player: paint,
  };
}
