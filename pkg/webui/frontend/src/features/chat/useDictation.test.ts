import { act, fireEvent, renderHook, waitFor } from '@testing-library/react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import apiService from '../../services/api';
import type { DictationRecorderOptions } from './dictationAudio';
import { isVoiceCommand, useDictation } from './useDictation';

const { createRecorder } = vi.hoisted(() => ({ createRecorder: vi.fn() }));
vi.mock('./dictationAudio', () => ({ createDictationRecorder: createRecorder }));

const capability = {
  enabled: true,
  available: true,
  maxAudioBytes: 20 * 1024 * 1024,
  maxDurationSeconds: 120,
  mimeTypes: ['audio/webm', 'audio/mp4', 'audio/wav'],
};
const stopTrack = vi.fn();
const stream = { getTracks: () => [{ stop: stopTrack }] } as unknown as MediaStream;
const getUserMedia = vi.fn();
const recorderStart = vi.fn();
const recorderFinish = vi.fn();
const recorderCancel = vi.fn();
const onComplete = vi.fn();
const onStart = vi.fn(() => onComplete);
let recorderOptions: DictationRecorderOptions;

function deferred<T>() {
  let resolve!: (value: T) => void;
  const promise = new Promise<T>((fulfill) => {
    resolve = fulfill;
  });
  return { promise, resolve };
}

function audioFile(name = 'recording.wav') {
  return new File(['RIFF1234WAVEdata'], name, { type: 'audio/wav' });
}

function renderDictation() {
  return renderHook(({ targetKey }) => useDictation({ targetKey, onStart }), {
    initialProps: { targetKey: 'conversation-one' },
  });
}

async function ready() {
  const hook = renderDictation();
  await waitFor(() => expect(hook.result.current.reason).toBeNull());
  return hook;
}

beforeEach(() => {
  vi.stubGlobal('isSecureContext', true);
  vi.stubGlobal(
    'AudioContext',
    class {
      audioWorklet = {};
    }
  );
  vi.stubGlobal('AudioWorkletNode', class {});
  vi.stubGlobal('navigator', { mediaDevices: { getUserMedia } });
  stopTrack.mockReset();
  onStart.mockClear();
  onComplete.mockClear();
  getUserMedia.mockReset().mockResolvedValue(stream);
  recorderStart.mockReset().mockResolvedValue(undefined);
  recorderFinish.mockReset().mockImplementation(async () => stopTrack());
  recorderCancel.mockReset().mockImplementation(() => stopTrack());
  createRecorder.mockReset().mockImplementation((options: DictationRecorderOptions) => {
    recorderOptions = options;
    return { start: recorderStart, finish: recorderFinish, cancel: recorderCancel };
  });
  vi.spyOn(apiService, 'getTranscriptionCapabilities').mockResolvedValue(capability);
  vi.spyOn(apiService, 'transcribeAudio').mockResolvedValue({ text: 'spoken words' });
});

afterEach(() => {
  vi.restoreAllMocks();
  vi.unstubAllGlobals();
  vi.useRealTimers();
});

