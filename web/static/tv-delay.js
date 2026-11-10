// Holding a video's picture back to meet its sound. Once a song's sound goes through Web
// Audio (to change its key), it reaches the speakers later than the browser expects: the key
// shifter needs a moment, and the browser syncs the picture as if Web Audio's own output
// delay were zero. So the picture runs ahead of the music. This draws each frame on a canvas
// over the video, that much later.

const MIN_SECONDS = 0.005; // below this, nobody can tell: show the video itself

export class PictureDelay {
  constructor(video, canvas) {
    this.video = video;
    this.canvas = canvas;
    this.g = canvas.getContext('2d');
    this.seconds = 0;
    this.frames = []; // { at: when the video would have shown it (ms), bitmap }
    this.running = false;
    this.pending = 0; // frames being copied
  }

  get active() {
    return this.running;
  }

  // set holds the picture back by seconds; 0 shows the video as it is.
  set(seconds) {
    this.seconds = Number.isFinite(seconds) ? Math.max(0, seconds) : 0;
    if (this.seconds >= MIN_SECONDS && !this.running) this.start();
    else if (this.seconds < MIN_SECONDS && this.running) this.stop();
  }

  start() {
    if (!('requestVideoFrameCallback' in HTMLVideoElement.prototype)) return; // shown as it is
    this.running = true;
    this.canvas.hidden = false;
    const onFrame = (now, meta) => {
      if (!this.running) return;
      this.copy(meta.expectedDisplayTime || now);
      this.video.requestVideoFrameCallback(onFrame);
    };
    this.video.requestVideoFrameCallback(onFrame);
    const tick = (now) => {
      if (!this.running) return;
      this.show(now);
      requestAnimationFrame(tick);
    };
    requestAnimationFrame(tick);
  }

  stop() {
    this.running = false;
    this.canvas.hidden = true;
    for (const f of this.frames) f.bitmap.close();
    this.frames = [];
    this.shown = null;
  }

  async copy(at) {
    if (!this.video.videoWidth || this.pending > 4) return; // a slow machine: skip a frame
    this.pending++;
    try {
      const bitmap = await createImageBitmap(this.video);
      if (this.running) this.frames.push({ at, bitmap });
      else bitmap.close();
    } catch {
      // The frame went away (a new song): the next one will do.
    } finally {
      this.pending--;
    }
  }

  // show draws the newest frame whose time has come, and lets go of the ones before it.
  show(now) {
    const due = now - this.seconds * 1000;
    let pick = -1;
    for (let i = 0; i < this.frames.length && this.frames[i].at <= due; i++) pick = i;
    if (pick < 0) return;
    const { bitmap } = this.frames[pick];
    for (const f of this.frames.splice(0, pick)) f.bitmap.close();
    // The drawn frame stays first until a later one is due, so a pause keeps its picture.
    if (bitmap === this.shown) return;
    this.shown = bitmap;
    if (this.canvas.width !== bitmap.width || this.canvas.height !== bitmap.height) {
      this.canvas.width = bitmap.width;
      this.canvas.height = bitmap.height;
    }
    this.g.drawImage(bitmap, 0, 0);
  }
}
