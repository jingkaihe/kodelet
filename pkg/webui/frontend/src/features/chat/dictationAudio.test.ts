import { runInNewContext } from 'node:vm';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import {
  createDictationRecorder,
  createDictationSession,
  type DictationRecorderOptions,
} from './dictationAudio';
import processorSource from './dictationProcessor.js?raw';

const sampleRate = 16_000;

function options(overrides: Partial<DictationRecorderOptions> = {}) {
  return {
    maxAudioBytes: 20 * 1024 * 1024,
    maxDurationSeconds: 120,
    onLevel: vi.fn(),
    onSpeech: vi.fn(),
    onSnapshot: vi.fn<(file: File) => void>(),
    onLimit: vi.fn(),
    onError: vi.fn(),
    ...overrides,
  };
}

function pcm(seconds: number, amplitude = 0.1) {
  return Float32Array.from(
    { length: Math.round(seconds * sampleRate) },
    (_, i) => (i % 2 ? -1 : 1) * amplitude
  );
}

function readWAV(file: File): Promise<DataView> {
  return new Promise((resolve, reject) => {
    const reader = new FileReader();
    reader.onload = () => resolve(new DataView(reader.result as ArrayBuffer));
    reader.onerror = reject;
    reader.readAsArrayBuffer(file);
  });
}

