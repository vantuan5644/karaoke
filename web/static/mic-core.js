// The mic's settings and the arithmetic behind them, kept apart from the Web Audio graph so
// they can be tested without a browser.

export const DEFAULTS = Object.freeze({
  on: false, volume: 100, echo: 35, reverb: 25,
  // Voice tuning, all off to start: warmth (low-mid body), bright (presence and air),
  // double (a second voice beside the first) and tune (pitch correction).
  warmth: 0, bright: 0, double: 0, tune: 0,
  deviceId: '', // the browser's mic
  nativeDevice: '', // the server's PipeWire mic, by node name
  lowLatency: true, // use the server's PipeWire mic when this page is on its computer
});

// The knobs a singer turns, with their ranges, in the order the panels show them.
export const KNOBS = Object.freeze({
  volume: { label: 'Mic volume', max: 200, step: 5 },
  echo: { label: 'Echo', max: 100, step: 5 },
  reverb: { label: 'Reverb', max: 100, step: 5 },
  warmth: { label: 'Warmth', max: 100, step: 5 },
  bright: { label: 'Bright', max: 100, step: 5 },
  double: { label: 'Double', max: 100, step: 5 },
  tune: { label: 'Tune', max: 100, step: 5 },
});

// Presets a singer can pick from without knowing what a compressor is.
export const PRESETS = Object.freeze({
  dry: { echo: 0, reverb: 10 },
  ktv: { echo: 55, reverb: 25 },
  hall: { echo: 15, reverb: 60 },
});

const clamp = (v, lo, hi) => Math.max(lo, Math.min(hi, Number.isFinite(v) ? v : lo));

// normalize keeps stored or remote-sent settings inside what the chain accepts.
export function normalize(s = {}) {
  const out = { on: Boolean(s.on) };
  for (const [k, { max }] of Object.entries(KNOBS)) out[k] = Math.round(clamp(s[k] ?? DEFAULTS[k], 0, max));
  out.deviceId = typeof s.deviceId === 'string' ? s.deviceId : '';
  out.nativeDevice = typeof s.nativeDevice === 'string' ? s.nativeDevice : '';
  out.lowLatency = s.lowLatency === undefined ? DEFAULTS.lowLatency : Boolean(s.lowLatency);
  return out;
}

// params turns the three knobs a singer sees into the chain's numbers. The echo is the
// karaoke-machine kind: one delay with feedback, high-passed so its tail stays out of the mud.
// Feedback stays well under 1, or the repeats would build into a loop.
export function params(s) {
  const n = normalize(s);
  return {
    micGain: n.volume / 100,
    echoSend: (n.echo / 100) * 0.7,
    echoFeedback: 0.12 + (n.echo / 100) * 0.33,
    echoTime: 0.16,
    reverbSend: (n.reverb / 100) * 0.85,
    warmthDb: (n.warmth / 100) * CHAIN.warmthMaxDb,
    brightDb: (n.bright / 100) * CHAIN.brightMaxDb,
    doubleMix: (n.double / 100) * 0.7,
    tune: n.tune,
  };
}

// tunePlan is the Tune knob's arithmetic (the same as the Go engine's tuneParams): how much
// of the way to the nearest note to pull (0..1), and how quickly. Low settings pull a little
// and slowly, keeping slides and vibrato; 100 snaps at once, the robotic effect.
export function tunePlan(tune) {
  const s = clamp(tune, 0, 100) / 100;
  if (s === 0) return { amount: 0, seconds: 0 };
  return { amount: Math.min(1, s / 0.6), seconds: 0.3 * (1 - s) * (1 - s) + 0.005 };
}

// TUNE_DELAY_MS is what pitch correction adds while it is on (the Go engine's DelayMs).
export const TUNE_DELAY_MS = 8;

