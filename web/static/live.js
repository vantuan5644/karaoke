// Shared by the phone remote and the TV: the JSON API, the live event stream, and a small
// DOM builder that only ever sets text (song titles come from YouTube, so never as HTML).

export async function api(path, { method = 'GET', body } = {}) {
  const opts = { method, headers: {} };
  if (body !== undefined || method !== 'GET') {
    opts.headers['Content-Type'] = 'application/json';
    opts.body = JSON.stringify(body ?? {});
  }
  const res = await fetch(path, opts);
  if (res.status === 204) return null;
  const data = await res.json().catch(() => null);
  if (!res.ok) throw new Error((data && data.error) || `${res.status} ${res.statusText}`);
  return data;
}

// connect keeps an EventSource open and hands each event to its handler. EventSource
// reconnects by itself after a dropped connection; this only covers it giving up for good.
export function connect(handlers) {
  let retry = 1000;
  const open = () => {
    const es = new EventSource('/api/events');
    for (const name of ['state', 'player', 'command']) {
      es.addEventListener(name, (e) => {
        try {
          handlers[name]?.(JSON.parse(e.data));
        } catch (err) {
          console.error(`event ${name}:`, err);
        }
      });
    }
    es.onopen = () => {
      retry = 1000;
      handlers.online?.(true);
    };
    es.onerror = () => {
      handlers.online?.(false);
      if (es.readyState === EventSource.CLOSED) {
        setTimeout(open, retry);
        retry = Math.min(retry * 2, 15000);
      }
    };
  };
  open();
}

export function el(tag, attrs = {}, ...children) {
  const node = document.createElement(tag);
  for (const [key, value] of Object.entries(attrs)) {
    if (value === undefined || value === null || value === false) continue;
    if (key === 'class') node.className = value;
    else if (key === 'text') node.textContent = value;
    else if (key.startsWith('on')) node.addEventListener(key.slice(2), value);
    else if (key === 'style' && typeof value === 'object') Object.assign(node.style, value);
    else node.setAttribute(key, value === true ? '' : value);
  }
  for (const child of children.flat()) {
    if (child === undefined || child === null || child === false) continue;
    node.append(child instanceof Node ? child : document.createTextNode(String(child)));
  }
  return node;
}

export function fmtTime(seconds) {
  if (!Number.isFinite(seconds) || seconds < 0) return '';
  const s = Math.floor(seconds % 60);
  const m = Math.floor(seconds / 60) % 60;
  const h = Math.floor(seconds / 3600);
  const mm = h ? String(m).padStart(2, '0') : String(m);
  return `${h ? h + ':' : ''}${mm}:${String(s).padStart(2, '0')}`;
}

export function store(key, fallback) {
  try {
    const raw = localStorage.getItem(`karaoke.${key}`);
    return raw === null ? fallback : JSON.parse(raw);
  } catch {
    return fallback;
  }
}

export function save(key, value) {
  try {
    localStorage.setItem(`karaoke.${key}`, JSON.stringify(value));
  } catch {
    // Private windows and full storage: the setting just isn't remembered.
  }
}

const ICONS = {
  play: 'M8 5v14l11-7z',
  pause: 'M6 5h4v14H6zM14 5h4v14h-4z',
  next: 'M6 18l8.5-6L6 6zm10-12h2v12h-2z',
  restart: 'M12 5V1L7 6l5 5V7a6 6 0 1 1-6 6H4a8 8 0 1 0 8-8z',
  volDown: 'M5 9v6h4l5 5V4L9 9zm11.5 3A4.5 4.5 0 0 0 14 8v8a4.5 4.5 0 0 0 2.5-4z',
  volUp: 'M3 9v6h4l5 5V4L7 9zm13.5 3A4.5 4.5 0 0 0 14 8v8a4.5 4.5 0 0 0 2.5-4zM14 3.2v2.1a7 7 0 0 1 0 13.4v2.1a9 9 0 0 0 0-17.6z',
  plus: 'M11 5h2v6h6v2h-6v6h-2v-6H5v-2h6z',
  up: 'M7 14l5-5 5 5z',
  top: 'M5 4h14v2H5zm2 10l5-5 5 5h-4v6h-2v-6z',
  close: 'M18.3 5.7 12 12l6.3 6.3-1.4 1.4L10.6 13.4 4.3 19.7 2.9 18.3 9.2 12 2.9 5.7 4.3 4.3l6.3 6.3 6.3-6.3z',
  search: 'M15.5 14h-.8l-.3-.3A6.5 6.5 0 1 0 14 15.5l.3.3v.8l5 5 1.5-1.5zm-6 0a4.5 4.5 0 1 1 0-9 4.5 4.5 0 0 1 0 9z',
  gear: 'M19.4 13a7.5 7.5 0 0 0 0-2l2.1-1.6-2-3.5-2.5 1a7.4 7.4 0 0 0-1.7-1L15 3h-4l-.4 2.9a7.4 7.4 0 0 0-1.7 1l-2.5-1-2 3.5L6.6 11a7.5 7.5 0 0 0 0 2l-2.1 1.6 2 3.5 2.5-1a7.4 7.4 0 0 0 1.7 1L11 21h4l.4-2.9a7.4 7.4 0 0 0 1.7-1l2.5 1 2-3.5zM13 15.5a3.5 3.5 0 1 1 0-7 3.5 3.5 0 0 1 0 7z',
  mic: 'M12 14a3 3 0 0 0 3-3V5a3 3 0 0 0-6 0v6a3 3 0 0 0 3 3zm5-3a5 5 0 0 1-10 0H5a7 7 0 0 0 6 6.9V21h2v-3.1a7 7 0 0 0 6-6.9z',
  tv: 'M21 3H3a2 2 0 0 0-2 2v12a2 2 0 0 0 2 2h5v2h8v-2h5a2 2 0 0 0 2-2V5a2 2 0 0 0-2-2zm0 14H3V5h18z',
};

export function icon(name, label) {
  const svg = document.createElementNS('http://www.w3.org/2000/svg', 'svg');
  svg.setAttribute('viewBox', '0 0 24 24');
  svg.setAttribute('class', 'icon');
  svg.setAttribute('aria-hidden', 'true');
  const path = document.createElementNS('http://www.w3.org/2000/svg', 'path');
  path.setAttribute('d', ICONS[name] || '');
  svg.append(path);
  if (!label) return svg;
  const wrap = document.createElement('span');
  wrap.append(svg, Object.assign(document.createElement('span'), { className: 'sr', textContent: label }));
  return wrap;
}

// lyricLines turns plain lyrics into one paragraph per line sung; the blank line between
// verses becomes a gap.
export function lyricLines(text) {
  return String(text || '').split('\n').map((line) => (line.trim() ? el('p', { text: line.trim() }) : el('p', { class: 'gap' })));
}