describe('cumulative dictation audio', () => {
  it('counts silence and isolated clicks toward the recording limit without uploading them', () => {
    const callbacks = options({ maxDurationSeconds: 6 });
    const session = createDictationSession(sampleRate, callbacks);
    session.push(pcm(3, 0));
    session.push(pcm(0.1, 0.5));
    session.push(pcm(2, 0));
    session.push(pcm(0.1, 0.3));
    session.push(pcm(1, 0));
    session.finish();
    expect(callbacks.onSnapshot).not.toHaveBeenCalled();
    expect(callbacks.onSpeech).not.toHaveBeenCalled();
    expect(callbacks.onLimit).toHaveBeenCalledTimes(1);
  });

  it('snapshots only after 2.5 seconds of silence and retains the complete audio prefix', async () => {
    const callbacks = options();
    const session = createDictationSession(sampleRate, callbacks);
    session.push(pcm(2, 0.001));
    expect(vi.mocked(callbacks.onLevel).mock.calls.length).toBeLessThanOrEqual(40);
    session.push(pcm(0.4));
    expect(callbacks.onLevel).toHaveBeenLastCalledWith(expect.closeTo(Math.sqrt(0.1) * 2));
    session.push(pcm(2.48, 0));
    expect(callbacks.onSnapshot).not.toHaveBeenCalled();
    session.push(pcm(0.02, 0));
    expect(callbacks.onSnapshot).toHaveBeenCalledTimes(1);
    expect(callbacks.onSpeech).toHaveBeenNthCalledWith(1, true);
    expect(callbacks.onSpeech).toHaveBeenNthCalledWith(2, false);
    session.push(pcm(0.5, 0));
    session.push(pcm(0.005, 0.2)); // Even a tiny final syllable must survive Done.
    session.finish();
    expect(callbacks.onSnapshot).toHaveBeenCalledTimes(2);
    const snapshots = vi.mocked(callbacks.onSnapshot).mock.calls.map(([file]) => file);
    const views = await Promise.all(snapshots.map(readWAV));
    for (const [index, file] of snapshots.entries()) {
      expect(file.name).toBe('dictation.wav');
      expect(file.type).toBe('audio/wav');
      const view = views[index];
      const ascii = (offset: number, count: number) =>
        String.fromCharCode(...new Uint8Array(view.buffer, offset, count));
      expect(ascii(0, 4)).toBe('RIFF');
      expect(ascii(8, 8)).toBe('WAVEfmt ');
      expect(ascii(36, 4)).toBe('data');
      expect(view.getUint32(4, true)).toBe(file.size - 8);
      expect(view.getUint32(40, true)).toBe(file.size - 44);
      expect(view.getUint16(20, true)).toBe(1);
      expect(view.getUint16(22, true)).toBe(1);
      expect(view.getUint16(34, true)).toBe(16);
      expect(view.getUint32(24, true)).toBe(sampleRate);
    }
    expect(snapshots[0].size).toBe(44 + Math.round(sampleRate * 4.9) * 2);
    expect(snapshots[1].size).toBe(44 + Math.round(sampleRate * 5.405) * 2);
    const [first, last] = views;
    expect(first.getInt16(44, true)).toBe(33); // Original input, not synthesized pre-roll.
    expect(first.getInt16(44 + sampleRate * 2 * 2, true)).toBe(3277);
    expect(new Uint8Array(last.buffer, 44, first.byteLength - 44)).toEqual(
      new Uint8Array(first.buffer, 44)
    );
    expect(last.getInt16(44 + sampleRate * 5 * 2, true)).toBe(0); // Intervening pause.
    expect(last.getInt16(44 + sampleRate * 5.4 * 2, true)).toBe(6553);
    expect(callbacks.onLimit).not.toHaveBeenCalled();
  });

  it('does not repeat a snapshot during silence or on Done without new speech', () => {
    const callbacks = options();
    const session = createDictationSession(sampleRate, callbacks);
    session.push(pcm(0.3));
    session.push(pcm(2.5, 0));
    expect(callbacks.onSnapshot).toHaveBeenCalledTimes(1);
    session.push(pcm(6, 0));
    session.finish();
    session.finish();
    expect(callbacks.onSnapshot).toHaveBeenCalledTimes(1);
    expect(callbacks.onLimit).not.toHaveBeenCalled();
  });

  it('adapts to stable low background noise and still detects stronger speech energy', () => {
    const callbacks = options();
    const session = createDictationSession(sampleRate, callbacks);
    session.push(pcm(3, 0.006));
    session.push(pcm(2, 0.012)); // Above fixed floor, below learned background threshold.
    expect(callbacks.onSnapshot).not.toHaveBeenCalled();
    expect(callbacks.onSpeech).not.toHaveBeenCalled();
    session.push(pcm(0.3, 0.08));
    session.push(pcm(2.5, 0.012));
    expect(callbacks.onSnapshot).toHaveBeenCalledTimes(1);
    session.cancel();
  });

  it.each([
    { name: 'duration', maxDurationSeconds: 120, maxAudioBytes: 20 * 1024 * 1024, limit: 120 },
    { name: 'bytes', maxDurationSeconds: 120, maxAudioBytes: 45 + sampleRate * 8.5, limit: 4.25 },
  ])('preserves cumulative audio up to the $name limit without splitting or overflowing', async (limits) => {
    const callbacks = options(limits);
    const session = createDictationSession(sampleRate, callbacks);
    session.push(pcm(0.4, 0.05));
    session.push(pcm(2.6, 0));
    session.push(pcm(limits.limit, 2));
    session.finish();
    expect(callbacks.onSnapshot).toHaveBeenCalledTimes(2);
    expect(callbacks.onLimit).toHaveBeenCalledTimes(1);
    expect(vi.mocked(callbacks.onSnapshot).mock.invocationCallOrder[1]).toBeLessThan(
      vi.mocked(callbacks.onLimit).mock.invocationCallOrder[0]
    );
    const first = await readWAV(vi.mocked(callbacks.onSnapshot).mock.calls[0][0]);
    const file = vi.mocked(callbacks.onSnapshot).mock.calls[1][0];
    expect(file.size).toBe(44 + sampleRate * limits.limit * 2);
    expect(file.size).toBeLessThanOrEqual(limits.maxAudioBytes);
    const view = await readWAV(file);
    expect(view.getUint32(40, true)).toBe(file.size - 44);
    expect(new Uint8Array(view.buffer, 44, first.byteLength - 44)).toEqual(
      new Uint8Array(first.buffer, 44)
    );
    const speechOffset = 44 + sampleRate * 3 * 2;
    expect(view.getInt16(speechOffset, true)).toBe(32767);
    expect(view.getInt16(speechOffset + 2, true)).toBe(-32768);
    session.push(pcm(1));
    session.finish();
    expect(callbacks.onSnapshot).toHaveBeenCalledTimes(2);
    expect(callbacks.onLimit).toHaveBeenCalledTimes(1);
  });
});

