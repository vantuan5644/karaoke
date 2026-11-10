// The TV's lyrics panel: the plain words of the song playing, beside the video, sized so the
// whole song fits when it can. No timing: singers read ahead at their own pace.
import { api, el, lyricLines } from './live.js';

const RETRY_MS = 15000;

// The panel's width: a third of the screen, or half for a song too long to fit in that.
const NARROW = '36vw';
const WIDE = '50vw';
// What fit tries, in order: width, two columns or one, and the text sizes (vh) to try.
const LAYOUTS = [[NARROW, false, 3.6, 2.2], [NARROW, true, 2.6, 2.0], [WIDE, true, 2.6, 1.8]];

export class LyricsPanel {
  constructor(root, { onChange = () => {} } = {}) {
    this.root = root;
    this.box = root.querySelector('.text');
    this.title = root.querySelector('.title');
    this.by = root.querySelector('.by');
    this.onChange = onChange;
    this.open = false;
    this.key = '';
    // Refit when the panel appears (text that arrives while it is hidden has nothing to
    // measure against) or the window changes. Width changes are fit's own doing, so only a
    // new height counts, or the two would chase each other.
    this.fitted = 0;
    new ResizeObserver(() => this.box.clientHeight !== this.fitted && this.fit()).observe(this.box);
    addEventListener('resize', () => this.fit());
    this.box.addEventListener('scroll', () => this.markMore());
  }

  // markMore fades the bottom edge while there is more of the song below it.
  markMore() {
    const b = this.box;
    b.classList.toggle('more', b.scrollTop + b.clientHeight < b.scrollHeight - 2);
  }

  setOpen(open) {
    this.open = open;
    document.body.classList.toggle('lyrics-open', open);
    requestAnimationFrame(() => this.fit());
    this.onChange(this);
  }

  // sync fetches the lyrics when the song, or the lyrics picked for it, changes.
  sync(state) {
    const cur = state?.current;
    const key = this.open && state?.features?.lyrics && cur ? `${cur.qid}|${cur.song.lyricsId || 0}` : '';
    if (key === this.key) return;
    if (key && key === this.failedKey && Date.now() < this.retryAt) return;
    this.key = key;
    if (!key) return;
    this.show(cur.song.title, '', 'Looking for the lyrics…');
    api(`/api/lyrics?qid=${encodeURIComponent(cur.qid)}`).then((ans) => {
      if (this.key !== key) return; // the song changed meanwhile
      if (ans.off) this.show(cur.song.title, '', 'Lyrics are off for this song.');
      else if (!ans.track) this.show(cur.song.title, '', 'No lyrics found. On a phone: Lyrics, then "Wrong lyrics?" to pick some.');
      else this.show(ans.track.track, ans.track.artist, '', ans.track.lyrics);
    }).catch((err) => {
      if (this.key !== key) return;
      this.show(cur.song.title, '', `Couldn't get the lyrics: ${err.message}`);
      this.key = '';
      this.failedKey = key;
      this.retryAt = Date.now() + RETRY_MS;
    });
  }

  show(title, by, message, text) {
    this.title.textContent = title;
    this.by.textContent = by;
    this.box.replaceChildren(...(text ? lyricLines(text) : [el('p', { class: 'note', text: message })]));
    this.box.scrollTop = 0;
    requestAnimationFrame(() => this.fit());
  }

  // fit picks the largest text that shows the whole song: one column, then two, then two in
  // a wider panel. A song longer than that gets one readable column, and phones (or Page
  // Up/Down) scroll it.
  fit() {
    const box = this.box;
    if (!this.open || !box.clientHeight) return;
    const fits = () => box.scrollHeight <= box.clientHeight + 1 && box.scrollWidth <= box.clientWidth + 1;
    const layout = (width, two, vh) => {
      document.body.style.setProperty('--lyrics-w', width);
      box.classList.toggle('two', two);
      box.style.fontSize = `${vh.toFixed(1)}vh`;
    };
    this.fitted = box.clientHeight;
    box.classList.remove('more');
    for (const [width, two, from, to] of LAYOUTS) {
      for (let vh = from; vh >= to - 1e-6; vh -= 0.1) {
        layout(width, two, vh);
        if (fits()) return;
      }
    }
    layout(NARROW, false, 2.4);
    this.markMore();
  }

  scroll(direction) {
    this.box.scrollBy({ top: Math.sign(direction || 1) * this.box.clientHeight * 0.8, behavior: 'smooth' });
  }
}
