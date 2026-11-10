// The phone remote: search, queue a song under your name, and drive the TV.
import { api, connect, el, fmtTime, icon, save, store } from './live.js';
import { initLyrics } from './remote-lyrics.js';

const $ = (sel) => document.querySelector(sel);
const view = { state: null, player: null, singer: store('singer', ''), more: '', query: '' };

// --- Toast ---------------------------------------------------------------
let toastTimer;
function toast(message, bad = false) {
  const t = $('#toast');
  t.textContent = message;
  t.classList.toggle('bad', bad);
  t.classList.add('show');
  clearTimeout(toastTimer);
  toastTimer = setTimeout(() => t.classList.remove('show'), 2600);
}

async function attempt(work) {
  try {
    return await work();
  } catch (err) {
    toast(err.message, true);
    return undefined;
  }
}

// --- Singer name ---------------------------------------------------------
function showSinger() {
  const chip = $('#singer');
  chip.textContent = view.singer ? `🎤 ${view.singer}` : 'Your name';
  chip.classList.toggle('unset', !view.singer);
}

function askName() {
  return new Promise((resolve) => {
    const dialog = $('#name-dialog');
    const input = $('#name-input');
    input.value = view.singer;
    const done = (ok) => {
      dialog.close();
      if (ok && input.value.trim()) {
        view.singer = input.value.trim().slice(0, 40);
        save('singer', view.singer);
        showSinger();
      }
      resolve(Boolean(view.singer));
    };
    $('#name-form').onsubmit = (e) => { e.preventDefault(); done(true); };
    $('#name-cancel').onclick = () => done(false);
    dialog.showModal();
    input.focus();
  });
}

// --- Tabs ----------------------------------------------------------------
function showTab(name) {
  for (const b of document.querySelectorAll('.tabs button')) {
    b.setAttribute('aria-selected', String(b.dataset.tab === name));
  }
  for (const tab of ['search', 'queue', 'history', 'lyrics']) $(`#tab-${tab}`).hidden = tab !== name;
  save('tab', name);
  if (name === 'lyrics') lyricsTab?.render(true);
}

// --- Search --------------------------------------------------------------
async function search(append) {
  const params = new URLSearchParams({ source: 'youtube' });
  if (append) params.set('more', view.more);
  else params.set('q', view.query);
  const local = append ? null : searchLibrary();
  if ($('#karaoke-only').checked) params.set('karaoke', '1');
  const button = append ? $('#more') : $('#search-form button');
  button.disabled = true;
  const page = await attempt(() => api(`/api/search?${params}`));
  button.disabled = false;
  if (!page) return;
  const list = $('#results');
  if (!append) list.replaceChildren();
  list.append(...page.results.map(resultItem));
  view.more = page.more || '';
  $('#more').hidden = !view.more || page.results.length === 0;
  const onComputer = append ? 0 : await local;
  $('#search-hint').hidden = list.children.length > 0 || onComputer > 0;
  if (!append && page.results.length === 0) $('#yt-heading').hidden = true;
  if (!append && page.results.length === 0 && !onComputer) {
    $('#search-hint').hidden = false;
    $('#search-hint').textContent = 'Nothing found. Try fewer words, or turn off "Karaoke versions only".';
  }
}

function canPlay(song) {
  return song.source === 'library' || song.embeddable || Boolean(view.state?.features?.download);
}

const FORMATS = { cdg: 'MP3+G', video: 'Video', audio: 'Audio' };

// thumb is a song's picture: YouTube's thumbnail, or for a file on this computer, its kind.
function thumb(song) {
  if (song.thumb) return el('img', { src: song.thumb, alt: '', loading: 'lazy', width: 112, height: 63 });
  return el('div', { class: `thumb file-${song.format || 'video'}`, 'aria-hidden': 'true' }, FORMATS[song.format] || 'File');
}

// The library: songs in folders on the server's computer, searched there (fast, no quota).
async function searchLibrary() {
  const box = $('#library-results');
  const count = view.state?.features?.library || 0;
  if (!count || !view.query) {
    box.hidden = true;
    $('#yt-heading').hidden = true;
    return 0;
  }
  const page = await attempt(() => api(`/api/search?${new URLSearchParams({ source: 'library', q: view.query })}`));
  const found = page?.results || [];
  $('#lib-results').replaceChildren(...found.map(resultItem));
  $('#lib-count').textContent = found.length >= 40 ? '40+' : String(found.length);
  box.hidden = found.length === 0;
  $('#yt-heading').hidden = found.length === 0;
  if (found.length) $('#search-hint').hidden = true;
  return found.length;
}