class FakeTrack extends EventTarget {
  readyState = 'live';
  stop = vi.fn(() => {
    this.readyState = 'ended';
  });
}

class FakeAudioContext {
  static instances: FakeAudioContext[] = [];
  state = 'suspended';
  sampleRate = sampleRate;
  destination = {};
  onstatechange: (() => void) | null = null;
  audioWorklet = { addModule: vi.fn().mockResolvedValue(undefined) };
  source = { connect: vi.fn(), disconnect: vi.fn() };
  resume = vi.fn(async () => {
    this.state = 'running';
  });
  close = vi.fn(async () => {
    this.state = 'closed';
  });
  createMediaStreamSource = vi.fn(() => this.source);
  constructor() {
    FakeAudioContext.instances.push(this);
  }
}

class FakeWorkletNode {
  static instances: FakeWorkletNode[] = [];
  onprocessorerror: (() => void) | null = null;
  port = {
    onmessage: null as ((event: { data: Record<string, unknown> }) => void) | null,
    postMessage: vi.fn(),
    close: vi.fn(),
  };
  connect = vi.fn();
  disconnect = vi.fn();
  constructor() {
    FakeWorkletNode.instances.push(this);
  }
  samples(seconds: number, amplitude = 0.1) {
    this.port.onmessage?.({ data: { type: 'samples', samples: pcm(seconds, amplitude) } });
  }
  flushed() {
    this.port.onmessage?.({ data: { type: 'flushed' } });
  }
}

function microphone() {
  const track = new FakeTrack();
  return {
    track,
    stream: { getTracks: () => [track], getAudioTracks: () => [track] } as unknown as MediaStream,
  };
}

