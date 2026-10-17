import processorURL from './dictationProcessor.js?url&no-inline';

export interface DictationRecorderOptions {
  maxAudioBytes: number;
  maxDurationSeconds: number;
  onLevel: (level: number) => void;
  onSpeech: (speaking: boolean) => void;
  onSnapshot: (file: File) => void;
  onLimit: () => void;
  onError: (message: string) => void;
}

// Energy detection only decides when to refresh the transcript. Preserve every
// sample, including pauses, so each WAV gives the provider the full context.
export function createDictationSession(sampleRate: number, options: DictationRecorderOptions) {
  const maxSamples = Math.floor(
    Math.min(options.maxDurationSeconds * sampleRate, (options.maxAudioBytes - 44) / 2)
  );
  if (
    !Number.isFinite(sampleRate) ||
    !Number.isFinite(maxSamples) ||
    sampleRate <= 0 ||
    maxSamples < sampleRate * 0.2
  ) {
    throw new Error('The server recording limits are too small for microphone dictation.');
  }
  const frameSize = Math.max(1, Math.floor(sampleRate * 0.02));
  let audio: DataView | undefined = new DataView(new ArrayBuffer(maxSamples * 2));
  let samples = 0;
  let voicedSamples = 0;
  let silenceSamples = 0;
  let hasSpeech = false;
  let dirty = false;
  let speaking = false;
  let closed = false;
  let noiseFloor = 0.003;
  let levelSamples = sampleRate * 0.05;
  let peakLevel = 0;

  const reportSpeech = (value: boolean) => {
    if (speaking === value) return;
    speaking = value;
    options.onSpeech(value);
  };

  const snapshot = () => {
    if (!audio || !dirty) return;
    // Clear before publishing: the callback may synchronously finish/cancel.
    dirty = false;
    const buffer = new ArrayBuffer(44 + samples * 2);
    const view = new DataView(buffer);
    const ascii = (offset: number, text: string) => {
      for (let i = 0; i < text.length; i++) view.setUint8(offset + i, text.charCodeAt(i));
    };
    ascii(0, 'RIFF');
    view.setUint32(4, buffer.byteLength - 8, true);
    ascii(8, 'WAVEfmt ');
    view.setUint32(16, 16, true);
    view.setUint16(20, 1, true); // PCM
    view.setUint16(22, 1, true); // mono
    view.setUint32(24, sampleRate, true);
    view.setUint32(28, sampleRate * 2, true);
    view.setUint16(32, 2, true);
    view.setUint16(34, 16, true);
    ascii(36, 'data');
    view.setUint32(40, samples * 2, true);
    new Uint8Array(buffer, 44).set(new Uint8Array(audio.buffer, 0, samples * 2));
    options.onSnapshot(new File([buffer], 'dictation.wav', { type: 'audio/wav' }));
  };

  const finish = () => {
    if (closed) return;
    closed = true;
    snapshot();
    audio = undefined;
    reportSpeech(false);
  };

  const push = (input: Float32Array) => {
    let offset = 0;
    while (!closed && audio && offset < input.length) {
      const length = Math.min(frameSize, input.length - offset, maxSamples - samples);
      const frame = input.subarray(offset, offset + length);
      offset += length;
      let energy = 0;
      for (const sample of frame) {
        energy += sample * sample;
        const value = Math.max(-1, Math.min(1, sample));
        audio.setInt16(samples++ * 2, Math.round(value * (value < 0 ? 32768 : 32767)), true);
      }
      const rms = Math.sqrt(energy / frame.length);
      peakLevel = Math.max(peakLevel, Math.min(1, Math.sqrt(rms) * 2));
      levelSamples += frame.length;
      if (levelSamples >= sampleRate * 0.05) {
        options.onLevel(peakLevel);
        levelSamples = 0;
        peakLevel = 0;
        if (closed) return;
      }
      const threshold = Math.max(hasSpeech ? 0.008 : 0.01, noiseFloor * (hasSpeech ? 1.8 : 3));
      const voiced = rms > threshold;
      // Learn the local background only from non-speech. Hysteresis avoids
      // interpreting quiet syllables as pauses or adapting to sustained speech.
      if (!voiced && !speaking) noiseFloor += (rms - noiseFloor) * 0.05;

      if (voiced) {
        voicedSamples += frame.length;
        silenceSamples = 0;
        if (voicedSamples >= sampleRate * 0.18) hasSpeech = true;
        // Once speech is established, retain even a tiny final syllable after
        // an earlier snapshot rather than requiring a whole new utterance.
        if (hasSpeech) {
          dirty = true;
          reportSpeech(true);
        }
      } else {
        silenceSamples += frame.length;
        if (!hasSpeech && silenceSamples >= sampleRate * 0.12) voicedSamples = 0;
        if (silenceSamples >= sampleRate * 0.18) reportSpeech(false);
      }
      if (closed) return;
      if (samples === maxSamples) {
        // Finalize before notifying the owner; callbacks may reenter, but the
        // buffer must never roll over or accept samples beyond this limit.
        finish();
        options.onLimit();
      } else if (silenceSamples >= sampleRate * 2.5) {
        snapshot();
      }
    }
  };

  return {
    push,
    finish,
    cancel() {
      closed = true;
      audio = undefined;
      dirty = false;
      reportSpeech(false);
    },
  };
}