describe('useDictation', () => {
  it('refreshes unavailable credentials on start while preparing audio synchronously', async () => {
    vi.mocked(apiService.getTranscriptionCapabilities).mockResolvedValueOnce({
      ...capability,
      available: false,
      reason: 'Sign in to Codex first.',
    });
    const { result } = renderDictation();
    await waitFor(() => expect(result.current.reason).toContain('Sign in'));
    const refreshed = deferred<typeof capability>();
    vi.mocked(apiService.getTranscriptionCapabilities).mockReturnValueOnce(refreshed.promise);
    act(() => {
      result.current.start();
      result.current.start();
      fireEvent.focus(window);
    });
    expect(createRecorder).toHaveBeenCalledTimes(1);
    expect(apiService.getTranscriptionCapabilities).toHaveBeenCalledTimes(2);
    expect(createRecorder.mock.invocationCallOrder[0]).toBeLessThan(
      vi.mocked(apiService.getTranscriptionCapabilities).mock.invocationCallOrder[1]
    );
    expect(getUserMedia).not.toHaveBeenCalled();
    await act(async () => refreshed.resolve({ ...capability, maxDurationSeconds: 60 }));
    expect(result.current.phase).toBe('recording');
    expect(result.current.reason).toBeNull();
    expect(recorderOptions.maxDurationSeconds).toBe(60);
    expect(getUserMedia).toHaveBeenCalledTimes(1);
  });

  it('retries failed discovery on focus and ignores superseded or unmounted responses', async () => {
    vi.mocked(apiService.getTranscriptionCapabilities).mockRejectedValueOnce(new Error('offline'));
    const hook = renderDictation();
    await waitFor(() => expect(hook.result.current.reason).toContain('Try again'));
    const stale = deferred<typeof capability>();
    vi.mocked(apiService.getTranscriptionCapabilities).mockReturnValueOnce(stale.promise);
    act(() => fireEvent.focus(window));
    const signal = vi.mocked(apiService.getTranscriptionCapabilities).mock.calls[1][0];
    await act(async () => fireEvent.focus(window));
    expect(signal?.aborted).toBe(true);
    expect(hook.result.current.reason).toBeNull();
    await act(async () => stale.resolve({ ...capability, enabled: false, available: false }));
    expect(hook.result.current.enabled).toBe(true);
    const pending = deferred<typeof capability>();
    vi.mocked(apiService.getTranscriptionCapabilities).mockReturnValueOnce(pending.promise);
    act(() => fireEvent.focus(window));
    const lastSignal = vi.mocked(apiService.getTranscriptionCapabilities).mock.calls[3][0];
    hook.unmount();
    expect(lastSignal?.aborted).toBe(true);
    await act(async () => pending.resolve({ ...capability, enabled: false, available: false }));
    fireEvent.focus(window);
    expect(apiService.getTranscriptionCapabilities).toHaveBeenCalledTimes(4);
    expect(getUserMedia).not.toHaveBeenCalled();
  });

  it('previews pause-triggered text while listening and completes only on Done', async () => {
    const { result } = await ready();
    await act(async () => result.current.start());
    expect(onStart).toHaveBeenCalledWith(false);
    expect(result.current.phase).toBe('recording');
    expect(getUserMedia).toHaveBeenCalledWith({ audio: true });
    expect(recorderStart).toHaveBeenCalledWith(stream);
    expect(createRecorder.mock.invocationCallOrder[0]).toBeLessThan(
      getUserMedia.mock.invocationCallOrder[0]
    );
    const file = audioFile();
    await act(async () => {
      recorderOptions.onLevel(0.65);
      recorderOptions.onSpeech(true);
      recorderOptions.onSnapshot(file);
    });
    expect(result.current.levels).toContain(0.65);
    expect(result.current.speaking).toBe(true);
    expect(result.current.transcript).toBe('spoken words');
    expect(result.current.transcribing).toBe(false);
    expect(result.current.phase).toBe('recording');
    expect(onComplete).not.toHaveBeenCalled();
    expect(recorderFinish).not.toHaveBeenCalled();
    expect(apiService.transcribeAudio).toHaveBeenCalledWith(file, expect.any(AbortSignal));
    await act(async () => result.current.stop());
    expect(recorderFinish).toHaveBeenCalledTimes(1);
    expect(stopTrack).toHaveBeenCalled();
    expect(onComplete).toHaveBeenCalledExactlyOnceWith('spoken words');
    expect(result.current.phase).toBe('idle');
  });

  it('replaces a queued snapshot with the final flush and waits before committing the complete text', async () => {
    const first = deferred<{ text: string }>();
    const second = deferred<{ text: string }>();
    vi.mocked(apiService.transcribeAudio)
      .mockReturnValueOnce(first.promise)
      .mockReturnValueOnce(second.promise);
    const { result } = await ready();
    await act(async () => result.current.start());
    const firstFile = audioFile('first.wav');
    const secondFile = audioFile('second.wav');
    const finalFile = audioFile('final.wav');
    await act(async () => {
      recorderOptions.onSnapshot(firstFile);
      recorderOptions.onSnapshot(secondFile);
    });
    expect(apiService.transcribeAudio).toHaveBeenCalledTimes(1);
    expect(result.current.transcribing).toBe(true);
    expect(result.current.phase).toBe('recording');
    expect(recorderFinish).not.toHaveBeenCalled();
    recorderFinish.mockImplementationOnce(async () => {
      stopTrack();
      recorderOptions.onSnapshot(finalFile);
    });
    await act(async () => {
      result.current.stop();
    });
    expect(result.current.phase).toBe('finishing');
    expect(onComplete).not.toHaveBeenCalled();
    await act(async () => first.resolve({ text: 'first words' }));
    expect(apiService.transcribeAudio).toHaveBeenCalledTimes(2);
    expect(vi.mocked(apiService.transcribeAudio).mock.calls[1][0]).toBe(finalFile);
    expect(result.current.transcript).toBe('first words');
    expect(onComplete).not.toHaveBeenCalled();
    await act(async () => second.resolve({ text: 'Corrected first words and the final phrase.' }));
    expect(apiService.transcribeAudio).toHaveBeenCalledTimes(2);
    expect(onComplete).toHaveBeenCalledWith('Corrected first words and the final phrase.');
    expect(result.current.phase).toBe('idle');
  });

  it.each([
    'cancel',
    'unmount',
    'switch',
  ] as const)('stops tracks granted after %s without starting capture', async (action) => {
    const permission = deferred<MediaStream>();
    getUserMedia.mockReturnValue(permission.promise);
    const hook = await ready();
    await act(async () => hook.result.current.start());
    expect(hook.result.current.phase).toBe('requesting');
    if (action === 'unmount') hook.unmount();
    else if (action === 'switch') hook.rerender({ targetKey: 'conversation-two' });
    else act(() => hook.result.current.cancel());
    stopTrack.mockClear();
    await act(async () => permission.resolve(stream));
    expect(stopTrack).toHaveBeenCalled();
    expect(recorderStart).not.toHaveBeenCalled();
    expect(apiService.transcribeAudio).not.toHaveBeenCalled();
  });

  it('cancels a pending availability check without requesting the microphone', async () => {
    const hook = await ready();
    const check = deferred<typeof capability>();
    vi.mocked(apiService.getTranscriptionCapabilities).mockReturnValueOnce(check.promise);
    act(() => hook.result.current.start());
    const signal = vi.mocked(apiService.getTranscriptionCapabilities).mock.calls[1][0];
    act(() => hook.result.current.cancel());
    expect(signal?.aborted).toBe(true);
    expect(recorderCancel).toHaveBeenCalled();
    await act(async () => check.resolve({ ...capability, enabled: false, available: false }));
    expect(getUserMedia).not.toHaveBeenCalled();
    expect(apiService.transcribeAudio).not.toHaveBeenCalled();
    expect(hook.result.current.phase).toBe('idle');
    expect(hook.result.current.enabled).toBe(true);
    expect(hook.result.current.error).toBeNull();
  });

  it.each([
    'cancel',
    'unmount',
    'switch',
  ] as const)('aborts uploads and ignores late audio and transcript callbacks after %s', async (action) => {
    const upload = deferred<{ text: string }>();
    vi.mocked(apiService.transcribeAudio).mockReturnValue(upload.promise);
    const hook = await ready();
    await act(async () => hook.result.current.start());
    const oldOptions = recorderOptions;
    await act(async () => oldOptions.onSnapshot(audioFile()));
    const signal = vi.mocked(apiService.transcribeAudio).mock.calls[0][1];
    if (action === 'unmount') hook.unmount();
    else if (action === 'switch') hook.rerender({ targetKey: 'conversation-two' });
    else act(() => hook.result.current.cancel());
    expect(signal?.aborted).toBe(true);
    expect(recorderCancel).toHaveBeenCalled();
    await act(async () => {
      oldOptions.onSnapshot(audioFile('late.wav'));
      oldOptions.onLevel(1);
      oldOptions.onError('late device error');
      upload.resolve({ text: 'late transcript' });
    });
    expect(apiService.transcribeAudio).toHaveBeenCalledTimes(1);
    expect(onComplete).not.toHaveBeenCalled();
    if (action !== 'unmount') expect(hook.result.current.error).toBeNull();
  });

  it('does not let a canceled session overwrite the preview of a new recording', async () => {
    const oldUpload = deferred<{ text: string }>();
    vi.mocked(apiService.transcribeAudio).mockReturnValueOnce(oldUpload.promise);
    const { result } = await ready();
    await act(async () => result.current.start());
    const oldOptions = recorderOptions;
    await act(async () => oldOptions.onSnapshot(audioFile()));
    act(() => result.current.cancel());
    await act(async () => result.current.start());
    await act(async () => recorderOptions.onSnapshot(audioFile()));
    expect(result.current.transcript).toBe('spoken words');
    await act(async () => {
      oldUpload.resolve({ text: 'obsolete transcript' });
      oldOptions.onSnapshot(audioFile());
      oldOptions.onLimit();
    });
    expect(result.current.phase).toBe('recording');
    expect(result.current.transcript).toBe('spoken words');
    expect(result.current.error).toBeNull();
    expect(apiService.transcribeAudio).toHaveBeenCalledTimes(2);
  });

  it('keeps the previous preview on failure and requires confirmation before inserting it', async () => {
    vi.mocked(apiService.transcribeAudio)
      .mockResolvedValueOnce({ text: 'first words' })
      .mockRejectedValueOnce(new Error('Provider unavailable'));
    const { result } = await ready();
    await act(async () => result.current.start());
    await act(async () => recorderOptions.onSnapshot(audioFile('first.wav')));
    await act(async () => recorderOptions.onSnapshot(audioFile('failed.wav')));
    expect(result.current.transcript).toBe('first words');
    expect(result.current.error).toContain('Provider unavailable');
    expect(result.current.error).toContain('preview may be incomplete');
    await act(async () => result.current.stop());
    expect(result.current.phase).toBe('review');
    expect(onComplete).not.toHaveBeenCalled();
    expect(apiService.transcribeAudio).toHaveBeenCalledTimes(2);
    await act(async () => result.current.stop());
    expect(onComplete).toHaveBeenCalledWith('first words');
    expect(result.current.phase).toBe('idle');
  });

  it('recovers from a failed update when a later full recording succeeds', async () => {
    vi.mocked(apiService.transcribeAudio)
      .mockResolvedValueOnce({ text: 'first words' })
      .mockRejectedValueOnce(new Error('Provider unavailable'))
      .mockResolvedValueOnce({ text: 'First words, the missing phrase, and the conclusion.' });
    const { result } = await ready();
    await act(async () => result.current.start());
    await act(async () => recorderOptions.onSnapshot(audioFile('first.wav')));
    await act(async () => recorderOptions.onSnapshot(audioFile('failed.wav')));
    expect(result.current.error).toBeTruthy();
    await act(async () => recorderOptions.onSnapshot(audioFile('complete.wav')));
    expect(result.current.error).toBeNull();
    expect(result.current.transcript).toBe('First words, the missing phrase, and the conclusion.');
    await act(async () => result.current.stop());
    expect(result.current.phase).toBe('idle');
    expect(onComplete).toHaveBeenCalledWith('First words, the missing phrase, and the conclusion.');
  });

  it.each([
    'duration',
    'buffer',
  ])('stops at the %s limit for review, never auto-committing', async (limit) => {
    const { result } = await ready();
    vi.useFakeTimers();
    await act(async () => result.current.start());
    expect(recorderOptions.maxDurationSeconds).toBe(120);
    expect(recorderOptions.maxAudioBytes).toBe(capability.maxAudioBytes);
    await act(async () => recorderOptions.onSnapshot(audioFile()));
    if (limit === 'duration') await act(async () => vi.advanceTimersByTimeAsync(120_000));
    else await act(async () => recorderOptions.onLimit());
    expect(recorderFinish).toHaveBeenCalledTimes(1);
    expect(result.current.phase).toBe('review');
    expect(result.current.error).toMatch(/limit/i);
    expect(onComplete).not.toHaveBeenCalled();
    await act(async () => result.current.stop());
    expect(onComplete).toHaveBeenCalledWith('spoken words');
  });

  it('coalesces slow-provider updates into only the newest snapshot while continuing to listen', async () => {
    const first = deferred<{ text: string }>();
    vi.mocked(apiService.transcribeAudio).mockReturnValueOnce(first.promise);
    const { result } = await ready();
    await act(async () => result.current.start());
    const latestFile = audioFile('latest.wav');
    await act(async () => {
      recorderOptions.onSnapshot(audioFile('first.wav'));
      recorderOptions.onSnapshot(audioFile('second.wav'));
      recorderOptions.onSnapshot(audioFile('third.wav'));
      recorderOptions.onSnapshot(latestFile);
    });
    expect(recorderFinish).not.toHaveBeenCalled();
    expect(result.current.transcribing).toBe(true);
    expect(result.current.error).toBeNull();
    expect(onComplete).not.toHaveBeenCalled();
    await act(async () => first.resolve({ text: 'first words' }));
    expect(result.current.phase).toBe('recording');
    expect(result.current.transcript).toBe('spoken words');
    expect(apiService.transcribeAudio).toHaveBeenCalledTimes(2);
    expect(vi.mocked(apiService.transcribeAudio).mock.calls[1][0]).toBe(latestFile);
    expect(onComplete).not.toHaveBeenCalled();
  });

  it('times out an upload, aborts it, and ignores its late response without retrying', async () => {
    const upload = deferred<{ text: string }>();
    vi.mocked(apiService.transcribeAudio).mockReturnValue(upload.promise);
    const { result } = await ready();
    vi.useFakeTimers();
    await act(async () => result.current.start());
    await act(async () => recorderOptions.onSnapshot(audioFile()));
    await act(async () => {
      result.current.stop();
    });
    await act(async () => vi.advanceTimersByTimeAsync(120_000));
    expect(vi.mocked(apiService.transcribeAudio).mock.calls[0][1]?.aborted).toBe(true);
    expect(result.current.phase).toBe('review');
    expect(result.current.error).toMatch(/timed out/i);
    expect(result.current.transcribing).toBe(false);
    await act(async () => upload.resolve({ text: 'late words' }));
    expect(result.current.error).toMatch(/timed out/i);
    expect(result.current.transcript).toBe('');
    expect(onComplete).not.toHaveBeenCalled();
    expect(apiService.transcribeAudio).toHaveBeenCalledTimes(1);
  });

  it.each([
    'constructor',
    'discovery',
    'permission',
    'start',
  ])('does not complete after a %s failure', async (failure) => {
    if (failure === 'constructor')
      createRecorder.mockImplementation(() => {
        throw new Error('Audio unavailable');
      });
    if (failure === 'permission')
      getUserMedia.mockRejectedValue(new DOMException('denied', 'NotAllowedError'));
    if (failure === 'start') recorderStart.mockRejectedValue(new Error('Audio unavailable'));
    const { result } = await ready();
    if (failure === 'discovery')
      vi.mocked(apiService.getTranscriptionCapabilities).mockRejectedValueOnce(
        new Error('offline')
      );
    await act(async () => result.current.handleCommand('/voice'));
    expect(onStart).toHaveBeenCalledWith(true);
    expect(result.current.error).toBeTruthy();
    expect(onComplete).not.toHaveBeenCalled();
    expect(apiService.transcribeAudio).not.toHaveBeenCalled();
    if (failure === 'constructor') expect(getUserMedia).not.toHaveBeenCalled();
    else expect(recorderCancel).toHaveBeenCalled();
    if (failure === 'discovery') {
      expect(getUserMedia).not.toHaveBeenCalled();
      await act(async () => result.current.start());
      expect(result.current.phase).toBe('recording');
      expect(result.current.error).toBeNull();
      expect(result.current.reason).toBeNull();
    }
  });

  it.each([
    'microphone',
    'finish',
  ])('preserves the previous preview for review after a %s failure', async (failure) => {
    const { result } = await ready();
    await act(async () => result.current.start());
    await act(async () => recorderOptions.onSnapshot(audioFile()));
    if (failure === 'microphone') {
      await act(async () => recorderOptions.onError('Microphone disconnected'));
    } else {
      recorderFinish.mockRejectedValueOnce(new Error('Device disconnected'));
      await act(async () => result.current.stop());
    }
    expect(result.current.phase).toBe('review');
    expect(result.current.error).toMatch(/disconnected|incomplete|finish/i);
    expect(result.current.transcript).toBe('spoken words');
    expect(onComplete).not.toHaveBeenCalled();
    expect(stopTrack).toHaveBeenCalled();
    await act(async () => result.current.stop());
    expect(onComplete).toHaveBeenCalledWith('spoken words');
  });

  it('prevents duplicate permission requests and ignores completion of canceled audio initialization', async () => {
    const initialization = deferred<void>();
    recorderStart.mockReturnValue(initialization.promise);
    const { result } = await ready();
    await act(async () => {
      result.current.start();
      result.current.start();
    });
    expect(createRecorder).toHaveBeenCalledTimes(1);
    expect(getUserMedia).toHaveBeenCalledTimes(1);
    expect(result.current.phase).toBe('requesting');
    act(() => result.current.cancel());
    await act(async () => initialization.resolve());
    expect(result.current.phase).toBe('idle');
    expect(onComplete).not.toHaveBeenCalled();
    expect(apiService.transcribeAudio).not.toHaveBeenCalled();
    expect(recorderCancel).toHaveBeenCalled();
    expect(stopTrack).toHaveBeenCalled();
  });

  it.each([
    'disabled',
    'disabled after mount',
    'unavailable',
    'insecure',
    'unsupported',
    'worklet',
    'format',
  ])('only handles /voice locally when enabled, with %s preventing capture', async (condition) => {
    if (condition === 'disabled')
      vi.mocked(apiService.getTranscriptionCapabilities).mockResolvedValue({
        ...capability,
        enabled: false,
        available: false,
      });
    if (condition === 'unavailable' || condition === 'disabled after mount')
      vi.mocked(apiService.getTranscriptionCapabilities)
        .mockResolvedValue({
          ...capability,
          enabled: condition !== 'disabled after mount',
          available: false,
          reason: 'Transcription is unavailable.',
        })
        .mockResolvedValueOnce(capability);
    if (condition === 'insecure') vi.stubGlobal('isSecureContext', false);
    if (condition === 'unsupported') vi.stubGlobal('AudioContext', undefined);
    if (condition === 'worklet') vi.stubGlobal('AudioWorkletNode', undefined);
    if (condition === 'format')
      vi.mocked(apiService.getTranscriptionCapabilities).mockResolvedValue({
        ...capability,
        mimeTypes: ['audio/webm'],
      });
    const { result } = renderDictation();
    await waitFor(() => expect(result.current.reason).not.toBe('Checking dictation availability…'));
    await act(async () =>
      expect(result.current.handleCommand('/voice')).toBe(condition !== 'disabled')
    );
    if (condition === 'disabled') expect(result.current.error).toBeNull();
    else expect(result.current.error).toBeTruthy();
    expect(getUserMedia).not.toHaveBeenCalled();
    expect(onComplete).not.toHaveBeenCalled();
    if (condition === 'disabled after mount') {
      expect(result.current.enabled).toBe(false);
      expect(result.current.handleCommand('/voice')).toBe(false);
    }
  });

  it('recognizes only the local command name, including invalid arguments', () => {
    expect(isVoiceCommand('/voice')).toBe(true);
    expect(isVoiceCommand(' /voice bad ')).toBe(true);
    expect(isVoiceCommand('/voices')).toBe(false);
    expect(isVoiceCommand('/voice/file')).toBe(false);
  });
});
