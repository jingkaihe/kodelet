import { act, fireEvent, render, screen, waitFor, within } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import type { ComponentProps } from 'react';
import { describe, expect, it, vi } from 'vitest';
import ChatComposer from './ChatComposer';
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
  it('portals the centered dialog outside its composer and displays listening guidance', () => {
    const { container } = render(<DictationDialog {...makeProps()} />);
    const dialog = screen.getByRole('dialog', { name: 'Dictation' });
    expect(dialog).toHaveAttribute('aria-modal', 'true');
    expect(container).not.toContainElement(dialog);
    expect(dialog.parentElement?.parentElement).toBe(document.body);
    expect(dialog.parentElement).toHaveClass('dictation-backdrop');
    expect(screen.getByRole('status')).toHaveTextContent('Listening…');
    expect(screen.queryByText(/Pause to transcribe/)).not.toBeInTheDocument();
    expect(screen.queryByText(/Added to your draft/)).not.toBeInTheDocument();
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

  it('keeps the previous transcript visible while updating and replaces it with revised text', () => {
    const props = makeProps({
      transcript: 'First sentence.',
    });
    const { rerender } = render(<DictationDialog {...props} />);
    const transcript = screen.getByRole('log', { name: 'Dictation transcript' });
    expect(transcript).toHaveTextContent('First sentence.');
    expect(screen.queryByText('Transcribing…')).not.toBeInTheDocument();
    rerender(<DictationDialog {...props} transcribing />);
    expect(transcript).toHaveTextContent('First sentence.');
    expect(screen.getByText('Transcribing…')).toBeVisible();
    expect(screen.getByRole('status')).toHaveTextContent('Listening…');
    expect(screen.queryByText(/part|queued|pending/i)).not.toBeInTheDocument();
    expect(screen.queryByText('Transcript')).not.toBeInTheDocument();
    expect(within(transcript).queryByRole('list')).not.toBeInTheDocument();
    expect(screen.queryByRole('alert')).not.toBeInTheDocument();
    expect(screen.getByRole('button', { name: 'Done dictating' })).toBeEnabled();
    rerender(
      <DictationDialog {...props} transcript="Corrected first sentence. Second sentence." />
    );
    expect(transcript.textContent).toBe('Corrected first sentence. Second sentence.');
    expect(screen.queryByText('First sentence.')).not.toBeInTheDocument();
    expect(screen.queryByText('Transcribing…')).not.toBeInTheDocument();
  });

  it('shows only the supplied warning and preserves the previous transcript', () => {
    render(
      <DictationDialog
        {...makeProps({
          phase: 'review',
          transcript: 'Previous transcription.',
          error: 'Provider unavailable. Review the previous transcription.',
        })}
      />
    );
    expect(screen.getByRole('alert').textContent).toBe(
      'Provider unavailable. Review the previous transcription.'
    );
    expect(screen.getByRole('log')).toHaveTextContent('Previous transcription.');
    expect(screen.queryByText(/part|queued|pending/i)).not.toBeInTheDocument();
  });

  it.each([
    'finishing',
    'review',
  ] as const)('describes the final upload accurately in %s without hiding the preview', (phase) => {
    render(
      <DictationDialog
        {...makeProps({ phase, transcribing: true, transcript: 'Previous transcription.' })}
      />
    );
    expect(screen.getByRole('dialog')).toHaveAccessibleDescription(
      'Microphone off. Waiting for the final transcription.'
    );
    expect(screen.getByText('Transcribing…')).toBeVisible();
    expect(screen.getByRole('log')).toHaveTextContent('Previous transcription.');
  });

  it('shows a simple upload indicator before the first transcription arrives', () => {
    render(<DictationDialog {...makeProps({ transcribing: true })} />);
    expect(screen.getByText('Transcribing…')).toBeVisible();
    expect(screen.getByRole('log')).toHaveTextContent('Your words will appear here after a pause.');
    expect(screen.queryByText(/part|queued|pending/i)).not.toBeInTheDocument();
  });

  it('shows recording-limit notices without claiming successfully captured speech is missing', () => {
    render(
      <DictationDialog {...makeProps({ phase: 'review', error: 'Recording limit reached.' })} />
    );
    expect(screen.getByRole('alert')).toHaveTextContent('Recording limit reached.');
    expect(screen.queryByText(/Some speech may be missing/)).not.toBeInTheDocument();
  });

  it('delegates Done and Cancel without changing the draft itself', async () => {
    const user = userEvent.setup();
    const props = makeProps();
    render(<DictationDialog {...props} />);
    await user.click(screen.getByRole('button', { name: 'Done dictating' }));
    expect(props.stop).toHaveBeenCalledOnce();
    expect(props.cancel).not.toHaveBeenCalled();
    await user.click(screen.getByRole('button', { name: 'Cancel dictation' }));
    expect(props.cancel).toHaveBeenCalledOnce();
  });

  it('traps focus in both directions and restores the previous focus on unmount', async () => {
    const user = userEvent.setup();
    render(<textarea aria-label="Draft" defaultValue="Keep my draft" />);
    const editor = screen.getByRole('textbox', { name: 'Draft' });
    editor.focus();
    const props = makeProps();
    const { unmount } = render(<DictationDialog {...props} />);
    const cancel = screen.getByRole('button', { name: 'Cancel dictation' });
    const done = screen.getByRole('button', { name: 'Done dictating' });
    await waitFor(() => expect(screen.getByRole('dialog')).toHaveFocus());
    await user.keyboard('{Enter}');
    expect(props.cancel).not.toHaveBeenCalled();
    await user.tab({ shift: true });
    expect(done).toHaveFocus();
    await user.tab();
    expect(cancel).toHaveFocus();
    await user.tab();
    expect(done).toHaveFocus();
    await user.keyboard('{Escape}');
    expect(props.cancel).toHaveBeenCalledOnce();
    expect(props.stop).not.toHaveBeenCalled();
    unmount();
    await waitFor(() => expect(editor).toHaveFocus());
    expect(editor).toHaveValue('Keep my draft');
    fireEvent.keyDown(editor, { key: 'Escape' });
    expect(props.cancel).toHaveBeenCalledOnce();
  });

  it('prefers returning to the composer over the control that opened dictation', async () => {
    render(
      <>
        <button type="button">Launch dictation</button>
        <textarea data-testid="composer-textarea" />
      </>
    );
    const launcher = screen.getByRole('button', { name: 'Launch dictation' });
    launcher.focus();
    const { unmount } = render(<DictationDialog {...makeProps()} />);
    await waitFor(() => expect(screen.getByRole('dialog')).toHaveFocus());
    unmount();
    await waitFor(() => expect(screen.getByTestId('composer-textarea')).toHaveFocus());
    expect(launcher).not.toHaveFocus();
  });

  it('keeps focus on Cancel when Done is disabled', async () => {
    const user = userEvent.setup();
    render(<DictationDialog {...makeProps({ phase: 'finishing' })} />);
    const cancel = screen.getByRole('button', { name: 'Cancel dictation' });
    await waitFor(() => expect(screen.getByRole('dialog')).toHaveFocus());
    await user.tab();
    expect(cancel).toHaveFocus();
    await user.tab({ shift: true });
    expect(cancel).toHaveFocus();
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

const composerProps = (): ComponentProps<typeof ChatComposer> => ({
  addImageDisabled: false,
  attachments: [],
  canStop: false,
  contextDisabled: false,
  contextIsStatic: true,
  contextText: 'test model',
  dragActive: false,
  draft: 'Keep my draft',
  placeholder: 'Ask kodelet…',
  showStop: false,
  slashCommandIndex: -1,
  slashCommandSuggestions: [],
  slashCommandSuggestionsOpen: false,
  slashUsageHint: '',
  stopActionLabel: 'Stop',
  streamError: null,
  submitActionLabel: 'Send',
  submitDisabled: false,
  textareaDisabled: false,
  onAttachImages: vi.fn(),
  onContextOpen: vi.fn(),
  onDragLeave: vi.fn(),
  onDragOver: vi.fn(),
  onDrop: vi.fn(),
  onDraftChange: vi.fn(),
  onDraftKeyDown: vi.fn(),
  onPaste: vi.fn(),
  onRemoveAttachment: vi.fn(),
  onSelectSlashCommand: vi.fn(),
  onStop: vi.fn(),
  onSubmit: vi.fn(),
});

describe('ChatComposer dictation integration', () => {
  const makeDictation = (
    overrides: Partial<NonNullable<ComponentProps<typeof ChatComposer>['dictation']>> = {}
  ): NonNullable<ComponentProps<typeof ChatComposer>['dictation']> => ({
    ...makeProps({ phase: 'idle' }),
    busy: false,
    enabled: true,
    reason: null,
    isBusy: () => false,
    editorRef: { current: null },
    start: vi.fn(),
    handleCommand: vi.fn(),
    ...overrides,
  });

  it('opens dictation from the microphone without sending or editing the draft', async () => {
    const user = userEvent.setup();
    const props = composerProps();
    const dictation = makeDictation();
    render(<ChatComposer {...props} dictation={dictation} />);
    const microphone = screen.getByRole('button', { name: 'Dictate text' });
    expect(microphone).toHaveAttribute('aria-haspopup', 'dialog');
    await user.click(microphone);
    expect(dictation.start).toHaveBeenCalledOnce();
    expect(dictation.stop).not.toHaveBeenCalled();
    expect(props.onSubmit).not.toHaveBeenCalled();
    expect(props.onDraftChange).not.toHaveBeenCalled();
    expect(screen.queryByRole('dialog')).not.toBeInTheDocument();
  });

  it('replaces inline capture status with a modal and keeps busy errors inside it', () => {
    const props = composerProps();
    render(
      <ChatComposer
        {...props}
        dictation={makeDictation({
          phase: 'review',
          busy: true,
          error: 'Recording limit reached.',
        })}
      />
    );
    const dialog = screen.getByRole('dialog', { name: 'Dictation' });
    expect(dialog).toContainElement(screen.getByRole('alert'));
    expect(screen.getByTestId('composer-textarea')).toHaveAttribute('readonly');
    expect(screen.getByTestId('composer-textarea')).toHaveValue('Keep my draft');
    expect(screen.getByRole('button', { name: 'Dictate text' })).toBeDisabled();
    expect(screen.queryByRole('button', { name: 'Stop dictation' })).not.toBeInTheDocument();
    expect(props.onSubmit).not.toHaveBeenCalled();
  });

  it('leaves setup errors inline when no dictation session is active', () => {
    render(
      <ChatComposer
        {...composerProps()}
        dictation={makeDictation({ error: 'Microphone unavailable.' })}
      />
    );
    expect(screen.queryByRole('dialog')).not.toBeInTheDocument();
    expect(screen.getByRole('alert')).toHaveTextContent('Microphone unavailable.');
    expect(screen.getByTestId('composer-textarea')).not.toHaveAttribute('readonly');
  });
});
