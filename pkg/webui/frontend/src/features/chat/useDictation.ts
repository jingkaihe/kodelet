import { useCallback, useEffect, useLayoutEffect, useRef, useState } from 'react';
import apiService from '../../services/api';
import type { TranscriptionCapabilities } from '../../types';
import { createDictationRecorder, type DictationRecorderOptions } from './dictationAudio';

export type Phase = 'idle' | 'requesting' | 'recording' | 'finishing' | 'review';

const maxAudioBytes = 20 * 1024 * 1024;
const maxDurationSeconds = 120;
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

interface Capture {
  targetKey: string;
  recorder?: ReturnType<typeof createDictationRecorder>;
  transcript: string;
  pendingSnapshot?: File;
  uploadError: string | null;
  finish: (commit: boolean) => void;
  stopping: boolean;
  captureDone: boolean;
  commitRequested: boolean;
  uploading: boolean;
  uploadAbort?: AbortController;
  timer?: ReturnType<typeof setInterval>;
  uploadTimer?: ReturnType<typeof setTimeout>;
}

function release(capture: Capture) {
  clearInterval(capture.timer);
  clearTimeout(capture.uploadTimer);
  capture.uploadAbort?.abort();
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
  const [phase, setPhase] = useState<Phase>('idle');
  const [elapsedSeconds, setElapsedSeconds] = useState(0);
  const [error, setError] = useState<string | null>(null);
  const [transcript, setTranscript] = useState('');
  const [transcribing, setTranscribing] = useState(false);
  const [uploadError, setUploadError] = useState<string | null>(null);
  const [levels, setLevels] = useState<number[]>(() => Array(waveformLength).fill(0));
  const [speaking, setSpeaking] = useState(false);
  const captureRef = useRef<Capture | null>(null);
  const targetRef = useRef(targetKey);
  targetRef.current = targetKey;
  const capabilityRequest = useRef<AbortController | null>(null);

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
    setPhase('idle');
    setError(null);
    setTranscript('');
    setTranscribing(false);
    setUploadError(null);
    return discard;
  }, [targetKey, discard]);

  const cancel = useCallback(() => {
    discard();
    setPhase('idle');
    setError(null);
    setTranscript('');
    setTranscribing(false);
    setUploadError(null);
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
    if (!enabled) {
      setError(reason);
      return;
    }
    const unsupported = mediaSupportReason();
    if (unsupported) {
      setError(unsupported);
      return;
    }
    const capture: Capture = {
      targetKey,
      transcript: '',
      uploadError: null,
      finish: () => {},
      stopping: false,
      captureDone: false,
      commitRequested: false,
      uploading: false,
    };
    captureRef.current = capture;
    const onComplete = onStart(fromCommand);
    const current = () => captureRef.current === capture && targetRef.current === capture.targetKey;
    const pending = () => capture.uploading || !!capture.pendingSnapshot;
    const commit = () => {
      if (!current()) return;
      const text = capture.transcript;
      captureRef.current = null;
      release(capture);
      setPhase('idle');
      setError(null);
      setUploadError(null);
      setTranscribing(false);
      if (text) onComplete(text);
    };
    const settled = () => {
      if (!current() || !capture.captureDone || pending()) return;
      if (capture.commitRequested) {
        if (capture.uploadError) {
          capture.commitRequested = false;
        } else {
          commit();
          return;
        }
      }
      setPhase('review');
    };
    const finishCapture = (commitRequested: boolean) => {
      if (!current()) return;
      if (capture.captureDone) {
        // A second, explicit Done accepts the visible partial result.
        if (commitRequested && !pending()) commit();
        else if (commitRequested) {
          capture.commitRequested = true;
          setPhase('finishing');
        }
        return;
      }
      if (capture.stopping) return;
      capture.stopping = true;
      capture.commitRequested = commitRequested;
      clearInterval(capture.timer);
      setSpeaking(false);
      setPhase('finishing');
      void (async () => {
        try {
          await capture.recorder?.finish();
        } catch {
          capture.recorder?.cancel();
          if (current()) {
            capture.commitRequested = false;
            setError(
              'Could not finish microphone recording. The preview may be incomplete; Done keeps the last successful transcription.'
            );
          }
        } finally {
          if (current()) {
            capture.captureDone = true;
            if (!capture.commitRequested) setPhase('review');
            settled();
          }
        }
      })();
    };
    capture.finish = finishCapture;

    const uploadNext = async () => {
      if (!current() || capture.uploading) return;
      const file = capture.pendingSnapshot;
      if (!file) {
        setTranscribing(false);
        settled();
        return;
      }
      capture.pendingSnapshot = undefined;
      capture.uploading = true;
      setTranscribing(true);
      const abort = new AbortController();
      capture.uploadAbort = abort;
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
        capture.transcript = result.text;
        capture.uploadError = null;
        setTranscript(result.text);
        setUploadError(null);
      } catch (cause) {
        if (!current()) return;
        const message =
          cause instanceof Error ? cause.message : 'Could not transcribe the recording.';
        capture.uploadError = capture.transcript
          ? message +
            ' The preview may be incomplete. Done keeps the last successful transcription.'
          : message;
        setUploadError(capture.uploadError);
      } finally {
        clearTimeout(capture.uploadTimer);
        capture.uploading = false;
        if (current()) {
          void uploadNext();
        }
      }
    };

    setError(null);
    setTranscript('');
    setTranscribing(false);
    setUploadError(null);
    setElapsedSeconds(0);
    setLevels(Array(waveformLength).fill(0));
    setSpeaking(false);
    setPhase('requesting');
    try {
      const recorderOptions: DictationRecorderOptions = {
        maxAudioBytes,
        maxDurationSeconds,
        onLevel: (level) => {
          if (current() && !capture.stopping) setLevels((value) => [...value.slice(1), level]);
        },
        onSpeech: (active) => {
          if (current() && !capture.stopping) setSpeaking(active);
        },
        onSnapshot: (file) => {
          if (!current() || capture.captureDone) return;
          // Keep one in-flight upload and only the newest waiting snapshot.
          // Intermediate snapshots are redundant: the newest contains them all.
          capture.pendingSnapshot = file;
          void uploadNext();
        },
        onLimit: () => {
          if (!current() || capture.stopping) return;
          setError(
            'The recording limit was reached. Review your transcript and choose Done to insert it.'
          );
          finishCapture(false);
        },
        onError: (message) => {
          if (!current()) return;
          setError(message);
          capture.commitRequested = false;
          capture.stopping = true;
          clearInterval(capture.timer);
          capture.recorder?.cancel();
          capture.captureDone = true;
          setSpeaking(false);
          setPhase('review');
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
      recorderOptions.maxAudioBytes = Math.min(fresh.maxAudioBytes || maxAudioBytes, maxAudioBytes);
      recorderOptions.maxDurationSeconds = Math.min(
        fresh.maxDurationSeconds || maxDurationSeconds,
        maxDurationSeconds
      );
      const stream = await navigator.mediaDevices.getUserMedia({ audio: true });
      if (!current()) {
        stream.getTracks().forEach((track) => {
          track.stop();
        });
        return;
      }
      await capture.recorder.start(stream);
      if (!current() || capture.stopping) return;
      setPhase('recording');
      const started = Date.now();
      const duration = recorderOptions.maxDurationSeconds;
      capture.timer = setInterval(() => {
        if (!current()) return;
        const elapsed = (Date.now() - started) / 1000;
        setElapsedSeconds(Math.floor(elapsed));
        if (elapsed >= duration) {
          setError(
            'The recording limit was reached. Review your transcript and choose Done to insert it.'
          );
          finishCapture(false);
        }
      }, 250);
    } catch (cause) {
      if (!current()) return;
      discard();
      setPhase('idle');
      setError(
        cause instanceof DOMException && cause.name === 'NotAllowedError'
          ? 'Microphone access was denied. Allow microphone access in your browser and try again.'
          : cause instanceof Error
            ? cause.message
            : 'Could not start microphone recording.'
      );
    }
  };

  const handleCommand = (text: string): boolean => {
    if (!enabled || !isVoiceCommand(text)) return false;
    if (text.trim() !== '/voice') setError('Usage: /voice');
    else void start(true);
    return true;
  };

  return {
    phase,
    busy: phase !== 'idle',
    isBusy: () => captureRef.current !== null,
    enabled,
    reason,
    error: [error, uploadError].filter(Boolean).join(' ') || null,
    elapsedSeconds,
    levels,
    speaking,
    transcript,
    transcribing,
    start: () => void start(),
    stop,
    cancel,
    handleCommand,
  };
}