describe('dictation recorder lifecycle', () => {
  beforeEach(() => {
    FakeAudioContext.instances = [];
    FakeWorkletNode.instances = [];
    vi.stubGlobal('AudioContext', FakeAudioContext);
    vi.stubGlobal('AudioWorkletNode', FakeWorkletNode);
  });

  afterEach(() => {
    vi.useRealTimers();
    vi.unstubAllGlobals();
  });

  it('resumes synchronously before permission, loads a same-origin asset, and owns the stream', async () => {
    const recorder = createDictationRecorder(options());
    const context = FakeAudioContext.instances[0];
    expect(context.resume).toHaveBeenCalledTimes(1);
    expect(context.audioWorklet.addModule).not.toHaveBeenCalled();
    const { stream, track } = microphone();
    await recorder.start(stream);
    expect(context.audioWorklet.addModule).toHaveBeenCalledWith(
      expect.stringContaining('dictationProcessor.js')
    );
    expect(context.audioWorklet.addModule.mock.calls[0][0]).not.toMatch(/^(blob:|data:)/);
    expect(context.source.connect).toHaveBeenCalledWith(FakeWorkletNode.instances[0]);
    recorder.cancel();
    recorder.cancel();
    expect(track.stop).toHaveBeenCalledTimes(1);
    expect(context.close).toHaveBeenCalledTimes(1);
    expect(context.source.disconnect).toHaveBeenCalledTimes(1);
  });

  it.each([
    'permission',
    'module',
  ] as const)('cancels while waiting for %s and stops late streams', async (pending) => {
    const recorder = createDictationRecorder(options());
    const context = FakeAudioContext.instances[0];
    const { stream, track } = microphone();
    let resolveModule: (() => void) | undefined;
    context.audioWorklet.addModule.mockImplementation(
      () =>
        new Promise<void>((resolve) => {
          resolveModule = resolve;
        })
    );
    const starting = pending === 'module' ? recorder.start(stream) : undefined;
    recorder.cancel();
    if (pending === 'permission') await recorder.start(stream);
    else {
      resolveModule?.();
      await starting;
    }
    expect(track.stop).toHaveBeenCalledTimes(1);
    expect(context.close).toHaveBeenCalledTimes(1);
    expect(FakeWorkletNode.instances).toHaveLength(0);
  });

  it('finishes after the final PCM acknowledgement without waiting for the pause threshold', async () => {
    vi.useFakeTimers();
    const callbacks = options();
    const recorder = createDictationRecorder(callbacks);
    const { stream, track } = microphone();
    await recorder.start(stream);
    const node = FakeWorkletNode.instances[0];
    node.samples(0.17);
    const finishing = recorder.finish();
    expect(recorder.finish()).toBe(finishing);
    expect(node.port.postMessage).toHaveBeenCalledWith({ type: 'finish' });
    expect(track.stop).not.toHaveBeenCalled();
    node.samples(0.02);
    node.flushed();
    await finishing;
    expect(callbacks.onSnapshot).toHaveBeenCalledTimes(1);
    expect(vi.mocked(callbacks.onSnapshot).mock.calls[0][0].size).toBe(44 + sampleRate * 0.19 * 2);
    expect(track.stop).toHaveBeenCalledTimes(1);
    expect(node.port.close).toHaveBeenCalledTimes(1);
    expect(callbacks.onError).not.toHaveBeenCalled();
    expect(vi.getTimerCount()).toBe(0);
  });

  it('allows finish during a snapshot callback and includes the worklet tail in the final snapshot', async () => {
    const callbacks = options();
    const recorder = createDictationRecorder(callbacks);
    await recorder.start(microphone().stream);
    let finished: Promise<void> | undefined;
    vi.mocked(callbacks.onSnapshot).mockImplementation(() => {
      finished = recorder.finish();
    });
    const node = FakeWorkletNode.instances[0];
    node.samples(0.3);
    node.samples(2.5, 0);
    node.samples(0.005);
    node.flushed();
    await finished;
    expect(callbacks.onSnapshot).toHaveBeenCalledTimes(2);
    expect(vi.mocked(callbacks.onSnapshot).mock.calls.map(([file]) => file.size)).toEqual([
      44 + sampleRate * 2.8 * 2,
      44 + sampleRate * 2.805 * 2,
    ]);
    expect(callbacks.onLimit).not.toHaveBeenCalled();
  });

  it.each([
    'finish',
    'cancel',
  ] as const)('closes the microphone at its limit before a reentrant %s', async (action) => {
    vi.useFakeTimers();
    const callbacks = options({ maxDurationSeconds: 1 });
    const recorder = createDictationRecorder(callbacks);
    const { stream, track } = microphone();
    await recorder.start(stream);
    vi.mocked(callbacks.onSnapshot).mockImplementation(() => {
      void recorder[action]();
    });
    vi.mocked(callbacks.onLimit).mockImplementation(() => {
      expect(track.readyState).toBe('ended');
      void recorder[action]();
    });
    const node = FakeWorkletNode.instances[0];
    node.samples(2);
    node.samples(1);
    await recorder.finish();
    expect(callbacks.onSnapshot).toHaveBeenCalledTimes(1);
    expect(vi.mocked(callbacks.onSnapshot).mock.calls[0][0].size).toBe(44 + sampleRate * 2);
    expect(callbacks.onLimit).toHaveBeenCalledTimes(1);
    expect(callbacks.onError).not.toHaveBeenCalled();
    expect(track.stop).toHaveBeenCalledTimes(1);
    expect(node.port.close).toHaveBeenCalledTimes(1);
    expect(FakeAudioContext.instances[0].close).toHaveBeenCalledTimes(1);
    expect(vi.getTimerCount()).toBe(0);
  });

  it('bounds flush acknowledgement and preserves known speech with an explicit warning', async () => {
    vi.useFakeTimers();
    const callbacks = options();
    const recorder = createDictationRecorder(callbacks);
    const { stream, track } = microphone();
    await recorder.start(stream);
    FakeWorkletNode.instances[0].samples(0.3);
    const finishing = recorder.finish();
    await vi.advanceTimersByTimeAsync(500);
    await finishing;
    expect(callbacks.onSnapshot).toHaveBeenCalledTimes(1);
    expect(callbacks.onError).toHaveBeenCalledWith(expect.stringContaining('incomplete'));
    expect(track.stop).toHaveBeenCalledTimes(1);
  });

  it('cancels a pending flush without submitting a final snapshot or leaving a timer', async () => {
    vi.useFakeTimers();
    const callbacks = options();
    const recorder = createDictationRecorder(callbacks);
    await recorder.start(microphone().stream);
    const node = FakeWorkletNode.instances[0];
    node.samples(0.3);
    const finishing = recorder.finish();
    recorder.cancel();
    node.flushed();
    await finishing;
    expect(callbacks.onSnapshot).not.toHaveBeenCalled();
    expect(callbacks.onError).not.toHaveBeenCalled();
    expect(vi.getTimerCount()).toBe(0);
  });

  it.each([
    'device',
    'processor',
    'suspended',
  ] as const)('cleans up and reports %s interruption once', async (failure) => {
    const callbacks = options();
    const recorder = createDictationRecorder(callbacks);
    const { stream, track } = microphone();
    await recorder.start(stream);
    const context = FakeAudioContext.instances[0];
    const node = FakeWorkletNode.instances[0];
    if (failure === 'device') track.dispatchEvent(new Event('ended'));
    if (failure === 'processor') node.onprocessorerror?.();
    if (failure === 'suspended') {
      context.state = 'suspended';
      context.onstatechange?.();
    }
    recorder.cancel();
    node.samples(0.3);
    expect(callbacks.onError).toHaveBeenCalledTimes(1);
    expect(callbacks.onSnapshot).not.toHaveBeenCalled();
    expect(track.stop).toHaveBeenCalledTimes(1);
    expect(context.close).toHaveBeenCalledTimes(1);
  });

  it('rejects setup failure and closes the context and microphone', async () => {
    const callbacks = options();
    const recorder = createDictationRecorder(callbacks);
    const { stream, track } = microphone();
    const context = FakeAudioContext.instances[0];
    context.audioWorklet.addModule.mockRejectedValue(new Error('Worklet refused by CSP'));
    await expect(recorder.start(stream)).rejects.toThrow('Worklet refused by CSP');
    expect(track.stop).toHaveBeenCalledTimes(1);
    expect(context.close).toHaveBeenCalledTimes(1);
    expect(callbacks.onError).not.toHaveBeenCalled();
  });
});