export function createDictationRecorder(options: DictationRecorderOptions) {
  // Both creation and resume happen synchronously in the initiating gesture,
  // before the parent awaits microphone permission (important on Safari).
  let context: AudioContext;
  try {
    // Let the browser resample microphone input rather than uploading 48 kHz PCM.
    context = new AudioContext({ latencyHint: 'interactive', sampleRate: 16_000 });
  } catch (error) {
    if (!(error instanceof DOMException) || error.name !== 'NotSupportedError') throw error;
    context = new AudioContext({ latencyHint: 'interactive' });
  }
  const resumed = context.resume().then(
    () => undefined,
    () => new Error('Could not start microphone audio processing.')
  );
  let stream: MediaStream | undefined;
  let source: MediaStreamAudioSourceNode | undefined;
  let node: AudioWorkletNode | undefined;
  let session: ReturnType<typeof createDictationSession> | undefined;
  let state: 'ready' | 'starting' | 'active' | 'finishing' | 'closed' = 'ready';
  let finishPromise: Promise<void> | undefined;
  let acknowledgeFlush: (() => void) | undefined;
  const isClosed = () => state === 'closed';

  const cleanup = () => {
    if (state === 'closed') return;
    state = 'closed';
    context.onstatechange = null;
    source?.disconnect();
    if (node) {
      node.onprocessorerror = null;
      node.port.onmessage = null;
      node.port.close();
      node.disconnect();
    }
    stream?.getTracks().forEach((track) => {
      track.removeEventListener('ended', deviceEnded);
      track.stop();
    });
    acknowledgeFlush?.();
    if (context.state !== 'closed') void context.close().catch(() => undefined);
  };
  const cancel = () => {
    if (state === 'closed') return;
    session?.cancel();
    cleanup();
  };
  const fail = (message: string) => {
    if (state === 'closed') return;
    // Preserve received samples without waiting on a failed worklet. Publish
    // before onError moves the owner into review and stops accepting snapshots.
    session?.finish();
    cleanup();
    options.onError(message);
  };
  const deviceEnded = () =>
    fail('The microphone disconnected. Check your input device and try again.');

  return {
    async start(input: MediaStream) {
      if (state !== 'ready') {
        input.getTracks().forEach((track) => {
          track.stop();
        });
        return;
      }
      stream = input;
      state = 'starting';
      try {
        const tracks = input.getAudioTracks();
        if (!tracks.length || tracks.some((track) => track.readyState === 'ended')) {
          throw new Error('No live microphone audio track is available.');
        }
        tracks.forEach((track) => {
          track.addEventListener('ended', deviceEnded);
        });
        session = createDictationSession(context.sampleRate, {
          ...options,
          onLimit: () => {
            cleanup();
            options.onLimit();
          },
        });
        const [resumeError] = await Promise.all([
          resumed,
          context.audioWorklet.addModule(processorURL),
        ]);
        if (isClosed()) return;
        if (resumeError) throw resumeError;
        if (context.state !== 'running')
          throw new Error('Microphone audio processing is suspended.');
        node = new AudioWorkletNode(context, 'kodelet-dictation', {
          numberOfInputs: 1,
          numberOfOutputs: 1,
          outputChannelCount: [1],
        });
        node.port.onmessage = ({ data }) => {
          if (state === 'closed') return;
          if (data.type === 'samples' && data.samples instanceof Float32Array) {
            session?.push(data.samples);
          } else if (data.type === 'flushed') {
            acknowledgeFlush?.();
          }
        };
        node.onprocessorerror = () => fail('Microphone audio processing failed. Please try again.');
        context.onstatechange = () => {
          if (context.state !== 'running')
            fail('Microphone capture was interrupted. Please try again.');
        };
        source = context.createMediaStreamSource(input);
        source.connect(node);
        // The processor emits only silence on its output. Connecting it keeps
        // capture running without playing microphone audio through speakers.
        node.connect(context.destination);
        state = 'active';
      } catch (error) {
        if (isClosed()) return;
        cancel();
        throw error;
      }
    },
    finish(): Promise<void> {
      if (finishPromise) return finishPromise;
      if (state !== 'active' || !node) {
        cancel();
        return Promise.resolve();
      }
      state = 'finishing';
      const processor = node;
      finishPromise = (async () => {
        let timedOut = false;
        await new Promise<void>((resolve) => {
          const timer = setTimeout(() => {
            timedOut = true;
            resolve();
          }, 500);
          acknowledgeFlush = () => {
            clearTimeout(timer);
            resolve();
          };
          try {
            processor.port.postMessage({ type: 'finish' });
          } catch {
            timedOut = true;
            acknowledgeFlush();
          }
        });
        if (isClosed()) return;
        // The worklet posts its last partial frame before the acknowledgement;
        // do not close the port or tracks until those samples have arrived.
        session?.finish();
        cleanup();
        if (timedOut)
          options.onError(
            'Microphone capture did not finish cleanly. The last audio may be incomplete.'
          );
      })();
      return finishPromise;
    },
    cancel,
  };
}
