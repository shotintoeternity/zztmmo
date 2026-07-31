// M16.10's observable AudioContext.
//
// sound.ts reaches Web Audio through exactly one door — `window.AudioContext ||
// window.webkitAudioContext` in ensureAudio — so replacing that constructor
// before the page loads leaves the REAL ZztSound in place: its scheduler, its
// priority arbitration, its note table, and its gate ramps all run unmodified,
// and every automation call they make is recorded instead of heard.
//
// The mock's clock is Date.now(), which Playwright's fake clock owns. That is
// what makes an audio assertion deterministic: the note scheduler only advances
// when the test advances it, so "how many notes were scheduled" is a decision
// the script makes rather than a race it runs.

/** Install the recorder. Must be called BEFORE page.goto. */
export async function installAudioMock(page) {
  await page.addInitScript(() => {
    const t0 = Date.now();
    const log = { events: [], resumes: 0, contexts: 0 };
    window.__m1610Audio = log;

    const param = (name) => ({
      value: 0,
      setValueAtTime(value, time) {
        log.events.push({ name, op: "set", value, time });
        return this;
      },
      linearRampToValueAtTime(value, time) {
        log.events.push({ name, op: "ramp", value, time });
        return this;
      },
      cancelScheduledValues(time) {
        log.events.push({ name, op: "cancel", time });
        return this;
      },
    });

    class MockAudioContext {
      constructor() {
        log.contexts += 1;
        this.state = "suspended";
        this.destination = { connect() {} };
      }
      get currentTime() {
        return (Date.now() - t0) / 1000;
      }
      resume() {
        log.resumes += 1;
        this.state = "running";
        return Promise.resolve();
      }
      createOscillator() {
        return { type: "sine", frequency: param("frequency"), connect() {}, start() {}, stop() {} };
      }
      createGain() {
        return { gain: param("gain"), connect() {} };
      }
    }

    window.AudioContext = MockAudioContext;
    // Left in place deliberately: sound.ts prefers window.AudioContext, and a
    // client that ever stopped preferring it would then be caught here rather
    // than silently playing through the real hardware path.
    window.webkitAudioContext = MockAudioContext;
  });
}

export async function audioLog(page) {
  return page.evaluate(() => ({
    events: window.__m1610Audio.events.slice(),
    resumes: window.__m1610Audio.resumes,
    contexts: window.__m1610Audio.contexts,
  }));
}

/** Every frequency the oscillator was told to take, in scheduling order. */
export function tones(log, since = 0) {
  return log.events.slice(since).filter((e) => e.name === "frequency" && e.op === "set").map((e) => e.value);
}

/**
 * How many melodies were ACCEPTED. resetScheduleNow cancels the gain automation
 * whenever ZztSound takes a new buffer, and queue() returns before that when
 * priority arbitration rejects one — so this counts admissions, not arrivals.
 */
export function melodyAdmissions(log, since = 0) {
  return log.events.slice(since).filter((e) => e.name === "gain" && e.op === "cancel").length;
}

// Vanilla's footstep poke: ElementPlayerTick's direct Sound(110)/NoSound, which
// reaches the client as a `walkClick` event and bypasses queue() entirely. It
// shares the oscillator with the melody, so a melody assertion has to say which
// of the two it means.
export const WALK_CLICK_HZ = 110;

/** Tones with the walk click filtered out — the notes of an actual melody. */
export function melodyTones(log, since = 0) {
  return tones(log, since).filter((hz) => hz !== WALK_CLICK_HZ);
}
