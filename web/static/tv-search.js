// One-screen use: search and the queue on the TV page itself, for a computer that is both
// the screen and the remote. Driven by the keyboard (/ opens it), or a mouse.
import { api, el, icon, save, store } from './live.js';
import { addedMessage, badge, canPlay, queueSong, searchLibrary, searchYouTube, songMeta, thumb } from './songs.js';

const $ = (root, sel) => root.querySelector(sel);

export class Finder {
  // features returns the server's current features; notify shows a short message.
  constructor(root, { features, notify, onClose }) {
    this.root = root;
    this.features = features;
    this.notify = notify;
    this.onClose = onClose;
    this.open = false;
    this.more = '';
    this.query = '';
    this.state = null;
    this.q = $(root, '#finder-q');
    this.singer = $(root, '#finder-singer');
    this.karaoke = $(root, '#finder-karaoke');
    this.results = $(root, '#finder-results');
    this.queue = $(root, '#finder-queue');
    this.status = $(root, '#finder-status');

    this.singer.value = store('singer', '');
    this.singer.onchange = () => save('singer', this.singer.value.trim());
    this.karaoke.checked = store('karaoke', true);
    this.karaoke.onchange = () => save('karaoke', this.karaoke.checked);
    $(root, '#finder-form').onsubmit = (e) => {
      e.preventDefault();
      this.search(false);
    };
    // A click outside the box closes it, as Esc does.
    root.addEventListener('mousedown', (e) => { if (e.target === root) this.hide(); });
    root.addEventListener('keydown', (e) => this.key(e));
  }

  show() {
    this.open = true;
    this.root.hidden = false;
    this.renderQueue();
    this.q.focus();
    this.q.select();
  }

  hide() {
    if (!this.open) return;
    this.open = false;
    this.root.hidden = true;
    document.activeElement?.blur();
    this.onClose?.();
  }

  // update follows the room, so the queue column is always current.
  update(state) {
    this.state = state;
    if (this.open) this.renderQueue();
  }

  // --- Search ------------------------------------------------------------
  async search(append) {
    const features = this.features();
    if (!append) {
      this.query = this.q.value.trim();
      if (!this.query) return;
      this.more = '';
    }
    this.status.textContent = 'Searching…';
    const fromLibrary = append ? Promise.resolve([]) : searchLibrary(this.query, features).catch(() => []);
    let page;
    try {
      page = await searchYouTube({ query: this.query, more: append ? this.more : '', karaoke: this.karaoke.checked });
    } catch (err) {
      page = { results: [], error: err.message };
    }
    const library = await fromLibrary;
    if (!append) this.results.replaceChildren();
    this.results.querySelector('.more')?.remove();
    if (library.length) this.results.append(el('h3', { text: 'On this computer' }), ...library.map((s) => this.row(s)));
    if (page.results.length) {
      if (!append && library.length) this.results.append(el('h3', { text: 'YouTube' }));
      this.results.append(...page.results.map((s) => this.row(s)));
    }
    this.more = page.more || '';
    if (this.more && page.results.length) {
      this.results.append(el('div', { class: 'row more', tabindex: '-1', onclick: () => this.search(true) }, 'More results'));
    }
    const rows = this.rows(this.results);
    if (page.error && !rows.length) this.status.textContent = `Search failed: ${page.error}`;
    else if (!rows.length) this.status.textContent = 'Nothing found. Try fewer words, or turn off "Karaoke versions only".';
    else this.status.textContent = '';
    if (!append && rows.length) this.focusRow(rows[0]);
  }

  row(song) {
    const features = this.features();
    const playable = canPlay(song, features);
    const note = badge(song, features);
    const row = el('div', { class: `row${playable ? '' : ' blocked'}`, tabindex: '-1', role: 'option' },
      thumb(song),
      el('div', { class: 'body' },
        el('div', { class: 'title', text: song.title }),
        el('div', { class: 'meta', text: songMeta(song) }),
        note && el('span', { class: `badge ${note.kind}`, text: note.text })),
      el('span', { class: 'added' }),
      el('div', { class: 'actions' },
        el('button', { type: 'button', tabindex: '-1', title: 'Add (Enter)', disabled: !playable, onclick: (e) => { e.stopPropagation(); this.add(row, false); } }, icon('plus')),
        el('button', { type: 'button', tabindex: '-1', title: 'Sing next (Shift+Enter)', disabled: !playable, onclick: (e) => { e.stopPropagation(); this.add(row, true); } }, icon('top'))));
    row.song = song;
    row.onclick = () => this.add(row, false);
    return row;
  }