// The fixed part of the chain: a live vocal chain in the usual order.
export const CHAIN = Object.freeze({
  highpassHz: 90, // rumble, handling noise and plosives
  warmthHz: 220, warmthMaxDb: 6, // low shelf
  mudHz: 300, mudGainDb: -3,
  presenceHz: 4000, presenceGainDb: 3,
  brightHz: 5000, brightMaxDb: 8, // high shelf
  // The doubler: two copies a little late, each drifting slowly, one per side.
  doubler: [{ ms: 14, depthMs: 2.5, hz: 0.31 }, { ms: 23, depthMs: 3, hz: 0.47 }],
  compressor: { threshold: -24, knee: 6, ratio: 3, attack: 0.003, release: 0.15 },
  echoHighpassHz: 250, echoLowpassHz: 4500,
  reverbSeconds: 1.2, reverbPreDelay: 0.025, reverbHighpassHz: 200,
  limiter: { threshold: -3, knee: 0, ratio: 20, attack: 0.001, release: 0.05 },
});

// impulse fills a stereo reverb impulse response: decaying noise, a little different per
// channel so the room sounds wide.
export function impulse(sampleRate, seconds, random = Math.random) {
  const length = Math.max(1, Math.round(sampleRate * seconds));
  const channels = [new Float32Array(length), new Float32Array(length)];
  for (const data of channels) {
    for (let i = 0; i < length; i++) {
      data[i] = (random() * 2 - 1) * Math.pow(1 - i / length, 3);
    }
  }
  return channels;
}

// howlScore says how much a spectrum looks like feedback: the share of its energy in one
// narrow peak (0 to 1), and which bin that peak is in. spectrum is an AnalyserNode's
// getFloatFrequencyData, in decibels.
export function howlScore(spectrum) {
  let peak = -Infinity;
  let bin = 0;
  for (let i = 2; i < spectrum.length; i++) {
    if (spectrum[i] > peak) {
      peak = spectrum[i];
      bin = i;
    }
  }
  if (peak < -25) return { score: 0, bin }; // too quiet to be feedback
  let total = 0;
  let near = 0;
  for (let i = 2; i < spectrum.length; i++) {
    const power = Math.pow(10, spectrum[i] / 10);
    total += power;
    if (Math.abs(i - bin) <= 2) near += power;
  }
  return { score: total > 0 ? near / total : 0, bin };
}

// HowlGuard tells feedback from a held note. Both put most of their energy in one narrow
// peak; feedback is also loud and stays on exactly one frequency, so the guard waits for a
// loud, narrow peak that does not move for a full second.
export class HowlGuard {
  constructor({ score = 0.7, level = 0.5, holdMs = 1000 } = {}) {
    Object.assign(this, { minScore: score, minLevel: level, holdMs });
    this.reset();
  }

  reset() {
    this.since = 0;
    this.bin = -1;
  }

  // update takes one look (spectrum, peak sample level, time in ms) and answers true when it
  // is time to turn the mic down.
  update(spectrum, level, now) {
    const { score, bin } = howlScore(spectrum);
    if (score < this.minScore || level < this.minLevel) {
      this.reset();
      return false;
    }
    if (this.bin < 0 || Math.abs(bin - this.bin) > 1) {
      this.since = now;
      this.bin = bin;
      return false;
    }
    if (now - this.since < this.holdMs) return false;
    this.reset();
    return true;
  }
}

// The delay probe's answers, reduced to one figure: the median of the tries that heard the
// click, in milliseconds, or null when none did.
export function medianMs(seconds) {
  const heard = seconds.filter((s) => Number.isFinite(s) && s > 0).sort((a, b) => a - b);
  if (heard.length === 0) return null;
  const mid = Math.floor(heard.length / 2);
  const s = heard.length % 2 ? heard[mid] : (heard[mid - 1] + heard[mid]) / 2;
  return Math.round(s * 1000);
}

// latencyVerdict says what a measured round trip means for someone singing.
export function latencyVerdict(ms) {
  if (ms === null) return 'No click heard: turn the speakers up or move the mic closer, then try again.';
  if (ms <= 25) return `${ms} ms: feels natural.`;
  if (ms <= 45) return `${ms} ms: noticeable; the echo hides some of it.`;
  return `${ms} ms: too slow to sing with comfortably. Use headphones on a mixer, or lower the system audio buffer.`;
}
