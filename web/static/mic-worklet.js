// Measures the round trip a singer hears: this processor plays a click through its output and
// listens for it on its input (the mic), counting frames in between. Both happen in the same
// render callback, so the figure covers the output buffer, the speakers, the air, the mic and
// the input buffer: all of the delay between singing and hearing it back.

const CLICK_SECONDS = 0.003;
const CLICK_HZ = 2500;

class DelayProbe extends AudioWorkletProcessor {
  constructor() {
    super();
    this.state = 'idle';
    this.port.onmessage = (e) => {
      if (e.data?.type !== 'measure') return;
      // Listen to the room before clicking, but only after the last click's tail has died
      // away: 0.25 s to settle, then 0.15 s to hear what the room sounds like.
      this.state = 'quiet';
      this.noiseFrom = currentFrame + Math.round(sampleRate * 0.25);
      this.quietUntil = this.noiseFrom + Math.round(sampleRate * 0.15);
      this.noise = 0;
    };
  }

  process(inputs, outputs) {
    const input = inputs[0]?.[0];
    const output = outputs[0];
    for (let j = 0; j < output[0].length; j++) {
      const frame = currentFrame + j;
      const x = input ? input[j] : 0;
      let y = 0;
      if (this.state === 'quiet') {
        if (frame >= this.noiseFrom) this.noise = Math.max(this.noise, Math.abs(x));
        if (frame >= this.quietUntil) {
          // Loud enough to stand out from the room, never below a floor that noise could hit.
          this.threshold = Math.min(0.6, Math.max(0.03, this.noise * 4));
          this.clickAt = frame;
          this.state = 'click';
        }
      }
      if (this.state === 'click' || this.state === 'listen') {
        const t = (frame - this.clickAt) / sampleRate;
        if (t < CLICK_SECONDS) {
          const window = Math.sin((Math.PI * t) / CLICK_SECONDS);
          y = 0.8 * window * Math.sin(2 * Math.PI * CLICK_HZ * t);
        }
        this.state = 'listen';
        // Anything before 1 ms is the click leaking straight through the graph, not the room.
        if (t > 0.001 && Math.abs(x) > this.threshold) {
          this.port.postMessage({ type: 'result', seconds: t });
          this.state = 'idle';
        } else if (t > 0.8) {
          this.port.postMessage({ type: 'result', seconds: null });
          this.state = 'idle';
        }
      }
      for (const channel of output) channel[j] = y;
    }
    return true;
  }
}

registerProcessor('delay-probe', DelayProbe);
