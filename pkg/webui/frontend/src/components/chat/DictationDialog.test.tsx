import { act, fireEvent, render, screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import type { ComponentProps } from 'react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { waveformLength } from '../../features/chat/useDictation';
import DictationDialog from './DictationDialog';

const makeProps = (
  overrides: Partial<ComponentProps<typeof DictationDialog>> = {}
): ComponentProps<typeof DictationDialog> => ({
  phase: 'recording',
  error: null,
  elapsedSeconds: 7,
  levelsRef: { current: Array<number>(waveformLength).fill(0.5) },
  transcript: '',
  transcribing: false,
  stop: vi.fn(),
  cancel: vi.fn(),
  onReturnFocus: vi.fn(),
  ...overrides,
});

describe('DictationDialog', () => {
  it('renders a centered, accessible dialog', () => {
    const { container } = render(<DictationDialog {...makeProps()} />);
    const dialog = screen.getByRole('dialog', { name: 'Dictation' });
    expect(dialog).toHaveAttribute('aria-modal', 'true');
    expect(container).toContainElement(dialog);
    expect(dialog.parentElement).toHaveClass('dictation-backdrop');
    expect(screen.getByRole('status')).toHaveTextContent('Listening…');
    expect(screen.queryByText('Transcript')).not.toBeInTheDocument();
    expect(screen.getByLabelText('Recording duration: 0:07')).toBeVisible();
    expect(screen.getByRole('log', { name: 'Dictation transcript' })).toHaveTextContent(
      'Your words will appear here after a pause.'
    );
  });

  describe('quieter waveform', () => {
    let nextFrame: FrameRequestCallback;
    let frameID: number;

    beforeEach(() => {
      frameID = 0;
      vi.spyOn(performance, 'now').mockReturnValue(0);
      vi.spyOn(window, 'requestAnimationFrame').mockImplementation((callback) => {
        nextFrame = callback;
        return ++frameID;
      });
      vi.spyOn(window, 'cancelAnimationFrame').mockImplementation(() => {});
    });

    afterEach(() => vi.restoreAllMocks());

    it('smooths peaks in space and time, keeps feedback steady, and clears on stop', () => {
      const props = makeProps({ elapsedSeconds: 67 });
      props.levelsRef.current.fill(0);
      props.levelsRef.current[2] = 1;
      const { rerender } = render(<DictationDialog {...props} />);
      const waveform = screen.getByRole('dialog').querySelector('.dictation-waveform');
      expect(waveform).toHaveAttribute('aria-hidden', 'true');
      expect(waveform).toHaveAttribute('data-recording', 'true');
      const bars = Array.from(
        screen.getByRole('dialog').querySelectorAll<HTMLElement>('.dictation-waveform-bar')
      );
      expect(bars).toHaveLength(waveformLength);
      const scale = (bar: HTMLElement) => Number(bar.style.transform.slice(7, -1));

      act(() => nextFrame(16));
      const blend = 1 - Math.exp(-16 / 110);
      for (const [slot, target] of [0, 0.25, 0.5, 0.25, 0].entries()) {
        expect(scale(bars[slot])).toBeCloseTo(0.06 + target * blend * 0.94);
      }
      const firstHeight = scale(bars[2]);
      act(() => nextFrame(32));
      expect(scale(bars[2])).toBeGreaterThan(firstHeight);
      expect(scale(bars[2])).toBeLessThan(0.53);
      expect(screen.getByRole('status')).toHaveTextContent('Listening…');
      expect(screen.getByLabelText('Recording duration: 1:07')).toBeVisible();

      const peak = scale(bars[2]);
      props.levelsRef.current.fill(0);
      act(() => nextFrame(48));
      expect(scale(bars[2])).toBeLessThan(peak);
      expect(scale(bars[2])).toBeGreaterThan(0.06);
      expect(screen.getByRole('status')).toHaveTextContent('Listening…');
      expect(waveform).toHaveAttribute('data-recording', 'true');

      const pending = frameID;
      rerender(<DictationDialog {...props} phase="review" />);
      expect(window.cancelAnimationFrame).toHaveBeenCalledWith(pending);
      expect(waveform).toHaveAttribute('data-recording', 'false');
      for (const bar of bars) expect(bar).toHaveStyle({ transform: 'scaleY(0.06)' });
    });

    it('uses a static baseline for reduced motion and responds to preference changes', () => {
      const motion = {
        ...window.matchMedia('(prefers-reduced-motion: reduce)'),
        matches: true,
      };
      vi.mocked(window.matchMedia).mockReturnValueOnce(motion);
      const { unmount } = render(<DictationDialog {...makeProps()} />);
      const bars = screen.getByRole('dialog').querySelectorAll('.dictation-waveform-bar');
      expect(window.requestAnimationFrame).not.toHaveBeenCalled();
      for (const bar of bars) expect(bar).toHaveStyle({ transform: 'scaleY(0.06)' });
      expect(screen.getByRole('status')).toHaveTextContent('Listening…');

      const change = vi.mocked(motion.addEventListener).mock.calls[0][1] as () => void;
      motion.matches = false;
      act(change);
      expect(window.requestAnimationFrame).toHaveBeenCalledOnce();
      act(() => nextFrame(16));
      const pending = frameID;
      motion.matches = true;
      act(change);
      expect(window.cancelAnimationFrame).toHaveBeenCalledWith(pending);
      for (const bar of bars) expect(bar).toHaveStyle({ transform: 'scaleY(0.06)' });
      unmount();
      expect(motion.removeEventListener).toHaveBeenCalledWith('change', change);
    });

    it('cancels the pending animation on unmount', () => {
      const { unmount } = render(<DictationDialog {...makeProps()} />);
      act(() => nextFrame(16));
      const pending = frameID;
      unmount();
      expect(window.cancelAnimationFrame).toHaveBeenCalledWith(pending);
    });
  });

  it.each([
    { phase: 'requesting' as const, status: 'Waiting for microphone permission…', disabled: true },
    { phase: 'recording' as const, status: 'Listening…', disabled: false },
    { phase: 'finishing' as const, status: 'Finishing transcription…', disabled: true },
    { phase: 'review' as const, status: 'Ready to review', disabled: false },
  ])('shows $phase state and the correct Done availability', ({ phase, status, disabled }) => {
    render(<DictationDialog {...makeProps({ phase })} />);
    expect(screen.getByRole('status')).toHaveTextContent(status);
    const done = screen.getByRole('button', { name: 'Done dictating' });
    if (disabled) expect(done).toBeDisabled();
    else expect(done).toBeEnabled();
    expect(screen.getByRole('button', { name: 'Cancel dictation' })).toBeEnabled();
  });

  it('keeps the preview visible with accessible upload and warning states', () => {
    const props = makeProps({
      phase: 'finishing',
      transcript: 'Previous transcription.',
      transcribing: true,
    });
    const { rerender } = render(<DictationDialog {...props} />);
    expect(screen.getByRole('dialog')).toHaveAccessibleDescription(
      'Microphone off. Waiting for the final transcription.'
    );
    expect(screen.getByText('Transcribing…')).toBeVisible();
    expect(screen.getByRole('log')).toHaveTextContent('Previous transcription.');
    rerender(
      <DictationDialog
        {...props}
        phase="review"
        transcribing={false}
        error="Provider unavailable. Review the previous transcription."
      />
    );
    expect(screen.getByRole('alert')).toHaveTextContent(
      'Provider unavailable. Review the previous transcription.'
    );
    expect(screen.getByRole('log')).toHaveTextContent('Previous transcription.');
    expect(screen.queryByText('Transcribing…')).not.toBeInTheDocument();
  });

  it('owns Done/Cancel keys, traps navigation, and returns focus once on close', async () => {
    const user = userEvent.setup();
    const props = makeProps();
    const { unmount } = render(<DictationDialog {...props} />);
    const cancel = screen.getByRole('button', { name: 'Cancel dictation' });
    const done = screen.getByRole('button', { name: 'Done dictating' });
    await waitFor(() => expect(screen.getByRole('dialog')).toHaveFocus());
    for (const key of ['Enter', 'Escape']) {
      fireEvent.keyDown(screen.getByRole('dialog'), { key, isComposing: true });
      fireEvent.keyDown(screen.getByRole('dialog'), { key, repeat: true });
    }
    expect(props.stop).not.toHaveBeenCalled();
    expect(props.cancel).not.toHaveBeenCalled();
    await user.keyboard('{Enter}');
    expect(props.stop).toHaveBeenCalledOnce();
    await user.tab();
    expect(cancel).toHaveFocus();
    await user.tab({ shift: true });
    expect(done).toHaveFocus();
    screen.getByRole('dialog').focus();
    await user.tab({ shift: true });
    expect(done).toHaveFocus();
    await user.tab();
    expect(cancel).toHaveFocus();
    await user.keyboard('{Escape}');
    expect(props.cancel).toHaveBeenCalledOnce();
    unmount();
    await waitFor(() => expect(props.onReturnFocus).toHaveBeenCalledOnce());
  });

  it('follows new text only while the reader is at the bottom', () => {
    const props = makeProps();
    const { rerender } = render(<DictationDialog {...props} />);
    const transcript = screen.getByRole('log');
    Object.defineProperties(transcript, {
      scrollHeight: { configurable: true, value: 1000 },
      clientHeight: { configurable: true, value: 100 },
    });
    rerender(<DictationDialog {...props} transcript="One." />);
    expect(transcript.scrollTop).toBe(1000);
    transcript.scrollTop = 100;
    fireEvent.scroll(transcript);
    rerender(<DictationDialog {...props} transcript="One. Two." />);
    expect(transcript.scrollTop).toBe(100);
    transcript.scrollTop = 900;
    fireEvent.scroll(transcript);
    rerender(<DictationDialog {...props} transcript="One. Two. Three." />);
    expect(transcript.scrollTop).toBe(1000);
  });
});
