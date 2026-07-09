const TICK_SEC = 1 / 18.2065;
const LOOKAHEAD_SEC = 0.05;
const SCHED_MS = 25;
const DRUM_STEP_SEC = 0.001;
const VOLUME = 0.1;
const CLICK_RAMP_SEC = 0.0008;

type Drum = {
  len: number;
  data: number[];
};

const FREQ_TABLE = buildFreqTable();

const DRUM_TABLE: Drum[] = [
  // ZZT percussion: 0 tick, 1 tweet, 2 cowbell, 3 triplet marker/N/A,
  // 4 hi snare, 5 hi woodblock, 6 low snare, 7 low tom,
  // 8 low woodblock, 9 bass drum.
  { len: 1, data: [3200] },
  { len: 14, data: [1100, 1200, 1300, 1400, 1500, 1600, 1700, 1800, 1900, 2000, 2100, 2200, 2300, 2400] },
  { len: 14, data: [4800, 4800, 8000, 1600, 4800, 4800, 8000, 1600, 4800, 4800, 8000, 1600, 4800, 4800] },
  { len: 14, data: [0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0] },
  { len: 14, data: [500, 656, 4805, 1512, 1864, 3858, 2093, 1308, 2361, 2628, 910, 2873, 852, 4704] },
  { len: 14, data: [1600, 895, 1600, 1269, 1600, 2267, 1600, 1388, 1600, 2039, 1600, 1324, 1600, 1916] },
  { len: 14, data: [2200, 1760, 1760, 1320, 2640, 880, 2200, 1760, 1760, 1320, 2640, 880, 2200, 1760] },
  { len: 14, data: [688, 676, 664, 652, 640, 628, 616, 604, 592, 580, 568, 556, 544, 532] },
  { len: 14, data: [1192, 1216, 1241, 1228, 1207, 1261, 1109, 1271, 1207, 1341, 1036, 1303, 1059, 934] },
  { len: 14, data: [436, 610, 583, 228, 282, 283, 440, 229, 480, 224, 560, 506, 559, 531] },
];

export class ZztSound {
  private ctx?: AudioContext;
  private osc?: OscillatorNode;
  private gain?: GainNode;
  private buffer: Uint8Array<ArrayBufferLike> = new Uint8Array(0);
  private pos = 0;
  private currentPriority = 0;
  private isPlaying = false;
  private enabled = true;
  private nextNoteTime = 0;
  private scheduler = 0;

  resume() {
    if (!this.enabled) {
      return;
    }
    this.ensureAudio();
    void this.ctx?.resume();
    this.startScheduler();
    this.schedule();
  }

  setEnabled(on: boolean) {
    if (this.enabled === on) {
      return;
    }
    this.enabled = on;
    if (!on) {
      this.stop();
      return;
    }
    if (this.ctx) {
      this.resume();
    }
  }

  queue(priority: number, notes: Uint8Array<ArrayBufferLike>) {
    if (!this.enabled || notes.length === 0) {
      return;
    }
    const canQueue =
      !this.isPlaying ||
      (priority >= this.currentPriority && this.currentPriority !== -1) ||
      priority === -1;
    if (!canQueue) {
      return;
    }

    if (priority >= 0 || !this.isPlaying) {
      this.currentPriority = priority;
      this.buffer = notes;
      this.pos = 0;
      this.isPlaying = true;
      this.resetScheduleNow();
    } else {
      const tail = this.buffer.slice(this.pos);
      if (tail.length + notes.length < 255) {
        const appended = new Uint8Array(tail.length + notes.length);
        appended.set(tail, 0);
        appended.set(notes, tail.length);
        this.buffer = appended;
        this.pos = 0;
        this.isPlaying = true;
      }
    }

    this.resume();
  }

  private ensureAudio() {
    if (this.ctx) {
      return;
    }
    const AudioCtor = window.AudioContext || (window as Window & { webkitAudioContext?: typeof AudioContext }).webkitAudioContext;
    if (!AudioCtor) {
      return;
    }
    this.ctx = new AudioCtor();
    this.osc = this.ctx.createOscillator();
    this.gain = this.ctx.createGain();
    this.osc.type = "square";
    this.osc.frequency.value = 440;
    this.gain.gain.value = 0;
    this.osc.connect(this.gain);
    this.gain.connect(this.ctx.destination);
    this.osc.start();
    this.nextNoteTime = this.ctx.currentTime;
  }

