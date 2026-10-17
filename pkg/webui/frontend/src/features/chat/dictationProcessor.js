// A same-origin worklet asset, not a blob/eval script. Its output stays silent;
// microphone PCM is transferred to the owner in small, non-overlapping frames.
class DictationProcessor extends AudioWorkletProcessor {
  constructor() {
    super();
    this.frame = new Float32Array(Math.max(1, Math.round(sampleRate * 0.02)));
    this.offset = 0;
    this.capturing = true;
    this.port.onmessage = ({ data }) => {
      if (data.type !== 'finish') return;
      this.capturing = false;
      this.flush();
      this.port.postMessage({ type: 'flushed' });
    };
  }

  flush() {
    if (!this.offset) return;
    const samples = this.frame.slice(0, this.offset);
    this.offset = 0;
    this.port.postMessage({ type: 'samples', samples }, [samples.buffer]);
  }

  process(inputs) {
    const channels = inputs[0];
    if (!this.capturing || !channels?.length) return true;
    for (let i = 0; i < channels[0].length; i++) {
      let sample = 0;
      for (const channel of channels) sample += channel[i];
      this.frame[this.offset++] = sample / channels.length;
      if (this.offset === this.frame.length) this.flush();
    }
    return true;
  }
}

registerProcessor('kodelet-dictation', DictationProcessor);
