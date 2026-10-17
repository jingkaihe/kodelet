import { Check, Mic } from 'lucide-react';
import { useEffect, useLayoutEffect, useRef } from 'react';
import { createPortal } from 'react-dom';
import { useDialogFocus } from '../../features/chat/useDialogFocus';
import Spinner from '../Spinner';

interface DictationDialogProps {
  phase: 'idle' | 'requesting' | 'recording' | 'finishing' | 'review';
  error: string | null;
  elapsedSeconds: number;
  levels: number[];
  speaking: boolean;
  transcript: string;
  transcribing: boolean;
  stop: () => void;
  cancel: () => void;
}

const waveformSlots = Array.from({ length: 32 }, (_, index) => index);

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
}: DictationDialogProps) => {
  const { dialogRef } = useDialogFocus(cancel, '[data-testid="composer-textarea"]', true);
  const backdropRef = useRef<HTMLDivElement | null>(null);
  const transcriptRef = useRef<HTMLDivElement | null>(null);
  const followTranscript = useRef(true);

  useEffect(() => {
    // Enter starts as Done, not the native activation of the Cancel button.
    // The shared helper still handles focus trapping and restoration.
    dialogRef.current?.focus();
  }, [dialogRef]);

  useLayoutEffect(() => {
    // The portal is a body child, outside the sticky composer and inert app.
    // Only undo inert attributes we own; another dialog may already own one.
    const changed = new Set<Element>();
    const makeBackgroundInert = () => {
      for (const child of document.body.children) {
        if (child !== backdropRef.current && !child.hasAttribute('inert')) {
          child.setAttribute('inert', '');
          changed.add(child);
        }
      }
    };
    makeBackgroundInert();
    const observer = new MutationObserver(makeBackgroundInert);
    observer.observe(document.body, { childList: true });
    const previousOverflow = document.body.style.overflow;
    document.body.style.overflow = 'hidden';
    return () => {
      observer.disconnect();
      for (const child of changed) child.removeAttribute('inert');
      document.body.style.overflow = previousOverflow;
    };
  }, []);

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
  const recentLevels = levels.slice(-waveformSlots.length);

  return createPortal(
    <div className="new-chat-dialog-backdrop dictation-backdrop" ref={backdropRef}>
      <div
        aria-describedby={phase === 'recording' ? undefined : 'dictation-description'}
        aria-labelledby="dictation-title"
        aria-modal="true"
        className="dictation-dialog surface-panel"
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
            {waveformSlots.map((slot) => {
              const level =
                phase === 'recording'
                  ? (recentLevels[slot - (waveformSlots.length - recentLevels.length)] ?? 0)
                  : 0;
              return (
                <span
                  className="dictation-waveform-bar"
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
    </div>,
    document.body
  );
};

export default DictationDialog;