function resultItem(song) {
  const playable = canPlay(song);
  const meta = [song.artist, fmtTime(song.duration), song.views, song.where].filter(Boolean).join(' · ');
  let badge = null;
  if (song.source !== 'library' && !song.embeddable) {
    badge = playable
      ? el('span', { class: 'badge dl', text: 'Downloads first', title: "This video can't play embedded, so the server downloads it" })
      : el('span', { class: 'badge err', text: "Can't play here", title: 'It refuses to embed, and the server has no yt-dlp' });
  }
  return el('li', { class: `item${playable ? '' : ' blocked'}` },
    thumb(song),
    el('div', { class: 'body' },
      el('div', { class: 'title', text: song.title }),
      el('div', { class: 'meta', text: meta }),
      badge),
    el('div', { class: 'actions' },
      el('button', { class: 'act add', type: 'button', disabled: !playable, 'aria-label': 'Add to the queue', onclick: () => add(song, false) }, icon('plus')),
      el('button', { class: 'act', type: 'button', disabled: !playable, 'aria-label': 'Sing next', title: 'Sing next', onclick: () => add(song, true) }, icon('top'))));
}

async function add(song, next) {
  if (!view.singer && !(await askName())) return;
  const added = await attempt(() => api('/api/queue', { method: 'POST', body: { song, singer: view.singer, next } }));
  if (!added) return;
  const n = added.ahead;
  toast(n === 0 ? `Up now: ${added.entry.song.title}` : n === 1 ? 'Added: it sings next' : `Added: ${n - 1} song${n === 2 ? '' : 's'} before it`);
}

// --- Now playing ---------------------------------------------------------
function playStatus(entry) {
  const play = entry.play;
  if (play.mode === 'wait') return `Downloading… ${Math.round((play.progress || 0) * 100)}%`;
  if (play.mode === 'error') return play.error;
  const p = view.player;
  if (!p || p.qid !== entry.qid) return 'Waiting for the TV';
  if (!p.started) return 'Waiting for someone to tap "Start" on the TV';
  return { playing: 'Playing', paused: 'Paused', buffering: 'Loading…', waiting: 'Up next card', error: 'Playback error' }[p.state] || '';
}

const command = (action, value = 0) => attempt(() => api('/api/player/command', { method: 'POST', body: { action, value } }));

// The now-playing card is built once per song and then updated in place: rebuilding it on
// every status report (once a second) would swap a button out from under a finger mid-tap.
let nowKey = '';
let nowRefs = null;

function renderNow() {
  const now = $('#now');
  const cur = view.state?.current;
  if (!cur) {
    if (nowKey !== 'idle') {
      now.className = 'now idle';
      now.replaceChildren(el('div', { text: 'Nothing playing. Search for a song to start the night.' }));
      nowKey = 'idle';
      nowRefs = null;
    }
    renderMic();
    return;
  }
  const key = `${cur.qid}|${cur.song.title}|${cur.singer}`;
  if (key !== nowKey) {
    nowKey = key;
    nowRefs = {
      status: el('div', { class: 'status' }),
      fill: el('i'),
      position: el('span'),
      duration: el('span'),
      toggle: el('button', { type: 'button', class: 'main', onclick: () => command('toggle') }),
      key: el('output'),
      keyNote: el('span', { class: 'hint' }),
    };
    nowRefs.keyRow = el('div', { class: 'keyrow' },
      el('span', { class: 'name', text: 'Key' }),
      el('button', { type: 'button', class: 'act', 'aria-label': 'Key down a semitone', onclick: () => setKey(-1) }, '♭'),
      nowRefs.key,
      el('button', { type: 'button', class: 'act', 'aria-label': 'Key up a semitone', onclick: () => setKey(1) }, '♯'),
      nowRefs.keyNote);
    now.className = 'now';
    now.replaceChildren(
      el('div', { class: 'row' },
        thumb(cur.song),
        el('div', { class: 'what' },
          el('div', { class: 'label', text: 'Now singing' }),
          el('div', { class: 'title', text: cur.song.title }),
          cur.singer ? el('div', { class: 'singer', text: `🎤 ${cur.singer}` }) : null,
          nowRefs.status)),
      el('div', { class: 'bar' }, nowRefs.fill),
      el('div', { class: 'times' }, nowRefs.position, nowRefs.duration),
      el('div', { class: 'controls' },
        el('button', { type: 'button', 'aria-label': 'Volume down', onclick: () => command('volume', -10) }, icon('volDown')),
        el('button', { type: 'button', 'aria-label': 'Restart the song', onclick: () => command('restart') }, icon('restart')),
        nowRefs.toggle,
        el('button', { type: 'button', 'aria-label': 'Skip to the next song', onclick: skip }, icon('next')),
        el('button', { type: 'button', 'aria-label': 'Volume up', onclick: () => command('volume', 10) }, icon('volUp'))),
      nowRefs.keyRow);
  }
  const p = view.player && view.player.qid === cur.qid ? view.player : null;
  const duration = p?.duration || cur.song.duration || 0;
  const position = p?.position || 0;
  const paused = p ? p.state !== 'playing' : false;
  nowRefs.status.textContent = playStatus(cur);
  nowRefs.fill.style.width = duration ? `${Math.min(100, (position / duration) * 100)}%` : '0';
  nowRefs.position.textContent = fmtTime(position);
  nowRefs.duration.textContent = fmtTime(duration);
  const semitones = cur.key || 0;
  nowRefs.keyRow.hidden = !view.state.features?.download;
  nowRefs.key.textContent = semitones ? `${semitones > 0 ? '+' : '−'}${Math.abs(semitones)}` : '0';
  nowRefs.keyNote.textContent = semitones && cur.play.mode === 'wait'
    ? `getting the song's copy${cur.play.progress ? ` ${Math.round(cur.play.progress * 100)}%` : ''}`
    : semitones ? '' : 'original';
  if (nowRefs.paused !== paused) {
    nowRefs.paused = paused;
    nowRefs.toggle.setAttribute('aria-label', paused ? 'Play' : 'Pause');
    nowRefs.toggle.replaceChildren(icon(paused ? 'play' : 'pause'));
  }
  renderMic();
}