  async add(row, next) {
    if (!canPlay(row.song, this.features())) {
      this.notify("This video can't play here.");
      return;
    }
    try {
      const added = await queueSong(row.song, this.singer.value.trim(), next);
      const message = addedMessage(added);
      // Said on the row itself: the queue column shows it arrive too.
      row.querySelector('.added').textContent = `✓ ${message}`;
      row.classList.add('done');
    } catch (err) {
      this.notify(err.message);
    }
  }

  // --- Queue -------------------------------------------------------------
  renderQueue() {
    const st = this.state;
    const upcoming = st?.queue || [];
    const focused = document.activeElement?.closest?.('#finder-queue .row')?.qid;
    $(this.root, '#finder-count').textContent = upcoming.length ? `(${upcoming.length})` : '';
    const now = st?.current
      ? el('div', { class: 'now' }, el('span', { class: 'label', text: 'Now' }), ` ${st.current.song.title}`, st.current.singer ? el('span', { class: 'who', text: ` · ${st.current.singer}` }) : null)
      : el('div', { class: 'now empty', text: 'Nothing playing yet' });
    const rows = upcoming.map((entry, i) => {
      const row = el('div', { class: 'row', tabindex: '-1' },
        el('span', { class: 'n', text: String(i + 1) }),
        el('div', { class: 'body' },
          el('div', { class: 'title', text: entry.song.title }),
          el('div', { class: 'meta', text: entry.singer || '' })),
        el('div', { class: 'actions' },
          el('button', { type: 'button', tabindex: '-1', title: 'Remove (Delete)', onclick: () => this.remove(entry) }, icon('close'))));
      row.qid = entry.qid;
      row.index = i;
      return row;
    });
    this.queue.replaceChildren(now, ...(rows.length ? rows : [el('p', { class: 'empty', text: 'The queue is empty.' })]));
    const again = rows.find((r) => r.qid === focused);
    if (again) this.focusRow(again);
  }

  remove(entry) {
    api(`/api/queue/${encodeURIComponent(entry.qid)}`, { method: 'DELETE' }).catch((err) => this.notify(err.message));
  }

  move(row, delta) {
    const to = row.index + delta;
    if (to < 0 || to >= (this.state?.queue.length || 0)) return;
    api(`/api/queue/${encodeURIComponent(row.qid)}/move`, { method: 'POST', body: { to } }).catch((err) => this.notify(err.message));
  }

  // --- Keys --------------------------------------------------------------
  rows(list) {
    return [...list.querySelectorAll('.row')];
  }

  focusRow(row) {
    for (const r of row.parentElement.querySelectorAll('.row')) r.tabIndex = r === row ? 0 : -1;
    row.focus();
    row.scrollIntoView({ block: 'nearest' });
  }

  key(e) {
    e.stopPropagation(); // the TV's own shortcuts are off while the finder is open
    if (e.key === 'Escape') {
      e.preventDefault();
      this.hide();
      return;
    }
    const row = e.target.closest?.('.row');
    if (e.target === this.q && e.key === 'ArrowDown') {
      e.preventDefault();
      const first = this.rows(this.results)[0];
      if (first) this.focusRow(first);
      return;
    }
    if (!row) return;
    const list = row.parentElement;
    const rows = this.rows(list);
    const i = rows.indexOf(row);
    const inQueue = list === this.queue;
    if (e.key === 'ArrowDown' || e.key === 'ArrowUp') {
      e.preventDefault();
      const delta = e.key === 'ArrowDown' ? 1 : -1;
      if (inQueue && e.shiftKey) return this.move(row, delta);
      if (rows[i + delta]) this.focusRow(rows[i + delta]);
      else if (delta < 0 && !inQueue) this.q.focus();
      return;
    }
    if (e.key === 'ArrowRight' && !inQueue) {
      const first = this.rows(this.queue)[0];
      if (first) { e.preventDefault(); this.focusRow(first); }
      return;
    }
    if (e.key === 'ArrowLeft' && inQueue) {
      const first = this.rows(this.results)[0];
      e.preventDefault();
      if (first) this.focusRow(first);
      else this.q.focus();
      return;
    }
    if (e.key === 'Enter') {
      e.preventDefault();
      if (row.classList.contains('more')) this.search(true);
      else if (!inQueue) this.add(row, e.shiftKey);
      return;
    }
    if (inQueue && (e.key === 'Delete' || e.key === 'Backspace')) {
      e.preventDefault();
      const entry = this.state?.queue[row.index];
      if (entry) this.remove(entry);
      return;
    }
    // Typing on a result goes back to the search box: the key lands there.
    if (e.key.length === 1 && !e.ctrlKey && !e.metaKey && !e.altKey && !inQueue) this.q.focus();
  }
}
