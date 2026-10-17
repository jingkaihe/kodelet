import { Check, Mic } from 'lucide-react';
import { useLayoutEffect, useRef } from 'react';
import { useDialogFocus } from '../../features/chat/useDialogFocus';
import type { Phase } from '../../features/chat/useDictation';
import Spinner from '../Spinner';

interface DictationDialogProps {
  phase: Phase;
  error: string | null;
  elapsedSeconds: number;
  levels: number[];
  speaking: boolean;
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
  speaking,
  transcript,
  transcribing,
  stop,
  cancel,
  onReturnFocus,
}: DictationDialogProps) => {
  const { dialogRef } = useDialogFocus(undefined, onReturnFocus);
  const transcriptRef = useRef<HTMLDivElement | null>(null);
  const followTranscript = useRef(true);

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
          : speaking
            ? 'Speaking…'
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
          <div aria-hidden="true" className="dictation-waveform" data-speaking={speaking}>
            {levels.map((sample, slot) => {
              const level = phase === 'recording' ? sample : 0;
              return (
                <span
                  className="dictation-waveform-bar"
                  // biome-ignore lint/suspicious/noArrayIndexKey: Bars are fixed display slots, not sample identities.
                  key={slot}
                  style={{ transform: `scaleY(${0.06 + level * 0.94})` }}
                />
              );
            })}
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
