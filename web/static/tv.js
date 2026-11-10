// The TV: plays the song at the head of the queue, embedded from YouTube or from the
// server's own copy, shows "up next" between songs, and tells the server when one ends.
import { api, connect, fmtTime, icon, save, store } from './live.js';
import { Mic } from './mic.js';
import { LyricsPanel } from './tv-lyrics.js';
import { KeyShift, keyLabel } from './keyshift.js';
import { PRESETS, latencyVerdict, normalize } from './mic-core.js';

const $ = (sel) => document.querySelector(sel);
const video = $('#video');

const tv = {
  state: null,
  started: false,
  phase: 'idle', // idle | upnext | wait | playing | error
  qid: null, // the entry on screen
  mode: null, // how it is playing: embed | file
  url: null,
  finished: null, // the entry already reported as ended
  embedFailed: null, // the entry YouTube refused to embed
  yt: null,
  ytLoading: null,
  volume: store('volume', 80),
  muted: false,
  joinURL: location.origin,
  timers: {},
};

// --- Screens ---------------------------------------------------------------
function setPhase(phase) {
  if (phase !== 'playing') {
    $('#banner').classList.remove('show');
    $('#nextbar').classList.remove('show');
  }
  tv.phase = phase;
  document.body.className = document.body.className.replace(/\bphase-\S+/g, '').trim() + ` phase-${phase}`;
  reportSoon();
}

function setMode(mode) {
  tv.mode = mode;
  document.body.classList.toggle('mode-embed', mode === 'embed');
  document.body.classList.toggle('mode-file', mode === 'file');
}

function later(name, ms, fn) {
  clearTimeout(tv.timers[name]);
  tv.timers[name] = ms === null ? undefined : setTimeout(fn, ms);
}

function flash(selector, ms) {
  const node = $(selector);
  node.classList.add('show');
  later(selector, ms, () => node.classList.remove('show'));
}

function osd(text) {
  $('#osd').textContent = text;
  flash('#osd', 1500);
}

