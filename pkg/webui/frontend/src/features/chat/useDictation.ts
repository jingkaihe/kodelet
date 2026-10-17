import { useCallback, useEffect, useLayoutEffect, useRef, useState } from 'react';
import apiService from '../../services/api';
import type { TranscriptionCapabilities } from '../../types';
import { createDictationRecorder, type DictationRecorderOptions } from './dictationAudio';

export type Phase = 'idle' | 'requesting' | 'recording' | 'finishing' | 'review';

// Allow transport overhead beyond the server request deadline.
const uploadTimeoutMs = 120_000;
const waveformLength = 32;
const capabilityErrorMessage = 'Could not check dictation availability. Try again.';

function mediaSupportReason(): string | null {
  if (!globalThis.isSecureContext) return 'Microphone dictation requires HTTPS or localhost.';
  if (
    !navigator.mediaDevices?.getUserMedia ||
    typeof AudioContext === 'undefined' ||
    typeof AudioWorkletNode === 'undefined'
  ) {
    return 'This browser does not support continuous microphone dictation.';
  }
  return null;
}

export const isVoiceCommand = (text: string): boolean => /^\/voice(?:\s|$)/.test(text.trim());

interface DictationState {
  // Flushing waits for final audio; its destination records whether Done or a
  // recording limit initiated the stop. Finishing then waits only for uploads.
  phase: Phase | 'flushing' | 'flushing-review';
  transcript: string;
  error: string | null;
  uploadError: string | null;
  upload?: AbortController;
  elapsedSeconds: number;
  levels: number[];
  speaking: boolean;
}

const idleState: DictationState = {
  phase: 'idle',
  transcript: '',
  error: null,
  uploadError: null,
  elapsedSeconds: 0,
  levels: Array(waveformLength).fill(0),
  speaking: false,
};

interface Capture {
  targetKey: string;
  state: DictationState;
  recorder?: ReturnType<typeof createDictationRecorder>;
  pendingSnapshot?: File;
  finish: (commit: boolean) => void;
  timer?: ReturnType<typeof setInterval>;
  uploadTimer?: ReturnType<typeof setTimeout>;
}

function release(capture: Capture) {
  clearInterval(capture.timer);
  clearTimeout(capture.uploadTimer);
  capture.state.upload?.abort();
  capture.recorder?.cancel();
  capture.pendingSnapshot = undefined;
}