  private startScheduler() {
    if (this.scheduler !== 0) {
      return;
    }
    this.scheduler = window.setInterval(() => this.schedule(), SCHED_MS);
  }

  private resetScheduleNow() {
    if (!this.ctx || !this.gain || !this.osc) {
      this.nextNoteTime = 0;
      return;
    }
    const now = this.ctx.currentTime;
    this.gain.gain.cancelScheduledValues(now);
    this.osc.frequency.cancelScheduledValues(now);
    this.gain.gain.setValueAtTime(0, now);
    this.nextNoteTime = now;
  }

  private stop() {
    this.buffer = new Uint8Array(0);
    this.pos = 0;
    this.isPlaying = false;
    this.resetScheduleNow();
  }

  private schedule() {
    if (!this.ctx || !this.gain || !this.osc || !this.enabled || !this.isPlaying) {
      return;
    }
    const horizon = this.ctx.currentTime + LOOKAHEAD_SEC;
    if (this.nextNoteTime < this.ctx.currentTime) {
      this.nextNoteTime = this.ctx.currentTime;
    }

    while (this.isPlaying && this.nextNoteTime < horizon) {
      if (this.pos >= this.buffer.length - 1) {
        this.gateOff(this.nextNoteTime);
        this.isPlaying = false;
        break;
      }

      const note = this.buffer[this.pos];
      const duration = this.buffer[this.pos + 1];
      this.pos += 2;

      if (note === 0) {
        this.gateOff(this.nextNoteTime);
        this.nextNoteTime += duration * TICK_SEC;
      } else if (note < 240) {
        this.scheduleTone(note, this.nextNoteTime);
        this.nextNoteTime += duration * TICK_SEC;
      } else {
        this.scheduleDrum(note - 240, this.nextNoteTime);
        const drum = DRUM_TABLE[note - 240];
        this.nextNoteTime += (drum?.len ?? 0) * DRUM_STEP_SEC + duration * TICK_SEC;
      }
    }
  }

  private scheduleTone(note: number, time: number) {
    const freq = FREQ_TABLE[note - 1];
    if (!freq || !this.osc) {
      this.gateOff(time);
      return;
    }
    this.osc.frequency.setValueAtTime(freq, time);
    this.gateOn(time);
  }

  private scheduleDrum(index: number, time: number) {
    const drum = DRUM_TABLE[index];
    if (!drum || !this.osc) {
      this.gateOff(time);
      return;
    }
    for (let i = 0; i < drum.len; i += 1) {
      const stepTime = time + i * DRUM_STEP_SEC;
      const freq = drum.data[i] ?? 0;
      if (freq > 0) {
        this.osc.frequency.setValueAtTime(freq, stepTime);
        if (i === 0) {
          this.gateOn(stepTime);
        }
      } else {
        this.gateOff(stepTime);
      }
    }
    this.gateOff(time + drum.len * DRUM_STEP_SEC);
  }

  private gateOn(time: number) {
    if (!this.gain) {
      return;
    }
    this.gain.gain.setValueAtTime(0, time);
    this.gain.gain.linearRampToValueAtTime(VOLUME, time + CLICK_RAMP_SEC);
  }

  private gateOff(time: number) {
    if (!this.gain) {
      return;
    }
    this.gain.gain.setValueAtTime(0, time);
  }
}

export function soundNotesFromProtocol(notes: number[] | undefined): Uint8Array<ArrayBufferLike> {
  if (!notes || notes.length === 0) {
    return new Uint8Array(0);
  }
  const out = new Uint8Array(notes.length);
  for (let i = 0; i < notes.length; i += 1) {
    out[i] = notes[i] & 0xff;
  }
  return out;
}

function buildFreqTable(): number[] {
  const table = new Array<number>(255).fill(0);
  const freqC1 = 32.0;
  const step = Math.pow(2, 1 / 12);
  for (let octave = 1; octave <= 15; octave += 1) {
    let base = Math.exp(octave * Math.log(2)) * freqC1;
    for (let note = 0; note <= 11; note += 1) {
      table[octave * 16 + note - 1] = Math.trunc(base);
      base *= step;
    }
  }
  return table;
}
