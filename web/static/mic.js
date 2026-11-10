// The singer's mic through the browser: a live vocal chain (high-pass, pitch correction,
// tone, compressor), a doubler, the karaoke echo and a reverb, a limiter, a feedback guard,
// and a probe that measures how late the singer hears themselves. When the server is on this
// computer and has PipeWire, mic-native.js does all this there instead, much faster.
import { CHAIN, HowlGuard, impulse, medianMs, normalize, params, tunePlan } from './mic-core.js';
import { audioContext } from './audio.js';

// When the mic howls it is turned down by 40%, at most every 3 s and never below 25%: real
// feedback stops once the gain drops, and a steady tone that is not feedback (a whistle, a
// synth note from the speakers) should not end up muting the singer.
const HOWL_CUT = 0.6;
const HOWL_FLOOR = 25;
const HOWL_COOLDOWN_MS = 3000;

export class Mic {
  constructor({ onChange = () => {}, onHowl = () => {} } = {}) {
    this.settings = normalize();
    this.onChange = onChange;
    this.onHowl = onHowl;
    this.ctx = null;
    this.stream = null;
    this.nodes = null;
    this.level = 0;
    this.latencyMs = null;
    this.error = '';
    this.device = '';
  }

  // supported says why the mic can't work here, or '' when it can.
  static unsupported() {
    if (!window.isSecureContext) return 'The mic needs this page on localhost or HTTPS.';
    if (!navigator.mediaDevices?.getUserMedia) return 'This browser has no mic access.';
    return '';
  }

  async devices() {
    const all = await navigator.mediaDevices.enumerateDevices();
    return all.filter((d) => d.kind === 'audioinput');
  }

  // context makes the AudioContext on first use; it must follow a click, or it stays silent.
  context() {
    if (!this.ctx) {
      this.ctx = audioContext();
      this.build();
    }
    if (this.ctx.state === 'suspended') this.ctx.resume();
    return this.ctx;
  }

  build() {
    const ctx = this.ctx;
    const biquad = (type, frequency, gain = 0, Q = 0.7) => {
      const f = ctx.createBiquadFilter();
      Object.assign(f, { type });
      f.frequency.value = frequency;
      f.gain.value = gain;
      f.Q.value = Q;
      return f;
    };
    const compressor = (o) => {
      const c = ctx.createDynamicsCompressor();
      for (const [k, v] of Object.entries(o)) c[k].value = v;
      return c;
    };
    const n = {
      input: ctx.createGain(),
      highpass: biquad('highpass', CHAIN.highpassHz),
      tuneIn: ctx.createGain(), // pitch correction goes between these two once it is wanted
      tuneOut: ctx.createGain(),
      warmth: biquad('lowshelf', CHAIN.warmthHz),
      mud: biquad('peaking', CHAIN.mudHz, CHAIN.mudGainDb, 1),
      presence: biquad('peaking', CHAIN.presenceHz, CHAIN.presenceGainDb, 0.8),
      bright: biquad('highshelf', CHAIN.brightHz),
      compressor: compressor(CHAIN.compressor),
      mic: ctx.createGain(),
      doubleSend: ctx.createGain(),
      doubleSides: ctx.createChannelMerger(2),
      echoSend: ctx.createGain(),
      echoDelay: ctx.createDelay(1),
      echoFeedback: ctx.createGain(),
      echoHighpass: biquad('highpass', CHAIN.echoHighpassHz),
      echoLowpass: biquad('lowpass', CHAIN.echoLowpassHz),
      reverbSend: ctx.createGain(),
      reverbPreDelay: ctx.createDelay(0.2),
      reverb: ctx.createConvolver(),
      reverbHighpass: biquad('highpass', CHAIN.reverbHighpassHz),
      master: ctx.createGain(),
      limiter: compressor(CHAIN.limiter),
      meter: ctx.createAnalyser(),
    };
    const ir = impulse(ctx.sampleRate, CHAIN.reverbSeconds);
    const buffer = ctx.createBuffer(2, ir[0].length, ctx.sampleRate);
    ir.forEach((data, ch) => buffer.copyToChannel(data, ch));
    n.reverb.buffer = buffer;
    n.reverbPreDelay.delayTime.value = CHAIN.reverbPreDelay;
    n.meter.fftSize = 2048;

    // dry: input -> high-pass -> (tune) -> tone and EQ -> compressor -> mic level -> master
    n.input.connect(n.highpass).connect(n.tuneIn).connect(n.tuneOut);
    n.tuneOut.connect(n.warmth).connect(n.mud).connect(n.presence).connect(n.bright).connect(n.compressor).connect(n.mic).connect(n.master);
    // double: two copies 10 to 30 ms late, each drifting slowly (a few cents of pitch), one per side
    n.mic.connect(n.doubleSend);
    n.doublers = [];
    CHAIN.doubler.forEach((side, i) => {
      const delay = ctx.createDelay(0.1);
      delay.delayTime.value = side.ms / 1000;
      const lfo = ctx.createOscillator();
      const depth = ctx.createGain();
      lfo.frequency.value = side.hz;
      depth.gain.value = side.depthMs / 1000;
      lfo.connect(depth).connect(delay.delayTime);
      lfo.start();
      n.doubleSend.connect(delay).connect(n.doubleSides, 0, i);
      n.doublers.push(delay, lfo, depth);
    });
    n.doubleSides.connect(n.master);
    // echo: one delay feeding itself through the filters, so each repeat is darker and quieter
    n.mic.connect(n.echoSend).connect(n.echoDelay).connect(n.echoHighpass).connect(n.echoLowpass);
    n.echoLowpass.connect(n.echoFeedback).connect(n.echoDelay);
    n.echoLowpass.connect(n.master);
    // reverb
    n.mic.connect(n.reverbSend).connect(n.reverbPreDelay).connect(n.reverb).connect(n.reverbHighpass).connect(n.master);
    // out
    n.master.connect(n.limiter).connect(ctx.destination);
    // The meter and the feedback guard listen before the compressor, which would otherwise
    // hold a howl under the guard's threshold.
    n.bright.connect(n.meter);
    this.nodes = n;
    this.apply();
  }

