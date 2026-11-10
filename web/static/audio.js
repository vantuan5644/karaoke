// The TV page's one AudioContext, shared by the mic and the music. It is made on first use,
// which must follow a click: browsers keep a context made without one silent.
let ctx = null;

export function audioContext() {
  if (!ctx) ctx = new AudioContext({ latencyHint: 'interactive' });
  if (ctx.state === 'suspended') ctx.resume();
  return ctx;
}
