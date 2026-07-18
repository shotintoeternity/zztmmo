import assert from "node:assert/strict";
import { build } from "esbuild";

// Bundle sound.ts under Node so M17.7 can exercise the real ZztSound synth against
// a mock Web Audio graph. M17.3 shipped the AudioContext unlock() fix with no
// automated coverage at all ("Not yet audibly confirmed in a real browser"); this
// is that missing net. The mock records every gain/frequency automation event so a
// test can assert that a queued note actually gates the oscillator on (an audible
// note), which is exactly what silence would fail to produce.
const output = await build({
  entryPoints: ["src/sound.ts"],
  bundle: true,
  format: "esm",
  platform: "node",
  write: false,
});
const source = Buffer.from(output.outputFiles[0].contents).toString("base64");
const { ZztSound, soundNotesFromProtocol } = await import(`data:text/javascript;base64,${source}`);

// A recording Web Audio graph. clock is advanced by the test to drive the
// look-ahead scheduler that ZztSound runs off window.setInterval.
function mockAudio() {
  const events = [];
  let clock = 0;
  let resumeCalls = 0;
  const intervals = [];
  class Param {
    constructor(name) {
      this.name = name;
      this.value = 0;
    }
    setValueAtTime(v, t) {
      events.push({ node: this.name, op: "set", value: v, time: t });
      this.value = v;
    }
    linearRampToValueAtTime(v, t) {
      events.push({ node: this.name, op: "ramp", value: v, time: t });
      this.value = v;
    }
    cancelScheduledValues(t) {
      events.push({ node: this.name, op: "cancel", time: t });
    }
  }
  class GainNode {
    constructor() {
      this.gain = new Param("gain");
    }
    connect() {}
  }
  class OscillatorNode {
    constructor() {
      this.type = "";
      this.frequency = new Param("freq");
      this.started = false;
    }
    connect() {}
    start() {
      this.started = true;
    }
  }
  class AudioContext {
    constructor() {
      this.state = "suspended";
      this._dest = {};
    }
    get currentTime() {
      return clock;
    }
    createOscillator() {
      return new OscillatorNode();
    }
    createGain() {
      return new GainNode();
    }
    get destination() {
      return this._dest;
    }
    resume() {
      resumeCalls += 1;
      this.state = "running";
      return Promise.resolve();
    }
  }
  const win = {
    AudioContext,
    setInterval: (fn) => {
      intervals.push(fn);
      return intervals.length;
    },
  };
  return {
    win,
    events,
    resumeCallCount: () => resumeCalls,
    advance: (dt) => {
      clock += dt;
      for (const fn of intervals) fn();
    },
    now: () => clock,
  };
}

function gateOns(events) {
  // An audible note is a positive linear ramp on the gain param.
  return events.filter((e) => e.node === "gain" && e.op === "ramp" && e.value > 0);
}

// A quarter-note (duration 8) at octave 2 C, then a rest, terminated. The trailing
// byte is the two-byte-pair terminator the scheduler needs (pos >= length-1).
const ONE_NOTE = Uint8Array.from([0x20, 8, 0]);

// --- The synth actually gates a note on when driven through the in-game path. ---
{
  const audio = mockAudio();
  globalThis.window = audio.win;
  const s = new ZztSound();
  // Mirror main.ts: a document gesture unlocks, startPlay enables + resumes.
  s.unlock();
  s.setEnabled(true);
  s.resume();
  s.queue(0, ONE_NOTE);
  // Run the look-ahead scheduler across enough wall-clock to reach the note.
  for (let i = 0; i < 4; i += 1) audio.advance(0.03);

  const ons = gateOns(audio.events);
  assert.ok(ons.length >= 1, "a queued note must produce at least one audible gain ramp");
  const freqSet = audio.events.find((e) => e.node === "freq" && e.op === "set" && e.value > 0);
  assert.ok(freqSet, "a tone must set a positive oscillator frequency");
  assert.ok(audio.resumeCallCount() >= 1, "resume()/unlock() must resume the AudioContext");
  console.log("sound.test.mjs: queued note gates the oscillator on");
}

// --- unlock() resumes even while sound is disabled (M17.3's core behavior). ---
// The title screen mutes (setEnabled(false)); a gesture there must still leave the
// context "running" so the first in-game note is not swallowed by a suspended ctx.
{
  const audio = mockAudio();
  globalThis.window = audio.win;
  const s = new ZztSound();
  s.setEnabled(false); // title screen mute
  s.unlock(); // any document gesture
  assert.equal(audio.resumeCallCount(), 1, "unlock() must resume a suspended context while muted");
  console.log("sound.test.mjs: unlock() resumes the context even while muted");
}

// --- A disabled synth stays silent: no note gates on. ---
{
  const audio = mockAudio();
  globalThis.window = audio.win;
  const s = new ZztSound();
  s.unlock();
  s.setEnabled(false);
  s.queue(0, ONE_NOTE);
  for (let i = 0; i < 4; i += 1) audio.advance(0.03);
  assert.equal(gateOns(audio.events).length, 0, "a disabled synth must not gate any note on");
  console.log("sound.test.mjs: disabled synth stays silent");
}

// --- soundNotesFromProtocol maps the protocol's numeric note array to bytes. ---
// Regression guard: the wire carries notes as number[] (protocol.go soundNoteBytes
// -> []uint16 -> JSON array). A string here would & 0xff every char to NaN->0 and
// silence everything, so pin the array contract.
{
  assert.deepEqual([...soundNotesFromProtocol([0x20, 8, 0])], [0x20, 8, 0]);
  assert.equal(soundNotesFromProtocol(undefined).length, 0);
  assert.equal(soundNotesFromProtocol([]).length, 0);
  // Values above a byte are masked, mirroring the &0xff in the synth.
  assert.deepEqual([...soundNotesFromProtocol([0x120])], [0x20]);
  console.log("sound.test.mjs: soundNotesFromProtocol maps numeric notes to bytes");
}

console.log("sound.test.mjs: all assertions passed");
