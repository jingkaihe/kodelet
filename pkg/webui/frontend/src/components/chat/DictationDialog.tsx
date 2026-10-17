import { Check, Mic } from 'lucide-react';
import { useEffect, useEffectEvent, useLayoutEffect, useRef } from 'react';
import { useDialogFocus } from '../../features/chat/useDialogFocus';
import type { Phase } from '../../features/chat/useDictation';
import Spinner from '../Spinner';

interface DictationDialogProps {
  phase: Phase;
  error: string | null;
  elapsedSeconds: number;
  levels: number[];
  transcript: string;
  transcribing: boolean;
  stop: () => void;
  cancel: () => void;
  onReturnFocus: () => void;
}

const DictationDialog = ({
  phase,
  error,
  elapsedSeconds,
  levels,
  transcript,
  transcribing,
  stop,
  cancel,
  onReturnFocus,
}: DictationDialogProps) => {
  const { dialogRef } = useDialogFocus(undefined, onReturnFocus);
  const transcriptRef = useRef<HTMLDivElement | null>(null);
  const followTranscript = useRef(true);
  const waveformRef = useRef<HTMLDivElement | null>(null);
  const displayedLevels = useRef<number[]>([]);

  const paintWaveform = useEffectEvent((blend: number, active: boolean) => {
    Array.from(waveformRef.current?.children ?? []).forEach((bar, slot) => {
      const sample = levels[slot];
      // Soften isolated peaks without changing the audio used for transcription.
      const target = active
        ? ((levels[slot - 1] ?? sample) + sample * 2 + (levels[slot + 1] ?? sample)) / 4
        : 0;
      const previous = displayedLevels.current[slot] ?? 0;
      const level = previous + (target - previous) * blend;
      displayedLevels.current[slot] = level;
      (bar as HTMLElement).style.transform = `scaleY(${0.06 + level * 0.94})`;
    });
  });

  useEffect(() => {
    if (phase !== 'recording') {
      paintWaveform(1, false);
      return;
    }
    const motion = window.matchMedia('(prefers-reduced-motion: reduce)');
    let animation = 0;
    let previous = performance.now();
    const animate = (now: number) => {
      // Match the approved prototype's easing at any display refresh rate.
      paintWaveform(1 - Math.exp(-Math.min(now - previous, 100) / 110), true);
      previous = now;
      animation = requestAnimationFrame(animate);
    };
    const updateMotion = () => {
      cancelAnimationFrame(animation);
      if (motion.matches) {
        paintWaveform(1, false);
      } else {
        previous = performance.now();
        animation = requestAnimationFrame(animate);
      }
    };
    updateMotion();
    motion.addEventListener('change', updateMotion);
    return () => {
      cancelAnimationFrame(animation);
      motion.removeEventListener('change', updateMotion);
    };
  }, [phase]);

  // biome-ignore lint/correctness/useExhaustiveDependencies(transcript): Follow revised transcript text, but leave a reader's scroll position alone.
  useLayoutEffect(() => {
    const preview = transcriptRef.current;
    if (preview && followTranscript.current) preview.scrollTop = preview.scrollHeight;
  }, [transcript]);

  const status =
    phase === 'requesting'
      ? 'Waiting for microphone permission…'
      : phase === 'finishing'
        ? 'Finishing transcription…'
        : phase === 'review'
          ? 'Ready to review'
          : 'Listening…';
  const duration = `${Math.floor(elapsedSeconds / 60)}:${String(elapsedSeconds % 60).padStart(2, '0')}`;

  return (
    <div className="new-chat-dialog-backdrop dictation-backdrop">
      <div
        aria-describedby={phase === 'recording' ? undefined : 'dictation-description'}
        aria-labelledby="dictation-title"
        aria-modal="true"
        className="dictation-dialog surface-panel"
        onKeyDown={(event) => {
          if (event.nativeEvent.isComposing || (event.key !== 'Enter' && event.key !== 'Escape'))
            return;
          // Let a focused button activate natively, especially Enter on Cancel.
          if (
            event.key === 'Enter' &&
            !event.repeat &&
            event.target instanceof Element &&
            event.target.closest('button')
          )
            return;
          event.preventDefault();
          event.stopPropagation();
          if (event.repeat) return;
          if (event.key === 'Escape') cancel();
          else if (phase === 'recording' || phase === 'review') stop();
        }}
        ref={dialogRef}
        role="dialog"
        tabIndex={-1}
      >
        <header className="dictation-header">
          <h2 id="dictation-title">
            <Mic aria-hidden="true" className="h-4 w-4" strokeWidth={1.8} />
            Dictation
          </h2>
          <span
            aria-label={`Recording duration: ${duration}`}
            className="dictation-duration"
            role="timer"
          >
            {duration}
          </span>
        </header>

        <div className="dictation-visualizer">
          <div
            aria-hidden="true"
            className="dictation-waveform"
            data-recording={phase === 'recording'}
            ref={waveformRef}
          >
            {levels.map((_, slot) => (
              <span
                className="dictation-waveform-bar"
                // biome-ignore lint/suspicious/noArrayIndexKey: Bars are fixed display slots, not sample identities.
                key={slot}
              />
            ))}
          </div>
          <output aria-live="polite" className="dictation-status">
            {status}
          </output>
          {phase !== 'recording' ? (
            <p className="dictation-description" id="dictation-description">
              {phase === 'requesting'
                ? 'Allow microphone access to start dictating.'
                : phase === 'finishing' || transcribing
                  ? 'Microphone off. Waiting for the final transcription.'
                  : 'Microphone off. Review your transcription.'}
            </p>
          ) : null}
        </div>

        <div className="dictation-preview">
          {transcribing ? (
            <div aria-live="polite" className="dictation-transcribing">
              <Spinner /> Transcribing…
            </div>
          ) : null}
          <div
            aria-label="Dictation transcript"
            aria-live="polite"
            aria-relevant="additions text"
            className="dictation-transcript"
            onScroll={(event) => {
              const { scrollHeight, scrollTop, clientHeight } = event.currentTarget;
              followTranscript.current = scrollHeight - scrollTop - clientHeight < 32;
            }}
            ref={transcriptRef}
            role="log"
          >
            {transcript ? (
              <p>{transcript}</p>
            ) : (
              <p className="dictation-transcript-empty">
                Your words will appear here after a pause.
              </p>
            )}
          </div>
        </div>

        {error ? (
          <div className="dictation-warning" role="alert">
            <p>{error}</p>
          </div>
        ) : null}

        <footer className="dictation-footer">
          <div className="dictation-actions">
            <button
              aria-label="Cancel dictation"
              className="dictation-button"
              onClick={cancel}
              type="button"
            >
              Cancel <kbd>Esc</kbd>
            </button>
            <button
              aria-label="Done dictating"
              className="dictation-button dictation-button-done"
              disabled={phase === 'requesting' || phase === 'finishing'}
              onClick={stop}
              type="button"
            >
              <Check aria-hidden="true" className="h-4 w-4" />
              Done <kbd>↵</kbd>
            </button>
          </div>
        </footer>
      </div>
    </div>
  );
};

export default DictationDialog;
