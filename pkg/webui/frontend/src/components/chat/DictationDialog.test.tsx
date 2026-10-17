import { act, fireEvent, render, screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import type { ComponentProps } from 'react';
import { describe, expect, it, vi } from 'vitest';
import DictationDialog from './DictationDialog';

const makeProps = (
  overrides: Partial<ComponentProps<typeof DictationDialog>> = {}
): ComponentProps<typeof DictationDialog> => ({
  phase: 'recording',
  error: null,
  elapsedSeconds: 7,
  levels: [0, 0.25, 0.5, 1],
  speaking: false,
  transcript: '',
  transcribing: false,
  stop: vi.fn(),
  cancel: vi.fn(),
  ...overrides,
});

describe('DictationDialog', () => {
  it('portals a centered, accessible dialog outside the composer', () => {
    const { container } = render(<DictationDialog {...makeProps()} />);
    const dialog = screen.getByRole('dialog', { name: 'Dictation' });
    expect(dialog).toHaveAttribute('aria-modal', 'true');
    expect(container).not.toContainElement(dialog);
    expect(dialog.parentElement?.parentElement).toBe(document.body);
    expect(dialog.parentElement).toHaveClass('dictation-backdrop');
    expect(screen.getByRole('status')).toHaveTextContent('Listening…');
    expect(screen.queryByText('Transcript')).not.toBeInTheDocument();
    expect(screen.getByLabelText('Recording duration: 0:07')).toBeVisible();
    expect(screen.getByRole('log', { name: 'Dictation transcript' })).toHaveTextContent(
      'Your words will appear here after a pause.'
    );
  });

  it('renders actual level samples and clears the waveform when capture stops', () => {
    const props = makeProps({ speaking: true, elapsedSeconds: 67 });
    const { rerender } = render(<DictationDialog {...props} />);
    const dialog = screen.getByRole('dialog');
    const waveform = dialog.querySelector('.dictation-waveform');
    expect(waveform).toHaveAttribute('aria-hidden', 'true');
    const bars = Array.from(dialog.querySelectorAll('.dictation-waveform-bar'));
    expect(bars).toHaveLength(32);
    expect(bars[0]).toHaveStyle({ transform: 'scaleY(0.06)' });
    expect(bars[29]).toHaveStyle({ transform: 'scaleY(0.295)' });
    expect(bars[30]).toHaveStyle({ transform: 'scaleY(0.53)' });
    expect(bars[31]).toHaveStyle({ transform: 'scaleY(1)' });
    expect(screen.getByRole('status')).toHaveTextContent('Speaking…');
    expect(screen.getByLabelText('Recording duration: 1:07')).toBeVisible();
    rerender(<DictationDialog {...props} phase="review" />);
    for (const bar of bars) expect(bar).toHaveStyle({ transform: 'scaleY(0.06)' });
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

  it('focuses the dialog, traps keyboard navigation and handles Escape', async () => {
    const user = userEvent.setup();
    const props = makeProps();
    render(<DictationDialog {...props} />);
    const cancel = screen.getByRole('button', { name: 'Cancel dictation' });
    const done = screen.getByRole('button', { name: 'Done dictating' });
    await waitFor(() => expect(screen.getByRole('dialog')).toHaveFocus());
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
    expect(props.stop).not.toHaveBeenCalled();
  });

  it('makes background content inert and restores only its own attributes and scroll lock', async () => {
    const background = document.createElement('main');
    const alreadyInert = document.createElement('section');
    const lateBackground = document.createElement('aside');
    alreadyInert.setAttribute('inert', 'existing');
    document.body.append(background, alreadyInert);
    const originalOverflow = document.body.style.overflow;
    document.body.style.overflow = 'clip';
    try {
      const { unmount } = render(<DictationDialog {...makeProps()} />);
      expect(background).toHaveAttribute('inert');
      expect(alreadyInert).toHaveAttribute('inert', 'existing');
      expect(screen.getByRole('dialog').parentElement).not.toHaveAttribute('inert');
      expect(document.body.style.overflow).toBe('hidden');
      await act(async () => {
        document.body.append(lateBackground);
      });
      expect(lateBackground).toHaveAttribute('inert');
      unmount();
      expect(background).not.toHaveAttribute('inert');
      expect(lateBackground).not.toHaveAttribute('inert');
      expect(alreadyInert).toHaveAttribute('inert', 'existing');
      expect(document.body.style.overflow).toBe('clip');
    } finally {
      background.remove();
      alreadyInert.remove();
      lateBackground.remove();
      document.body.style.overflow = originalOverflow;
    }
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
