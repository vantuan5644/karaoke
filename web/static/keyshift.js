// Changing a song's key: the downloaded copy's sound goes through Signalsmith Stretch, which
// shifts pitch and keeps the tempo. Only a download can do this, since YouTube's embedded
// player keeps its sound to itself. In the original key the sound bypasses the shifter, so it
// adds no delay.
import SignalsmithStretch from './vendor/signalsmith-stretch.mjs';
import { audioContext } from './audio.js';

// The library would build its worklet from a blob: URL, which the page's CSP refuses; the
// same module is served as a file (see THIRD_PARTY.md for how it was made).
SignalsmithStretch.moduleUrl = '/static/vendor/signalsmith-stretch-worklet.js';

export class KeyShift {
  constructor(video) {
    this.video = video;
    this.semitones = 0;
    this.source = null;
    this.stretch = null;
    this.loading = null;
  }

  // set moves the video's sound by semitones (0 for the original key).
  async set(semitones) {
    this.semitones = semitones;
    if (!this.source) {
      if (!semitones) return; // the video plays the plain way until a key change
      // Once taken, the video's sound only reaches the speakers through this graph.
      this.source = audioContext().createMediaElementSource(this.video);
    }
    if (semitones && !this.stretch) {
      this.loading ||= SignalsmithStretch(audioContext(), { numberOfInputs: 1, numberOfOutputs: 1, outputChannelCount: [2] })
        .then((node) => {
          node.connect(audioContext().destination);
          node.start();
          return node;
        });
      this.stretch = await this.loading;
    }
    this.route();
  }

  route() {
    const shift = this.semitones !== 0 && this.stretch;
    this.source.disconnect();
    if (shift) {
      this.stretch.schedule({ semitones: this.semitones });
      this.source.connect(this.stretch);
    } else {
      this.source.connect(audioContext().destination);
    }
  }

  // latency is how far the shifted sound trails the picture, in seconds.
  async latency() {
    return this.stretch && this.semitones ? this.stretch.latency() : 0;
  }
}

export function keyLabel(semitones) {
  if (!semitones) return 'Original key';
  return `Key ${semitones > 0 ? '+' : '−'}${Math.abs(semitones)}`;
}