it('the actual worklet downmixes without overlapping samples and flushes its final partial frame', () => {
  class Base {
    port = { postMessage: vi.fn(), onmessage: (_event: { data: { type: string } }) => undefined };
  }
  let Processor: (new () => Base & { process: (inputs: Float32Array[][]) => boolean }) | undefined;
  runInNewContext(processorSource, {
    AudioWorkletProcessor: Base,
    Float32Array,
    sampleRate: 1000,
    registerProcessor: (name: string, value: typeof Processor) => {
      expect(name).toBe('kodelet-dictation');
      Processor = value;
    },
  });
  if (!Processor) throw new Error('Processor did not register');
  const processor = new Processor();
  processor.process([[new Float32Array(27).fill(0.2), new Float32Array(27).fill(0.4)]]);
  expect(processor.port.postMessage).toHaveBeenCalledTimes(1);
  processor.port.onmessage({ data: { type: 'finish' } });
  const messages = processor.port.postMessage.mock.calls.map(([message]) => message);
  expect(messages.map((message) => message.type)).toEqual(['samples', 'samples', 'flushed']);
  expect(messages[0].samples).toHaveLength(20);
  expect(messages[1].samples).toHaveLength(7);
  expect(messages[1].samples[0]).toBeCloseTo(0.3);
  processor.process([[new Float32Array(20).fill(1)]]);
  expect(processor.port.postMessage).toHaveBeenCalledTimes(3);
});
