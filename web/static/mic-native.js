// The server's own PipeWire mic (internal/voice), for a TV page on the server's computer:
// the same knobs as the browser's mic (mic.js), but the voice never enters the browser, so
// the singer hears themselves about 5 ms late instead of 60 to 70.
import { api } from './live.js';
import { normalize } from './mic-core.js';

const POLL_MS = 250; // the meter, and the keep-alive that stops a forgotten mic

export class NativeMic {
  constructor({ onChange = () => {}, onHowl = () => {} } = {}) {
    this.settings = normalize();
    this.onChange = onChange;
    this.onHowl = onHowl;
    this.native = true;
    this.deviceKey = 'nativeDevice';
    this.level = 0;
    this.latencyMs = null; // measured round trip
    this.engineMs = 0; // what the engine says it adds
    this.error = '';
    this.device = '';
    this.howls = null;
  }

  unsupported() {
    return '';
  }

  async devices() {
    const { devices } = await api('/api/mic/devices');
    return devices.map((d) => ({ deviceId: d.id, label: d.default ? `${d.name} (default)` : d.name }));
  }

  context() {
    return null; // no browser audio: nothing to unlock
  }

  open() {
    return this.set({});
  }

  close() {
    clearTimeout(this.timer);
  }

  dispose() {
    this.close();
    this.disposed = true; // a reply still on its way changes nothing
  }

  async set(changes) {
    this.settings = normalize({ ...this.settings, ...changes });
    await this.push();
    this.poll();
    this.onChange(this);
  }

  // push sends the settings one request at a time, the latest each time, so a slider
  // dragged fast doesn't pile requests up or land them out of order.
  push() {
    if (this.sending) {
      this.again = true;
      return this.sending;
    }
    this.sending = (async () => {
      do {
        this.again = false;
        this.pushes = (this.pushes || 0) + 1;
        const sent = this.settings;
        // seenHowls: a change made before this page heard of a feedback cut must not undo it.
        const body = { ...sent, deviceId: sent.nativeDevice, seenHowls: this.howls ?? undefined };
        try {
          this.apply(await api('/api/mic', { method: 'POST', body }), sent);
        } catch (err) {
          this.error = `Mic: ${err.message}`;
        }
      } while (this.again);
      this.sending = null;
    })();
    return this.sending;
  }

  // apply takes the engine's status; sent is the settings it answers. It says whether
  // anything the panel shows changed.
  apply(st, sent) {
    const before = `${this.error}|${this.device}|${this.settings.on}|${this.settings.volume}`;
    this.level = st.level || 0;
    this.device = st.device || '';
    this.engineMs = st.latencyMs || 0;
    this.error = st.error || '';
    if (this.howls !== null && st.howls > this.howls) {
      this.settings = { ...this.settings, volume: st.volume };
      this.onHowl(this);
    }
    this.howls = st.howls;
    // It was asked to run and isn't: it failed, or the mic went away.
    if (sent.on && !st.on && this.settings.on === sent.on) this.settings = { ...this.settings, on: false };
    return before !== `${this.error}|${this.device}|${this.settings.on}|${this.settings.volume}`;
  }

  poll() {
    clearTimeout(this.timer);
    if (!this.settings.on) return;
    this.timer = setTimeout(async () => {
      const pushes = this.pushes;
      try {
        const info = await api('/api/mic');
        // A change sent while this was asked makes the answer stale.
        const current = !this.sending && this.pushes === pushes && !this.disposed;
        if (info.status && current && this.apply(info.status, this.settings)) this.onChange(this);
      } catch {
        // The server restarted, or is busy: the next poll tries again.
      }
      if (!this.disposed) this.poll();
    }, POLL_MS);
  }

  async measure() {
    if (!this.settings.on) await this.set({ on: true });
    if (!this.settings.on) return null;
    const { latencyMs } = await api('/api/mic/measure', { method: 'POST', body: {} });
    this.latencyMs = latencyMs > 0 ? latencyMs : null;
    this.onChange(this);
    return this.latencyMs;
  }

  status() {
    const s = this.settings;
    return {
      on: s.on, volume: s.volume, echo: s.echo, reverb: s.reverb,
      warmth: s.warmth, bright: s.bright, double: s.double, tune: s.tune,
      engine: 'native',
      device: this.device,
      latencyMs: this.latencyMs ?? 0,
      outputMs: this.engineMs,
      error: this.error,
    };
  }
}