export function useDictation({
  targetKey,
  onStart,
}: {
  targetKey: string;
  // The composer snapshots its insertion point and returns the completion callback.
  onStart: (fromCommand: boolean) => (text: string) => void;
}) {
  const [capabilities, setCapabilities] = useState<TranscriptionCapabilities | null>(null);
  const [capabilityError, setCapabilityError] = useState<string | null>(null);
  const [state, setState] = useState(idleState);
  const captureRef = useRef<Capture | null>(null);
  const targetRef = useRef(targetKey);
  targetRef.current = targetKey;
  const capabilityRequest = useRef<AbortController | null>(null);

  // Callbacks need the latest snapshot before React renders. Publish that same
  // immutable snapshot, rather than maintaining separate UI and capture fields.
  const update = useCallback((patch: Partial<DictationState>) => {
    const capture = captureRef.current;
    const next = { ...(capture?.state ?? idleState), ...patch };
    if (capture) capture.state = next;
    setState(next);
  }, []);

  const refreshCapabilities = useCallback(async () => {
    capabilityRequest.current?.abort();
    const controller = new AbortController();
    capabilityRequest.current = controller;
    try {
      const result = await apiService.getTranscriptionCapabilities(controller.signal);
      if (controller.signal.aborted) return null;
      setCapabilities(result);
      setCapabilityError(null);
      return result;
    } catch {
      if (!controller.signal.aborted) setCapabilityError(capabilityErrorMessage);
      return null;
    } finally {
      if (capabilityRequest.current === controller) capabilityRequest.current = null;
    }
  }, []);

  // biome-ignore lint/correctness/useExhaustiveDependencies(targetKey): Navigation cancels discovery; refresh it for the new draft owner.
  useEffect(() => {
    const refresh = () => {
      // Returning from microphone permission must not cancel the startup check.
      if (!captureRef.current) void refreshCapabilities();
    };
    refresh();
    window.addEventListener('focus', refresh);
    return () => {
      window.removeEventListener('focus', refresh);
      capabilityRequest.current?.abort();
    };
  }, [refreshCapabilities, targetKey]);

  const discard = useCallback(() => {
    capabilityRequest.current?.abort();
    const capture = captureRef.current;
    captureRef.current = null;
    if (capture) release(capture);
  }, []);

  // Invalidate synchronously when the draft owner changes, including while
  // permission, audio initialization, or a transcription request is pending.
  // biome-ignore lint/correctness/useExhaustiveDependencies(targetKey): A different draft owner must release the previous capture.
  useLayoutEffect(() => {
    setState(idleState);
    return discard;
  }, [targetKey, discard]);

  const cancel = useCallback(() => {
    discard();
    setState(idleState);
  }, [discard]);

  const stop = useCallback(() => captureRef.current?.finish(true), []);

  const enabled = capabilities?.enabled === true;
  const reason =
    capabilityError ||
    (!capabilities
      ? 'Checking dictation availability…'
      : !capabilities.enabled || !capabilities.available
        ? capabilities.reason || 'Dictation is not configured on this server.'
        : mediaSupportReason());

  const start = async (fromCommand = false) => {
    if (captureRef.current) return;
    if (!capabilities?.enabled) {
      update({ error: reason });
      return;
    }
    const unsupported = mediaSupportReason();
    if (unsupported) {
      update({ error: unsupported });
      return;
    }
    const capture: Capture = {
      targetKey,
      state: { ...idleState, phase: 'requesting' },
      finish: () => {},
    };
    captureRef.current = capture;
    setState(capture.state);
    const onComplete = onStart(fromCommand);
    const current = () => captureRef.current === capture && targetRef.current === capture.targetKey;
    const recording = () => ['requesting', 'recording'].includes(capture.state.phase);
    const pending = () => !!capture.state.upload || !!capture.pendingSnapshot;
    const commit = () => {
      if (!current()) return;
      const text = capture.state.transcript;
      cancel();
      if (text) onComplete(text);
    };
    const settled = () => {
      if (!current() || capture.state.phase !== 'finishing' || pending()) return;
      if (capture.state.uploadError) update({ phase: 'review' });
      else commit();
    };
    const finishCapture = (commitRequested: boolean) => {
      if (!current()) return;
      if (capture.state.phase === 'review') {
        // A second, explicit Done accepts the visible partial result.
        if (commitRequested && !pending()) commit();
        else if (commitRequested) {
          update({ phase: 'finishing' });
        }
        return;
      }
      if (!recording()) return;
      clearInterval(capture.timer);
      update({ phase: commitRequested ? 'flushing' : 'flushing-review', speaking: false });
      void (async () => {
        try {
          await capture.recorder?.finish();
        } catch {
          capture.recorder?.cancel();
          if (current()) {
            update({
              phase: 'review',
              error:
                'Could not finish microphone recording. The preview may be incomplete; Done keeps the last successful transcription.',
            });
          }
        } finally {
          if (current()) {
            if (capture.state.phase === 'flushing') update({ phase: 'finishing' });
            else if (capture.state.phase === 'flushing-review') update({ phase: 'review' });
            settled();
          }
        }
      })();
    };
    capture.finish = finishCapture;

    const uploadNext = async () => {
      if (!current() || capture.state.upload) return;
      const file = capture.pendingSnapshot;
      if (!file) {
        settled();
        return;
      }
      capture.pendingSnapshot = undefined;
      const abort = new AbortController();
      update({ upload: abort });
      try {
        const result = await Promise.race([
          apiService.transcribeAudio(file, abort.signal),
          new Promise<never>((_, reject) => {
            capture.uploadTimer = setTimeout(() => {
              reject(new Error('Transcription timed out.'));
              abort.abort();
            }, uploadTimeoutMs);
          }),
        ]);
        if (!current()) return;
        // Each response covers the whole recording so far, including any
        // corrections to earlier words. Never concatenate cumulative results.
        update({ transcript: result.text, uploadError: null });
      } catch (cause) {
        if (!current()) return;
        const message =
          cause instanceof Error ? cause.message : 'Could not transcribe the recording.';
        update({
          uploadError: capture.state.transcript
            ? message +
              ' The preview may be incomplete. Done keeps the last successful transcription.'
            : message,
        });
      } finally {
        clearTimeout(capture.uploadTimer);
        if (current()) {
          update({ upload: undefined });
          void uploadNext();
        }
      }
    };

    try {
      const recorderOptions: DictationRecorderOptions = {
        maxAudioBytes: capabilities.maxAudioBytes,
        maxDurationSeconds: capabilities.maxDurationSeconds,
        onLevel: (level) => {
          if (current() && recording())
            update({ levels: [...capture.state.levels.slice(1), level] });
        },
        onSpeech: (active) => {
          if (current() && recording()) update({ speaking: active });
        },
        onSnapshot: (file) => {
          if (!current() || ['finishing', 'review'].includes(capture.state.phase)) return;
          // Keep one in-flight upload and only the newest waiting snapshot.
          // Intermediate snapshots are redundant: the newest contains them all.
          capture.pendingSnapshot = file;
          void uploadNext();
        },
        onLimit: () => {
          if (!current() || !recording()) return;
          update({
            error:
              'The recording limit was reached. Review your transcript and choose Done to insert it.',
          });
          finishCapture(false);
        },
        onError: (message) => {
          if (!current()) return;
          clearInterval(capture.timer);
          update({ phase: 'review', error: message, speaking: false });
          capture.recorder?.cancel();
        },
      };
      // Resume in the user gesture, before the network check. The recorder reads
      // limits only in start(stream), after the fresh capabilities arrive.
      capture.recorder = createDictationRecorder(recorderOptions);
      const fresh = await refreshCapabilities();
      if (!current()) return;
      if (!fresh) throw new Error(capabilityErrorMessage);
      if (!fresh.enabled || !fresh.available) {
        throw new Error(fresh.reason || 'Dictation is not configured on this server.');
      }
      if (!fresh.mimeTypes.includes('audio/wav')) {
        throw new Error('The server does not support WAV audio for continuous dictation.');
      }
      recorderOptions.maxAudioBytes = fresh.maxAudioBytes;
      recorderOptions.maxDurationSeconds = fresh.maxDurationSeconds;
      const stream = await navigator.mediaDevices.getUserMedia({ audio: true });
      if (!current()) {
        stream.getTracks().forEach((track) => {
          track.stop();
        });
        return;
      }
      await capture.recorder.start(stream);
      if (!current() || !recording()) return;
      update({ phase: 'recording' });
      const started = Date.now();
      const duration = recorderOptions.maxDurationSeconds;
      capture.timer = setInterval(() => {
        if (!current()) return;
        const elapsed = (Date.now() - started) / 1000;
        update({ elapsedSeconds: Math.floor(elapsed) });
        // Sample counts bound audio memory; wall time also bounds stalled capture.
        if (elapsed >= duration) recorderOptions.onLimit();
      }, 250);
    } catch (cause) {
      if (!current()) return;
      discard();
      update({
        error:
          cause instanceof DOMException && cause.name === 'NotAllowedError'
            ? 'Microphone access was denied. Allow microphone access in your browser and try again.'
            : cause instanceof Error
              ? cause.message
              : 'Could not start microphone recording.',
      });
    }
  };

  const handleCommand = (text: string): boolean => {
    if (!enabled || !isVoiceCommand(text)) return false;
    if (text.trim() !== '/voice') update({ error: 'Usage: /voice' });
    else void start(true);
    return true;
  };

  const phase =
    state.phase === 'flushing' || state.phase === 'flushing-review' ? 'finishing' : state.phase;
  return {
    phase,
    busy: phase !== 'idle',
    // Unlike the rendered busy state, this guards same-tick starts/submissions.
    isBusy: () => captureRef.current !== null,
    enabled,
    reason,
    error: [state.error, state.uploadError].filter(Boolean).join(' ') || null,
    elapsedSeconds: state.elapsedSeconds,
    levels: state.levels,
    speaking: state.speaking,
    transcript: state.transcript,
    transcribing: !!state.upload,
    start: () => void start(),
    stop,
    cancel,
    handleCommand,
  };
}