  apply() {
    if (!this.nodes) return;
    const p = params(this.settings);
    const t = this.ctx.currentTime;
    const n = this.nodes;
    n.input.gain.setTargetAtTime(p.boostGain, t, 0.02);
    n.mic.gain.setTargetAtTime(this.settings.on ? p.micGain : 0, t, 0.02);
    n.echoSend.gain.setTargetAtTime(p.echoSend, t, 0.05);
    n.echoFeedback.gain.setTargetAtTime(p.echoFeedback, t, 0.05);
    n.echoDelay.delayTime.setTargetAtTime(p.echoTime, t, 0.05);
    n.reverbSend.gain.setTargetAtTime(p.reverbSend, t, 0.05);
    n.warmth.gain.setTargetAtTime(p.warmthDb, t, 0.05);
    n.bright.gain.setTargetAtTime(p.brightDb, t, 0.05);
    n.doubleSend.gain.setTargetAtTime(p.doubleMix, t, 0.05);
    this.tune(tunePlan(p.tune, p.speed, p.notes));
  }

  // tune sets pitch correction, loading its worklet the first time it is wanted.
  async tune(plan) {
    if (this.tuner) {
      this.tuner.port.postMessage({ plan });
      return;
    }
    if (!plan.amount || this.tunerLoading) return;
    this.tunerLoading = true;
    try {
      await this.ctx.audioWorklet.addModule('/static/tune-worklet.js');
      const n = this.nodes;
      if (!n) return; // let go of while it loaded
      this.tuner = new AudioWorkletNode(this.ctx, 'pitch-tuner', { numberOfInputs: 1, numberOfOutputs: 1, outputChannelCount: [1] });
      n.tuneIn.disconnect();
      n.tuneIn.connect(this.tuner).connect(n.tuneOut);
      const p = params(this.settings);
      this.tuner.port.postMessage({ plan: tunePlan(p.tune, p.speed, p.notes) });
    } catch (err) {
      this.error = `Pitch correction: ${err.message}`;
      this.onChange(this);
    } finally {
      this.tunerLoading = false;
    }
  }

  async set(changes) {
    const before = this.settings;
    this.settings = normalize({ ...before, ...changes });
    const deviceChanged = this.settings.deviceId !== before.deviceId;
    if (this.settings.on && (!this.stream || deviceChanged)) {
      await this.open();
    } else if (!this.settings.on && this.stream) {
      this.close();
    }
    this.apply();
    this.onChange(this);
  }