function showJoin() {
  for (const img of document.querySelectorAll('.qr')) img.src = `/api/qr?text=${encodeURIComponent(tv.joinURL)}`;
  for (const p of document.querySelectorAll('.join')) p.textContent = tv.joinURL.replace(/^https?:\/\//, '');
}

// --- Players -------------------------------------------------------------------
const PLAYER_TIMEOUT_MS = 20000;

// loadYouTubeAPI loads YouTube's player script once. If it fails (offline, or blocked), the
// next song tries again.
function loadYouTubeAPI() {
  if (!tv.ytLoading) {
    tv.ytLoading = new Promise((resolve, reject) => {
      window.onYouTubeIframeAPIReady = resolve;
      const script = document.createElement('script');
      script.src = 'https://www.youtube.com/iframe_api';
      script.onerror = () => {
        tv.ytLoading = null;
        script.remove();
        reject(new Error("YouTube's player script didn't load"));
      };
      document.head.append(script);
    });
  }
  return tv.ytLoading;
}

// youtubePlayer builds the one YouTube player. Everyone waiting for it shares one promise, so
// state that arrives while it loads does not build a second player.
function youtubePlayer() {
  tv.ytReady ||= loadYouTubeAPI().then(makeYouTubePlayer).catch((err) => {
    tv.ytReady = null;
    throw err;
  });
  return tv.ytReady;
}

function withTimeout(promise, ms, message) {
  let timer;
  const timeout = new Promise((_, reject) => { timer = setTimeout(() => reject(new Error(message)), ms); });
  return Promise.race([promise, timeout]).finally(() => clearTimeout(timer));
}

function makeYouTubePlayer() {
  return new Promise((resolve) => {
    const player = new YT.Player('yt', {
      width: '100%', height: '100%',
      playerVars: { autoplay: 1, cc_load_policy: 0, controls: 0, disablekb: 1, fs: 0, iv_load_policy: 3, playsinline: 1, rel: 0, origin: location.origin },
      events: {
        onReady: () => { tv.yt = player; resolve(player); },
        onStateChange: (e) => {
          if (e.data === YT.PlayerState.ENDED && tv.mode === 'embed') finish(tv.qid);
          if (e.data === YT.PlayerState.PLAYING) {
            applyVolume();
            // Auto-generated captions ("[Music]") would sit on top of the video's own lyrics.
            player.unloadModule?.('captions');
          }
          reportSoon();
        },
        onError: (e) => youtubeError(e.data),
      },
    });
  });
}

// What the remote's buttons act on, whichever player is up.
const players = {
  embed: {
    toggle: () => (tv.yt.getPlayerState() === YT.PlayerState.PLAYING ? tv.yt.pauseVideo() : tv.yt.playVideo()),
    play: () => tv.yt.playVideo(),
    pause: () => tv.yt.pauseVideo(),
    seekBy: (d) => tv.yt.seekTo(Math.max(0, tv.yt.getCurrentTime() + d), true),
    restart: () => tv.yt.seekTo(0, true),
    position: () => tv.yt.getCurrentTime() || 0,
    duration: () => tv.yt.getDuration() || 0,
    state: () => ({ 1: 'playing', 2: 'paused', 3: 'buffering', 5: 'buffering', [-1]: 'buffering' })[tv.yt.getPlayerState()] || 'paused',
  },
  file: {
    toggle: () => (video.paused ? video.play() : video.pause()),
    play: () => video.play(),
    pause: () => video.pause(),
    seekBy: (d) => { video.currentTime = Math.max(0, video.currentTime + d); },
    restart: () => { video.currentTime = 0; },
    position: () => video.currentTime || 0,
    duration: () => (Number.isFinite(video.duration) ? video.duration : 0),
    state: () => (video.paused ? 'paused' : video.readyState < 3 ? 'buffering' : 'playing'),
  },
};

function active() {
  if (tv.phase !== 'playing') return null;
  if (tv.mode === 'embed' && tv.yt) return players.embed;
  if (tv.mode === 'file') return players.file;
  return null;
}

function stopPlayers() {
  if (tv.yt) tv.yt.stopVideo();
  video.pause();
  video.removeAttribute('src');
  video.load();
  setMode(null);
  tv.url = null;
  tv.starting = null;
}

function applyVolume() {
  video.volume = tv.volume / 100;
  video.muted = tv.muted;
  if (tv.yt) {
    tv.yt.setVolume(tv.volume);
    if (tv.muted) tv.yt.mute(); else tv.yt.unMute();
  }
}

// --- Following the room ----------------------------------------------------------
function onState(state) {
  tv.state = state;
  route();
  lyricsPanel.sync(state);
  keyChanged(state.current);
}

function route() {
  const cur = tv.state?.current;
  if (!cur) {
    if (tv.phase !== 'idle') stopPlayers();
    tv.qid = null;
    setPhase('idle');
    return;
  }
  if (!tv.started) return; // the start screen covers whatever is behind it
  if (cur.qid !== tv.qid) {
    stopPlayers();
    tv.qid = cur.qid;
    upNext(cur);
    return;
  }
  if (tv.phase === 'upnext') {
    fillUpNext(cur);
    return;
  }
  apply(cur);
}

function upNext(cur) {
  fillUpNext(cur);
  setPhase('upnext');
  const seconds = tv.state.settings.upNextSeconds || 6;
  const bar = $('#upnext .countdown span');
  bar.style.transition = 'none';
  bar.style.transform = 'scaleX(1)';
  requestAnimationFrame(() => requestAnimationFrame(() => {
    bar.style.transition = `transform ${seconds}s linear`;
    bar.style.transform = 'scaleX(0)';
  }));
  later('upnext', seconds * 1000, () => {
    const now = tv.state?.current;
    if (now && now.qid === cur.qid) apply(now);
  });
}

function fillUpNext(cur) {
  $('#upnext .who').textContent = cur.singer || 'Your turn';
  $('#upnext .song').textContent = cur.song.title;
}

function apply(cur) {
  if (tv.phase === 'error') return; // already on its way out
  // A song playing keeps the player it started in: its download finishing, being retried or
  // being switched on mid-song would otherwise stop it or start it over. Real trouble comes
  // from the players themselves (youtubeError, the video's error event). The one exception
  // is a key change, which only a download can do: the song carries on from it, where the
  // video was.
  if (tv.phase === 'playing' && (tv.mode || tv.starting === cur.qid)) {
    if (tv.mode === 'file') shiftKey(cur);
    else if (tv.mode === 'embed' && cur.key && cur.play.mode === 'file') startFile(cur, cur.play.url, tv.yt?.getCurrentTime?.() || 0);
    return;
  }
  let play = cur.play;
  if (play.mode === 'embed' && tv.embedFailed === cur.qid) play = { mode: 'wait' }; // until the server catches up
  switch (play.mode) {
    case 'embed':
      if (tv.phase !== 'playing' || tv.mode !== 'embed') startEmbed(cur);
      break;
    case 'file':
      if (tv.phase !== 'playing' || tv.mode !== 'file' || tv.url !== play.url) startFile(cur, play.url);
      break;
    case 'wait':
      if (tv.phase === 'playing') stopPlayers();
      $('#wait .song').textContent = cur.song.title;
      $('#wait .meter i').style.width = `${Math.round((play.progress || 0) * 100)}%`;
      $('#wait .detail').textContent = play.progress ? `Downloading ${Math.round(play.progress * 100)}%` : 'Waiting for the download to start';
      setPhase('wait');
      break;
    default:
      showError(cur, play.error || 'This song cannot play.');
  }
}

async function startEmbed(cur) {
  setPhase('playing');
  tv.starting = cur.qid;
  let player;
  try {
    player = await withTimeout(youtubePlayer(), PLAYER_TIMEOUT_MS, "YouTube's player didn't load");
  } catch (err) {
    if (tv.starting === cur.qid && tv.qid === cur.qid) {
      tv.starting = null;
      askForCopy(cur, 0, `${err.message}; asking the server for a copy`);
    }
    return;
  }
  if (tv.qid !== cur.qid || tv.starting !== cur.qid) return; // skipped or stopped while it loaded
  tv.starting = null;
  video.pause();
  setMode('embed');
  player.loadVideoById(cur.song.videoId);
  applyVolume();
  nowSinging(cur);
}

// startFile plays the downloaded copy; at, when set, carries on from there (a key change
// mid-song), without the now-singing banner again.
function startFile(cur, url, at = 0) {
  setPhase('playing');
  if (tv.yt) tv.yt.stopVideo();
  setMode('file');
  tv.url = url;
  video.src = url;
  if (at > 0) video.addEventListener('loadedmetadata', () => { video.currentTime = at; }, { once: true });
  applyVolume();
  shiftKey(cur);
  video.play().catch((err) => console.warn('play:', err));
  if (!at) nowSinging(cur);
  else osd(keyLabel(cur.key || 0)); // the copy a key change was waiting for is here
}

// --- Key -----------------------------------------------------------------------------
const keyShift = new KeyShift(video);

function shiftKey(cur) {
  const want = cur.key || 0;
  if (keyShift.semitones === want && (want === 0 || keyShift.stretch)) return;
  keyShift.set(want).catch((err) => osd(`Key change failed: ${err.message}`));
}

// keyChanged tells the room when the singers move the key, and why it may take a moment.
function keyChanged(cur) {
  const key = cur?.key || 0;
  if (!cur || cur.qid !== tv.keyQid) {
    tv.keyQid = cur?.qid;
    tv.key = key;
    return;
  }
  if (key === tv.key) return;
  tv.key = key;
  const waiting = tv.mode === 'embed' && key && cur.play.mode !== 'file';
  osd(waiting ? `${keyLabel(key)}: getting the song's copy first` : keyLabel(key));
}

function changeKey(delta) {
  const cur = tv.state?.current;
  if (!cur) return;
  api(`/api/queue/${encodeURIComponent(cur.qid)}/key`, { method: 'POST', body: { key: (cur.key || 0) + delta } })
    .catch((err) => osd(err.message));
}

function nowSinging(cur) {
  $('#banner .who').textContent = cur.singer ? `🎤 ${cur.singer}` : '🎤';
  $('#banner .song').textContent = cur.song.title;
  flash('#banner', 8000);
}

function showError(cur, message) {
  stopPlayers();
  $('#error .song').textContent = cur.song.title;
  $('#error .detail').textContent = message;
  setPhase('error');
  later('error', 6000, () => finish(cur.qid));
}

// 101 and 150: the owner refuses embedding. 153: YouTube could not tell who is embedding it.
// Both are what a download fixes, so the server gets the chance to fetch one.
function youtubeError(code) {
  const cur = tv.state?.current;
  if (!cur || cur.qid !== tv.qid) return;
  if ([101, 150, 153].includes(code)) {
    askForCopy(cur, code, "This video won't play embedded; asking the server for a copy");
    return;
  }
  showError(cur, `YouTube refused to play it (error ${code}).`);
}

// askForCopy waits for the server to download what the embedded player can't play. code is
// YouTube's error, or 0 when its player never loaded here.
function askForCopy(cur, code, detail) {
  tv.embedFailed = cur.qid;
  setPhase('wait');
  $('#wait .song').textContent = cur.song.title;
  $('#wait .detail').textContent = detail;
  api('/api/player/embed-failed', { method: 'POST', body: { qid: cur.qid, code } }).catch((err) => showError(cur, err.message));
}

function finish(qid) {
  if (!qid || tv.finished === qid) return;
  tv.finished = qid;
  api('/api/player/next', { method: 'POST', body: { qid } }).catch((err) => {
    console.warn('next:', err);
    // Lost on the way (Wi-Fi, a server restart): try again while the song is still current.
    if (tv.finished === qid) tv.finished = null;
    later('finish', 3000, () => {
      if (tv.state?.current?.qid === qid) finish(qid);
    });
  });
}

video.addEventListener('ended', () => tv.mode === 'file' && finish(tv.qid));
video.addEventListener('error', () => {
  const cur = tv.state?.current;
  if (tv.mode === 'file' && cur && video.getAttribute('src')) showError(cur, 'The downloaded copy would not play.');
});

// --- The remote's commands -------------------------------------------------------
function onCommand({ action, value }) {
  if (action === 'volume') {
    tv.volume = Math.max(0, Math.min(100, tv.volume + value));
    tv.muted = false;
    save('volume', tv.volume);
    applyVolume();
    osd(`Volume ${tv.volume}`);
    reportSoon();
    return;
  }
  if (action === 'mute') {
    tv.muted = !tv.muted;
    applyVolume();
    osd(tv.muted ? 'Muted' : `Volume ${tv.volume}`);
    reportSoon();
    return;
  }
  if (action.startsWith('mic-')) {
    micCommand(action, value);
    return;
  }
  if (action === 'lyrics-toggle') {
    toggleLyrics();
    return;
  }
  if (action === 'lyrics-scroll') {
    lyricsPanel.scroll(value);
    return;
  }
  const player = active();
  if (!player) return;
  if (action === 'seek') {
    player.seekBy(value);
    osd(`${value > 0 ? '+' : ''}${value}s`);
  } else {
    player[action]?.();
  }
  reportSoon();
}

// --- Telling the server how it is going -------------------------------------------
function status() {
  const player = active();
  let state = { idle: 'idle', upnext: 'waiting', wait: 'waiting', error: 'error' }[tv.phase] || 'buffering';
  let position = 0;
  let duration = 0;
  if (player) {
    try {
      state = player.state();
      position = player.position();
      duration = player.duration();
    } catch {
      state = 'buffering';
    }
  }
  return { qid: tv.qid || '', state, position, duration, volume: tv.volume, muted: tv.muted, started: tv.started, mic: mic.status(), lyrics: lyricsPanel.open };
}

function report() {
  const s = status();
  nextBar(s);
  api('/api/player/status', { method: 'POST', body: s }).catch(() => {});
}

function reportSoon() {
  later('report', 150, report);
}

function nextBar(s) {
  const next = tv.state?.queue?.[0];
  const show = next && tv.phase === 'playing' && s.duration > 0 && s.duration - s.position < 30;
  if (show) $('#nextbar').textContent = `Next: ${next.singer ? next.singer + ' · ' : ''}${next.song.title}`;
  $('#nextbar').classList.toggle('show', Boolean(show));
}

// --- The mic ------------------------------------------------------------------------
const mic = new Mic({
  onChange: () => micChanged(),
  onHowl: () => osd('Feedback! Mic turned down'),
});
mic.settings = normalize(store('mic', {}));

function micChanged() {
  save('mic', mic.settings);
  const s = mic.settings;
  $('#mic-on').checked = s.on;
  for (const k of ['volume', 'echo', 'reverb']) {
    $(`#mic-${k}`).value = s[k];
    $(`#mic-${k}-out`).textContent = k === 'volume' ? `${s[k]}%` : s[k];
  }
  $('#mic-button').classList.toggle('on', s.on);
  $('#mic-error').hidden = !mic.error;
  $('#mic-error').textContent = mic.error;
  if (mic.latencyMs !== null) $('#mic-latency').textContent = `Round trip ${latencyVerdict(mic.latencyMs)}`;
  reportSoon();
}

function micCommand(action, value) {
  const s = mic.settings;
  const changes = {
    'mic-toggle': { on: !s.on },
    'mic-volume': { volume: s.volume + value, on: true },
    'mic-echo': { echo: s.echo + value },
    'mic-reverb': { reverb: s.reverb + value },
    'mic-preset': PRESETS[['dry', 'ktv', 'hall'][value]] || {},
  }[action];
  if (!changes) return;
  mic.set(changes).then(() => {
    const now = mic.settings;
    osd(action === 'mic-toggle' ? (now.on ? 'Mic on' : 'Mic off') : `Mic ${now.volume}% · echo ${now.echo} · reverb ${now.reverb}`);
  });
}

async function fillDevices() {
  const select = $('#mic-device');
  try {
    const devices = await mic.devices();
    select.replaceChildren(new Option('Default mic', ''), ...devices
      .filter((d) => d.deviceId && d.deviceId !== 'default')
      .map((d, i) => new Option(d.label || `Mic ${i + 1}`, d.deviceId)));
    select.value = mic.settings.deviceId;
  } catch {
    // No device list until the browser has been allowed to use a mic.
  }
}

function toggleMicPanel() {
  const panel = $('#mic-panel');
  panel.hidden = !panel.hidden;
  if (!panel.hidden) {
    fillDevices();
    meter();
  }
}

function meter() {
  if ($('#mic-panel').hidden) return;
  $('#mic-meter i').style.width = `${Math.min(100, Math.round(mic.level * 100))}%`;
  requestAnimationFrame(meter);
}

function initMicPanel() {
  $('#mic-button').append(icon('mic'));
  const why = Mic.unsupported();
  if (why) {
    $('#mic-error').hidden = false;
    $('#mic-error').textContent = why;
    for (const input of document.querySelectorAll('#mic-panel input, #mic-panel select, #mic-panel button')) input.disabled = true;
  }
  $('#mic-button').onclick = toggleMicPanel;
  $('#mic-on').onchange = (e) => mic.set({ on: e.target.checked });
  for (const k of ['volume', 'echo', 'reverb']) {
    $(`#mic-${k}`).oninput = (e) => mic.set({ [k]: Number(e.target.value) });
  }
  $('#mic-device').onchange = (e) => mic.set({ deviceId: e.target.value, on: true }).then(fillDevices);
  for (const b of document.querySelectorAll('[data-preset]')) b.onclick = () => mic.set(PRESETS[b.dataset.preset]);
  $('#mic-measure').onclick = async () => {
    $('#mic-measure').disabled = true;
    $('#mic-latency').textContent = 'Listening for three clicks…';
    const ms = await mic.measure().catch((err) => { $('#mic-latency').textContent = err.message; return undefined; });
    if (ms === null) $('#mic-latency').textContent = latencyVerdict(null);
    $('#mic-measure').disabled = false;
  };
  navigator.mediaDevices?.addEventListener?.('devicechange', fillDevices);
  micChanged();
}

// --- Lyrics -------------------------------------------------------------------------
const lyricsPanel = new LyricsPanel($('#lyrics'), { onChange: () => reportSoon() });

function toggleLyrics() {
  lyricsPanel.setOpen(!lyricsPanel.open);
  save('lyrics', lyricsPanel.open);
  lyricsPanel.sync(tv.state);
  osd(lyricsPanel.open ? 'Lyrics on' : 'Lyrics off');
}

// --- Start, keys, cursor ------------------------------------------------------------
function start() {
  if (tv.started) return;
  tv.started = true;
  document.body.classList.add('started');
  loadYouTubeAPI().catch((err) => console.warn(err.message)); // a head start; songs retry
  if (!Mic.unsupported()) {
    mic.context(); // the click that started the TV is what lets audio run
    if (mic.settings.on) mic.open().then(() => micChanged());
  }
  route();
  report();
}

function toggleFullscreen() {
  if (document.fullscreenElement) document.exitFullscreen();
  else document.documentElement.requestFullscreen().catch(() => {});
}

document.addEventListener('keydown', (e) => {
  if (!tv.started) {
    if (e.key === 'Enter' || e.key === ' ') { e.preventDefault(); start(); }
    return;
  }
  const keys = {
    ' ': () => onCommand({ action: 'toggle' }), k: () => onCommand({ action: 'toggle' }),
    ArrowRight: () => onCommand({ action: 'seek', value: 10 }), ArrowLeft: () => onCommand({ action: 'seek', value: -10 }),
    ArrowUp: () => onCommand({ action: 'volume', value: 5 }), ArrowDown: () => onCommand({ action: 'volume', value: -5 }),
    m: () => onCommand({ action: 'mute' }), f: toggleFullscreen, v: toggleMicPanel, l: toggleLyrics,
    PageDown: () => lyricsPanel.scroll(1), PageUp: () => lyricsPanel.scroll(-1),
    '[': () => changeKey(-1), ']': () => changeKey(1),
    n: () => tv.state?.current && finish(tv.state.current.qid),
  };
  const run = keys[e.key] || keys[e.key.toLowerCase()];
  if (run) { e.preventDefault(); run(); }
});

let cursorTimer;
document.addEventListener('mousemove', () => {
  document.body.classList.remove('hide-cursor');
  clearTimeout(cursorTimer);
  cursorTimer = setTimeout(() => document.body.classList.add('hide-cursor'), 2500);
});

async function init() {
  $('#start-button').onclick = start;
  initMicPanel();
  if (store('lyrics', false)) lyricsPanel.setOpen(true);
  setPhase('idle');
  try {
    const info = await api('/api/info');
    const local = ['localhost', '127.0.0.1', '[::1]'].includes(location.hostname);
    if (local && info.urls?.length) tv.joinURL = info.urls[0];
  } catch {
    // The page still works; the QR code just shows this page's own address.
  }
  showJoin();
  connect({ state: onState, command: onCommand });
  setInterval(() => (tv.phase === 'playing' ? report() : nextBar(status())), 1000);
  setInterval(() => tv.phase !== 'playing' && report(), 5000);
}

init();

// For the console: what the TV thinks it is doing.
window.karaokeTV = tv;
window.karaokeMic = mic;
window.karaokeLyrics = lyricsPanel;
window.karaokeKey = keyShift;
window.fmtTime = fmtTime;