// --- The TV's mic ---------------------------------------------------------
let micRefs = null;

function renderMic() {
  const card = $('#mic');
  const m = view.player?.mic;
  if (!m || (!m.on && m.error && m.error.includes('localhost'))) {
    card.hidden = true;
    return;
  }
  card.hidden = false;
  if (!micRefs) {
    const step = (label, action, delta, out) => el('div', { class: 'step' },
      el('span', { class: 'name', text: label }),
      el('button', { type: 'button', class: 'act', 'aria-label': `${label} down`, onclick: () => command(action, -delta) }, '−'),
      out,
      el('button', { type: 'button', class: 'act', 'aria-label': `${label} up`, onclick: () => command(action, delta) }, '+'));
    micRefs = {
      toggle: el('button', { type: 'button', class: 'mic-toggle', onclick: () => command('mic-toggle') }, icon('mic'), el('span')),
      volume: el('output'), echo: el('output'), reverb: el('output'),
      note: el('div', { class: 'hint' }),
    };
    card.replaceChildren(
      el('div', { class: 'mic-head' }, micRefs.toggle,
        el('div', { class: 'presets' },
          ...['Dry', 'KTV', 'Hall'].map((name, i) => el('button', { type: 'button', class: 'ghost', onclick: () => command('mic-preset', i) }, name)))),
      step('Mic', 'mic-volume', 10, micRefs.volume),
      step('Echo', 'mic-echo', 10, micRefs.echo),
      step('Reverb', 'mic-reverb', 10, micRefs.reverb),
      micRefs.note);
  }
  micRefs.toggle.classList.toggle('on', m.on);
  micRefs.toggle.lastChild.textContent = m.on ? 'Mic on' : 'Mic off';
  micRefs.volume.textContent = `${m.volume}%`;
  micRefs.echo.textContent = m.echo;
  micRefs.reverb.textContent = m.reverb;
  micRefs.note.textContent = m.error || (m.latencyMs ? `Delay ${m.latencyMs} ms${m.device ? ' · ' + m.device : ''}` : m.device || '');
}

function setKey(delta) {
  const cur = view.state?.current;
  if (!cur) return;
  attempt(() => api(`/api/queue/${encodeURIComponent(cur.qid)}/key`, { method: 'POST', body: { key: (cur.key || 0) + delta } }));
}

async function skip() {
  const cur = view.state?.current;
  if (!cur || !confirm(`Skip "${cur.song.title}"?`)) return;
  await attempt(() => api('/api/player/next', { method: 'POST', body: { qid: cur.qid } }));
}

// --- Queue and history ---------------------------------------------------
function entryBadge(entry) {
  if (entry.play.mode === 'wait') {
    const pct = Math.round((entry.play.progress || 0) * 100);
    return el('span', { class: 'badge dl', text: pct ? `Downloading ${pct}%` : 'Waiting to download' });
  }
  if (entry.play.mode === 'error') return el('span', { class: 'badge err', text: "Can't play", title: entry.play.error });
  if (entry.play.mode === 'file') return el('span', { class: 'badge ok', text: 'Ready' });
  return null;
}

