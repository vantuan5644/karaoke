// MP3+G on the TV: the graphics drawn from the .cdg file in step with the audio, which plays
// in the page's <video> element like any other copy.
import CDGraphics from './vendor/cdgraphics.js';

export class CDGView {
  constructor(canvas, media, stage) {
    this.canvas = canvas;
    this.ctx = canvas.getContext('2d');
    this.media = media;
    this.stage = stage;
    this.cdg = null;
    this.url = null;
    this.frame = 0;
    this.delay = 0; // seconds the sound trails the media clock by (a key change)
  }

  // load shows the graphics at url; null hides them.
  async load(url) {
    if (url === this.url) return;
    this.stop();
    this.url = url;
    if (!url) return;
    const res = await fetch(url);
    if (!res.ok) throw new Error(`the graphics: ${res.status}`);
    const data = await res.arrayBuffer();
    if (this.url !== url) return; // another song started meanwhile
    this.cdg = new CDGraphics(data);
    this.canvas.hidden = false;
    const tick = () => {
      this.frame = requestAnimationFrame(tick);
      this.draw();
    };
    tick();
  }

  draw() {
    const frame = this.cdg.render(Math.max(0, this.media.currentTime - this.delay));
    if (!frame.isChanged) return;
    this.ctx.putImageData(frame.imageData, 0, 0);
    // Letterboxing in the song's own background colour, so the picture has no frame.
    const [r, g, b] = frame.backgroundRGBA;
    this.stage.style.backgroundColor = `rgb(${r} ${g} ${b})`;
  }

  stop() {
    cancelAnimationFrame(this.frame);
    this.cdg = null;
    this.url = null;
    this.canvas.hidden = true;
    this.stage.style.backgroundColor = '';
  }
}