  async open() {
    const why = Mic.unsupported();
    if (why) {
      this.fail(why);
      return;
    }
    this.close();
    const ctx = this.context();
    const audio = {
      // Call processing hears a held note as noise and pumps the level: all of it off.
      echoCancellation: false,
      noiseSuppression: false,
      autoGainControl: false,
      latency: 0,
      channelCount: 1,
    };
    if (this.settings.deviceId) audio.deviceId = { exact: this.settings.deviceId };
    try {
      this.stream = await navigator.mediaDevices.getUserMedia({ audio });
    } catch (err) {
      if (this.settings.deviceId && err.name === 'OverconstrainedError') {
        this.settings = { ...this.settings, deviceId: '' }; // that mic is gone; take the default
        return this.open();
      }
      this.fail(err.name === 'NotAllowedError' ? 'Mic permission was refused.' : `Mic: ${err.message}`);
      return;
    }
    const track = this.stream.getAudioTracks()[0];
    this.device = track?.label || 'Mic';
    this.error = '';
    this.source = ctx.createMediaStreamSource(this.stream);
    this.source.connect(this.nodes.input);
    this.watch();
  }

  close() {
    this.source?.disconnect();
    this.source = null;
    for (const track of this.stream?.getTracks() || []) track.stop();
    this.stream = null;
    this.level = 0;
    cancelAnimationFrame(this.watching);
  }

  // dispose lets go of everything: the mic, and the whole chain in the page's audio, which
  // otherwise keeps running (its oscillators and the pitch worklet never stop by themselves).
  dispose() {
    this.close();
    if (this.tuner) {
      this.tuner.port.postMessage({ stop: true });
      this.tuner.disconnect();
      this.tuner = null;
    }
    const n = this.nodes;
    if (!n) return;
    for (const node of [...Object.values(n), ...n.doublers]) {
      if (!(node instanceof AudioNode)) continue;
      if (node instanceof OscillatorNode) node.stop();
      node.disconnect();
    }
    this.nodes = null;
  }

  fail(message) {
    this.error = message;
    this.settings = { ...this.settings, on: false };
    this.close();
    this.apply();
    this.onChange(this);
  }

  // watch reads the level for the meter and turns the mic down when it starts to howl.
  watch() {
    const meter = this.nodes.meter;
    const wave = new Float32Array(meter.fftSize);
    const spectrum = new Float32Array(meter.frequencyBinCount);
    const guard = new HowlGuard();
    let quietUntil = 0;
    const tick = () => {
      if (!this.stream) return;
      meter.getFloatTimeDomainData(wave);
      let peak = 0;
      for (const x of wave) peak = Math.max(peak, Math.abs(x));
      this.level = peak;
      meter.getFloatFrequencyData(spectrum);
      const now = performance.now();
      if (this.settings.on && guard.update(spectrum, peak, now) && now >= quietUntil) {
        quietUntil = now + HOWL_COOLDOWN_MS;
        const volume = Math.max(HOWL_FLOOR, Math.round(this.settings.volume * HOWL_CUT));
        if (volume < this.settings.volume) {
          this.set({ volume });
          this.onHowl(this);
        }
      }
      this.watching = requestAnimationFrame(tick);
    };
    this.watching = requestAnimationFrame(tick);
  }

  // measure plays three clicks and times each one's return through the mic.
  async measure() {
    if (!this.stream) await this.set({ on: true });
    if (!this.stream) return null;
    const ctx = this.ctx;
    if (!this.probeLoaded) {
      await ctx.audioWorklet.addModule('/static/mic-worklet.js');
      this.probeLoaded = true;
    }
    const probe = new AudioWorkletNode(ctx, 'delay-probe', { numberOfInputs: 1, numberOfOutputs: 1, outputChannelCount: [2] });
    this.source.connect(probe);
    probe.connect(ctx.destination);
    const once = () => new Promise((resolve) => {
      probe.port.onmessage = (e) => resolve(e.data.seconds);
      probe.port.postMessage({ type: 'measure' });
    });
    const tries = [];
    for (let i = 0; i < 3; i++) {
      tries.push(await once());
      await new Promise((r) => setTimeout(r, 250));
    }
    probe.disconnect();
    this.source?.disconnect(probe);
    this.latencyMs = medianMs(tries);
    this.onChange(this);
    return this.latencyMs;
  }

  // status is what the TV reports for the phones.
  status() {
    const s = this.settings;
    return {
      on: s.on,
      volume: s.volume,
      echo: s.echo,
      reverb: s.reverb,
      warmth: s.warmth,
      bright: s.bright,
      double: s.double,
      tune: s.tune,
      boost: s.boost,
      speed: s.speed,
      scale: s.scale,
      engine: 'browser',
      device: this.device,
      latencyMs: this.latencyMs ?? 0,
      outputMs: this.ctx ? Math.round(((this.ctx.baseLatency || 0) + (this.ctx.outputLatency || 0)) * 1000) : 0,
      error: this.error,
    };
  }
}