function renderQueue() {
  const queue = view.state?.queue || [];
  $('#queue-count').textContent = queue.length || '';
  $('#queue-empty').hidden = queue.length > 0;
  $('#clear').hidden = queue.length === 0;
  $('#queue').replaceChildren(...queue.map((entry, i) => el('li', { class: 'item' },
    el('span', { class: 'pos', text: i + 1 }),
    el('div', { class: 'body' },
      el('div', { class: 'title', text: entry.song.title }),
      entry.singer ? el('div', { class: 'who', text: `🎤 ${entry.singer}` }) : null,
      entryBadge(entry)),
    el('div', { class: 'actions row' },
      i > 0 ? el('button', { class: 'act', type: 'button', 'aria-label': 'Sing next', title: 'Sing next', onclick: () => move(entry, 0) }, icon('top')) : null,
      el('button', { class: 'act', type: 'button', 'aria-label': 'Remove from the queue', onclick: () => remove(entry) }, icon('close'))))));
}

function renderHistory() {
  const history = view.state?.history || [];
  $('#history-empty').hidden = history.length > 0;
  $('#history').replaceChildren(...history.map((entry) => el('li', { class: 'item' },
    thumb(entry.song),
    el('div', { class: 'body' },
      el('div', { class: 'title', text: entry.song.title }),
      entry.singer ? el('div', { class: 'who', text: `🎤 ${entry.singer}` }) : null),
    el('div', { class: 'actions' },
      el('button', { class: 'act add', type: 'button', 'aria-label': 'Sing it again', title: 'Sing it again', onclick: () => add(entry.song, false) }, icon('plus'))))));
}

function move(entry, to) {
  return attempt(() => api(`/api/queue/${encodeURIComponent(entry.qid)}/move`, { method: 'POST', body: { to } }));
}

function remove(entry) {
  return attempt(() => api(`/api/queue/${encodeURIComponent(entry.qid)}`, { method: 'DELETE' }));
}

// --- Settings ------------------------------------------------------------
function openSettings() {
  const s = view.state?.settings || {};
  const canDownload = Boolean(view.state?.features?.download);
  $('#always-download').checked = Boolean(s.alwaysDownload);
  $('#always-download').disabled = !canDownload;
  if (!canDownload) $('#always-download-hint').textContent = "Needs yt-dlp on the server, which isn't installed.";
  $('#upnext').value = s.upNextSeconds || 6;
  $('#settings-dialog').showModal();
}

async function saveSettings(e) {
  e.preventDefault();
  const upNextSeconds = Math.max(2, Math.min(30, Number($('#upnext').value) || 6));
  const ok = await attempt(() => api('/api/settings', { method: 'POST', body: { alwaysDownload: $('#always-download').checked, upNextSeconds } }));
  if (ok !== undefined) {
    $('#settings-dialog').close();
    toast('Saved');
  }
}

// --- Wiring --------------------------------------------------------------
function render() {
  renderNow();
  renderQueue();
  renderHistory();
  lyricsTab?.render();
}

let lyricsTab = null;

function init() {
  lyricsTab = initLyrics({ view, attempt, command, toast });
  showSinger();
  $('#open-settings').append(icon('gear'));
  $('#singer').onclick = askName;
  $('#open-settings').onclick = openSettings;
  $('#settings-form').onsubmit = saveSettings;
  $('#settings-cancel').onclick = () => $('#settings-dialog').close();
  for (const b of document.querySelectorAll('.tabs button')) b.onclick = () => showTab(b.dataset.tab);
  showTab(store('tab', 'search'));
  $('#karaoke-only').checked = store('karaokeOnly', true);
  $('#karaoke-only').onchange = (e) => save('karaokeOnly', e.target.checked);
  $('#search-form').onsubmit = (e) => {
    e.preventDefault();
    view.query = $('#q').value.trim();
    if (!view.query) return;
    $('#q').blur();
    search(false);
  };
  $('#more').onclick = () => search(true);
  $('#clear').onclick = () => confirm('Remove every song waiting in the queue?') &&
    attempt(() => api('/api/queue/clear', { method: 'POST' }));
  api('/api/info').then((info) => { $('#version').textContent = `Version ${info.version}`; }).catch(() => {});
  renderNow();
  connect({
    state: (s) => { view.state = s; if (s.player) view.player = s.player; render(); },
    player: (p) => { view.player = p; renderNow(); lyricsTab.player(); },
    online: (up) => document.body.classList.toggle('offline', !up),
  });
}

init();
