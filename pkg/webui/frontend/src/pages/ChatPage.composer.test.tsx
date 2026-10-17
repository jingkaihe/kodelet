import {
  act,
  configure,
  fireEvent,
  getConfig,
  render,
  screen,
  waitFor,
  within,
} from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { describe, expect, it, vi } from 'vitest';
import type { ChatStreamEvent } from '../types';
import {
  ChatPage,
  flushAsyncUpdates,
  mockGetChatSettings,
  mockGetConversation,
  mockGetConversations,
  mockGetSlashCommands,
  mockGetTranscriptionCapabilities,
  mockNavigate,
  mockSteerConversation,
  mockStreamChat,
  mockStreamConversation,
  mockTranscribeAudio,
  renderChatWithRunner,
  setRouteParams,
  setupChatPageTests,
  stubImageFileReader,
} from './ChatPage.testSupport';

const { createDictationRecorder } = vi.hoisted(() => ({ createDictationRecorder: vi.fn() }));
vi.mock('../features/chat/dictationAudio', () => ({ createDictationRecorder }));

function prepareDictation(available = true) {
  mockGetTranscriptionCapabilities.mockResolvedValue({
    enabled: true,
    available,
    reason: available ? undefined : 'Sign in to Codex first.',
    maxAudioBytes: 20 * 1024 * 1024,
    maxDurationSeconds: 120,
    mimeTypes: ['audio/wav'],
  });
  const stopTrack = vi.fn();
  const getUserMedia = vi.fn().mockResolvedValue({ getTracks: () => [{ stop: stopTrack }] });
  vi.stubGlobal('isSecureContext', true);
  vi.stubGlobal('AudioContext', class {});
  vi.stubGlobal('AudioWorkletNode', class {});
  vi.stubGlobal(
    'navigator',
    Object.create(navigator, { mediaDevices: { value: { getUserMedia } } })
  );
  let snapshot: (file: File) => void;
  const finish = vi.fn().mockImplementation(async () => stopTrack());
  const cancel = vi.fn().mockImplementation(() => stopTrack());
  createDictationRecorder.mockImplementation((options: { onSnapshot: (file: File) => void }) => {
    snapshot = options.onSnapshot;
    return { start: vi.fn().mockResolvedValue(undefined), finish, cancel };
  });
  return {
    stopTrack,
    finish,
    cancel,
    emitSnapshot: () =>
      snapshot(new File(['RIFF1234WAVEdata'], 'recording.wav', { type: 'audio/wav' })),
  };
}

describe('ChatPage composer, attachments, and slash commands', () => {
  setupChatPageTests();

  it.each([
    false,
    true,
  ])('intercepts unavailable /voice locally with an active run=%s', async (running) => {
    prepareDictation(false);
    mockGetSlashCommands.mockResolvedValue({
      commands: [{ name: 'voice', description: 'Workspace voice recipe' }],
    });
    mockStreamChat.mockImplementation(() => new Promise(() => undefined));
    await renderChatWithRunner();
    const textarea = screen.getByTestId('composer-textarea');
    if (running) {
      fireEvent.change(textarea, { target: { value: 'Start working' } });
      fireEvent.click(screen.getByRole('button', { name: 'Send' }));
      await waitFor(() => expect(mockStreamChat).toHaveBeenCalledTimes(1));
    }
    fireEvent.change(textarea, { target: { value: '/voice' } });
    expect(screen.getByTestId('slash-command-suggestions')).toHaveTextContent('/voice');
    expect(screen.getByTestId('slash-command-suggestions')).toHaveTextContent(
      'Dictate text into the composer'
    );
    expect(screen.queryByText('Workspace voice recipe')).not.toBeInTheDocument();
    expect(
      fireEvent.keyDown(textarea, { key: 'V', code: 'KeyV', altKey: true, shiftKey: true })
    ).toBe(false);
    fireEvent.click(screen.getByRole('button', { name: running ? 'Steer' : 'Send' }));
    expect(await screen.findByRole('alert')).toHaveTextContent('Sign in to Codex first.');
    expect(createDictationRecorder).not.toHaveBeenCalled();
    expect(mockSteerConversation).not.toHaveBeenCalled();
    expect(mockStreamChat).toHaveBeenCalledTimes(running ? 1 : 0);
    expect(
      screen.queryByText('Command queued until the current run finishes successfully')
    ).not.toBeInTheDocument();
  });

  it.each([
    false,
    true,
  ])('only allows local /voice without a runner when enabled=%s', async (enabled) => {
    if (enabled) prepareDictation(false);
    render(<ChatPage />);
    await flushAsyncUpdates();
    const textarea = screen.getByTestId('composer-textarea');
    expect(textarea).not.toBeDisabled();
    fireEvent.change(textarea, { target: { value: '/voice' } });
    const send = screen.getByRole('button', { name: 'Send' });
    if (enabled) {
      expect(send).toBeEnabled();
      fireEvent.click(send);
      expect(await screen.findByRole('alert')).toHaveTextContent('Sign in to Codex first.');
    } else {
      expect(send).toBeDisabled();
      expect(screen.queryByTestId('slash-command-suggestions')).not.toBeInTheDocument();
    }
    expect(mockStreamChat).not.toHaveBeenCalled();
  });

  it('leaves a discovered /voice queueable and ignores its shortcut when dictation is disabled', async () => {
    mockGetSlashCommands.mockResolvedValue({
      commands: [{ name: 'voice', description: 'Workspace voice recipe', hint: 'topic' }],
    });
    mockStreamChat.mockImplementation(() => new Promise(() => undefined));
    await renderChatWithRunner();
    const textarea = screen.getByTestId('composer-textarea');
    expect(screen.queryByRole('button', { name: 'Dictate text' })).not.toBeInTheDocument();
    expect(
      fireEvent.keyDown(textarea, { key: 'V', code: 'KeyV', altKey: true, shiftKey: true })
    ).toBe(true);
    expect(screen.queryByRole('alert')).not.toBeInTheDocument();
    expect(createDictationRecorder).not.toHaveBeenCalled();
    fireEvent.change(textarea, { target: { value: 'Start working' } });
    fireEvent.click(screen.getByRole('button', { name: 'Send' }));
    await waitFor(() => expect(mockStreamChat).toHaveBeenCalledTimes(1));
    fireEvent.change(textarea, { target: { value: '/voice' } });
    expect(screen.getByTestId('slash-command-suggestions')).toHaveTextContent(
      'Workspace voice recipe'
    );
    fireEvent.change(textarea, { target: { value: '/voice topic' } });
    fireEvent.click(screen.getByRole('button', { name: 'Queue command' }));
    expect(await screen.findByRole('region', { name: 'Queued commands' })).toHaveTextContent(
      '/voice topic'
    );
    expect(mockSteerConversation).not.toHaveBeenCalled();
    expect(mockStreamChat).toHaveBeenCalledTimes(1);
    expect(textarea).toHaveValue('');
  });

  it.each([
    { trigger: 'icon', running: false },
    { trigger: 'command', running: true },
    { trigger: 'shortcut', running: true },
  ])('dictates through $trigger with running=$running and returns to editing without sending', async ({
    trigger,
    running,
  }) => {
    const user = userEvent.setup();
    const recorder = prepareDictation();
    mockTranscribeAudio
      .mockResolvedValueOnce({ text: 'broken preview' })
      .mockResolvedValueOnce({ text: 'spoken words' });
    stubImageFileReader('data:image/png;base64,aGVsbG8=');
    mockStreamChat.mockImplementation(() => new Promise(() => undefined));
    await renderChatWithRunner();
    const textarea = screen.getByTestId<HTMLTextAreaElement>('composer-textarea');
    if (running) {
      fireEvent.change(textarea, { target: { value: 'Start working' } });
      fireEvent.click(screen.getByRole('button', { name: 'Send' }));
      await waitFor(() => expect(mockStreamChat).toHaveBeenCalledTimes(1));
    }
    fireEvent.change(screen.getByTestId('composer-image-input'), {
      target: { files: [new File(['hello'], 'keep.png', { type: 'image/png' })] },
    });
    await screen.findByAltText('keep.png');
    const microphone = screen.getByRole('button', { name: 'Dictate text' });
    expect(microphone).toHaveAttribute('aria-haspopup', 'dialog');
    expect(microphone).toHaveAttribute('aria-keyshortcuts', 'Alt+Shift+V');
    if (trigger !== 'command') {
      fireEvent.change(textarea, { target: { value: 'before after' } });
      textarea.setSelectionRange(6, 6);
      if (trigger === 'shortcut') {
        textarea.focus();
        expect(
          fireEvent.keyDown(textarea, {
            key: '◊',
            code: 'KeyV',
            altKey: true,
            shiftKey: true,
          })
        ).toBe(false);
      } else {
        microphone.focus();
        fireEvent.click(microphone);
      }
    } else {
      fireEvent.change(textarea, { target: { value: '/voice' } });
      fireEvent.click(screen.getByRole('button', { name: running ? 'Steer' : 'Send' }));
    }
    const dialog = await screen.findByRole('dialog', { name: 'Dictation' });
    await waitFor(() => expect(within(dialog).getByRole('button', { name: /Done/ })).toBeEnabled());
    expect(screen.getByRole('button', { name: running ? 'Steer' : 'Send' })).toBeDisabled();
    expect(textarea).toHaveAttribute('readonly');
    await act(async () => recorder.emitSnapshot());
    const transcript = within(dialog).getByRole('log', { name: 'Dictation transcript' });
    expect(transcript).toHaveTextContent('broken preview');
    await act(async () => recorder.emitSnapshot());
    expect(transcript.textContent).toBe('spoken words');
    expect(dialog).not.toHaveTextContent(/part \d|\d parts? pending/i);
    expect(textarea).toHaveValue(trigger === 'command' ? '/voice' : 'before after');
    if (trigger === 'icon') await user.click(within(dialog).getByRole('button', { name: /Done/ }));
    else await user.keyboard('{Enter}');
    await waitFor(() => expect(textarea).toHaveFocus());
    const prefix = trigger === 'command' ? 'spoken words' : 'before spoken words';
    const suffix = trigger === 'command' ? '' : ' after';
    expect(textarea).toHaveValue(prefix + suffix);
    expect(textarea.selectionStart).toBe(prefix.length);
    expect(screen.queryByRole('dialog', { name: 'Dictation' })).not.toBeInTheDocument();
    expect(screen.getByAltText('keep.png')).toBeInTheDocument();
    expect(textarea).not.toHaveAttribute('readonly');
    // keyboard(), unlike type(), does not refocus the editor for us.
    await user.keyboard(' continued');
    expect(textarea).toHaveValue(`${prefix} continued${suffix}`);
    expect(mockStreamChat).toHaveBeenCalledTimes(running ? 1 : 0);
    expect(mockSteerConversation).not.toHaveBeenCalled();
  });

  it('does not capture dictation shortcuts while typing, composing, in another dialog or in the terminal', async () => {
    prepareDictation();
    await renderChatWithRunner();
    createDictationRecorder.mockClear();
    const textarea = screen.getByTestId('composer-textarea');
    const shortcut = { key: 'V', code: 'KeyV', altKey: true, shiftKey: true };
    for (const overrides of [
      { altKey: false },
      { shiftKey: false },
      { ctrlKey: true },
      { metaKey: true },
      { isComposing: true },
    ]) {
      expect(fireEvent.keyDown(textarea, { ...shortcut, ...overrides })).toBe(true);
    }
    const handled = new KeyboardEvent('keydown', {
      ...shortcut,
      bubbles: true,
      cancelable: true,
    });
    handled.preventDefault();
    fireEvent(textarea, handled);
    expect(fireEvent.keyDown(textarea, { ...shortcut, repeat: true })).toBe(false);

    fireEvent.keyDown(textarea, { key: '/', ctrlKey: true });
    const shortcuts = screen.getByRole('dialog', { name: 'Keyboard shortcuts' });
    expect(within(shortcuts).getByText('Alt/⌥+Shift+V')).toBeInTheDocument();
    expect(fireEvent.keyDown(shortcuts, shortcut)).toBe(true);
    fireEvent.keyDown(shortcuts, { key: 'Escape' });

    fireEvent.keyDown(textarea, { key: '`', ctrlKey: true });
    const terminal = await screen.findByTestId('terminal-host');
    expect(fireEvent.keyDown(terminal, shortcut)).toBe(true);
    expect(createDictationRecorder).not.toHaveBeenCalled();
    expect(screen.queryByRole('dialog', { name: 'Dictation' })).not.toBeInTheDocument();
  });

  it('returns to the unchanged draft when Done is clicked without any speech', async () => {
    const user = userEvent.setup();
    prepareDictation();
    await renderChatWithRunner();
    const textarea = screen.getByTestId<HTMLTextAreaElement>('composer-textarea');
    fireEvent.change(textarea, { target: { value: 'Existing draft' } });
    textarea.setSelectionRange(8, 8);
    await user.click(screen.getByRole('button', { name: 'Dictate text' }));
    const dialog = await screen.findByRole('dialog', { name: 'Dictation' });
    await waitFor(() => expect(within(dialog).getByRole('button', { name: /Done/ })).toBeEnabled());
    await user.click(within(dialog).getByRole('button', { name: /Done/ }));
    await waitFor(() => expect(textarea).toHaveFocus());
    expect(textarea).toHaveValue('Existing draft');
    expect(textarea.selectionStart).toBe(8);
    await user.keyboard(' updated');
    expect(textarea).toHaveValue('Existing updated draft');
    expect(mockStreamChat).not.toHaveBeenCalled();
  });

  it.each([
    'Escape',
    'Cancel',
  ])('cancels with %s without changing the draft or sending', async (key) => {
    const user = userEvent.setup();
    const recorder = prepareDictation();
    mockTranscribeAudio.mockResolvedValue({ text: 'discard these words' });
    await renderChatWithRunner();
    const textarea = screen.getByTestId('composer-textarea');
    fireEvent.change(textarea, { target: { value: ' /voice ' } });
    fireEvent.click(screen.getByRole('button', { name: 'Send' }));
    const dialog = await screen.findByRole('dialog', { name: 'Dictation' });
    await act(async () => recorder.emitSnapshot());
    expect(within(dialog).getByRole('log')).toHaveTextContent('discard these words');
    mockNavigate.mockClear();
    fireEvent.keyDown(dialog, { key: 'O', ctrlKey: true, shiftKey: true });
    fireEvent.keyDown(dialog, { key: 'k', ctrlKey: true });
    expect(screen.getAllByRole('dialog')).toHaveLength(1);
    expect(mockNavigate).not.toHaveBeenCalled();
    if (key === 'Cancel') within(dialog).getByRole('button', { name: 'Cancel dictation' }).focus();
    await user.keyboard(key === 'Cancel' ? '{Enter}' : '{Escape}');
    expect(screen.queryByRole('dialog')).not.toBeInTheDocument();
    expect(recorder.cancel).toHaveBeenCalled();
    expect(recorder.finish).not.toHaveBeenCalled();
    expect(textarea).toHaveValue(' /voice ');
    expect(mockStreamChat).not.toHaveBeenCalled();
    expect(mockSteerConversation).not.toHaveBeenCalled();
  });

  it('keeps microphone and editor disabled while a steering request is pending', async () => {
    prepareDictation();
    mockStreamChat.mockImplementation(() => new Promise(() => undefined));
    mockSteerConversation.mockImplementation(() => new Promise(() => undefined));
    await renderChatWithRunner();
    const textarea = screen.getByTestId('composer-textarea');
    fireEvent.change(textarea, { target: { value: 'Start working' } });
    fireEvent.click(screen.getByRole('button', { name: 'Send' }));
    await waitFor(() => expect(mockStreamChat).toHaveBeenCalledTimes(1));
    fireEvent.change(textarea, { target: { value: 'Focus on tests' } });
    fireEvent.click(screen.getByRole('button', { name: 'Steer' }));
    await waitFor(() => expect(mockSteerConversation).toHaveBeenCalledTimes(1));
    expect(textarea).toBeDisabled();
    expect(screen.getByRole('button', { name: 'Dictate text' })).toBeDisabled();
    expect(
      fireEvent.keyDown(window, { key: 'V', code: 'KeyV', altKey: true, shiftKey: true })
    ).toBe(true);
    expect(screen.queryByRole('dialog', { name: 'Dictation' })).not.toBeInTheDocument();
    expect(mockTranscribeAudio).not.toHaveBeenCalled();
  });

  it('includes pasted image attachments in the streamed chat request', async () => {
    mockStreamChat.mockResolvedValue(undefined);
    stubImageFileReader('data:image/png;base64,aGVsbG8=');

    await renderChatWithRunner();

    await waitFor(() => expect(mockGetConversations).toHaveBeenCalled());
    await waitFor(() => expect(mockGetChatSettings).toHaveBeenCalled());

    const textarea = screen.getByPlaceholderText('Ask kodelet...');
    fireEvent.change(textarea, { target: { value: 'describe this image' } });

    const file = new File(['hello'], 'clipboard.png', { type: 'image/png' });
    fireEvent.paste(textarea, {
      clipboardData: {
        items: [
          {
            kind: 'file',
            type: 'image/png',
            getAsFile: () => file,
          },
        ],
      },
      preventDefault: vi.fn(),
    });

    await waitFor(() => expect(screen.getByAltText('clipboard.png')).toBeInTheDocument());

    fireEvent.click(screen.getByRole('button', { name: 'Send' }));

    await waitFor(() => expect(mockStreamChat).toHaveBeenCalled());
    expect(mockStreamChat).toHaveBeenCalledWith(
      expect.objectContaining({
        message: 'describe this image',
        profile: 'work',
        content: expect.arrayContaining([
          expect.objectContaining({
            type: 'text',
            text: 'describe this image',
          }),
          expect.objectContaining({
            type: 'image',
            source: expect.objectContaining({
              data: 'aGVsbG8=',
              media_type: 'image/png',
            }),
          }),
        ]),
      }),
      expect.any(Object)
    );
  });

  it.each([false, true])('preserves typed punctuation with streaming=%s', async (streaming) => {
    const text = '... ............ >= -> => != <= :: ';
    const user = userEvent.setup();
    let streamListener: ((event: ChatStreamEvent) => void) | null = null;
    mockStreamChat.mockImplementation(async (_request, options) => {
      streamListener = options.onEvent;
      return new Promise(() => undefined);
    });
    await renderChatWithRunner();
    const textarea = screen.getByTestId<HTMLTextAreaElement>('composer-textarea');

    if (streaming) {
      fireEvent.change(textarea, { target: { value: 'Start working' } });
      fireEvent.click(screen.getByRole('button', { name: 'Send' }));
      await waitFor(() => expect(streamListener).not.toBeNull());
    }

    // Let React schedule input updates normally instead of forcing an act flush per event.
    const { eventWrapper } = getConfig();
    configure({ eventWrapper: (callback) => callback() });
    let streamUpdates = 0;
    const streamTimer = streaming
      ? window.setInterval(() => {
          streamUpdates += 1;
          streamListener?.({ kind: 'text-delta', delta: 'Working ' });
        }, 1)
      : undefined;
    try {
      await user.type(textarea, text);
    } finally {
      window.clearInterval(streamTimer);
      configure({ eventWrapper });
    }

    expect(textarea).toHaveValue(text);
    expect(textarea).toHaveFocus();
    expect(textarea.selectionStart).toBe(text.length);
    expect(textarea.selectionEnd).toBe(text.length);
    if (streaming) {
      expect(streamUpdates).toBeGreaterThan(0);
      expect(screen.getByTestId('chat-transcript-scroll')).toHaveTextContent('Working');
    }

    await user.click(screen.getByRole('button', { name: streaming ? 'Steer' : 'Send' }));
    if (streaming) {
      expect(mockSteerConversation).toHaveBeenCalledWith(expect.any(String), text.trim(), [
        { type: 'text', text: text.trim() },
      ]);
    } else {
      expect(mockStreamChat).toHaveBeenCalledWith(
        expect.objectContaining({ message: text.trim() }),
        expect.any(Object)
      );
    }
  });

  it('submits with Shift+Enter and keeps plain Enter for multiline editing', async () => {
    mockStreamChat.mockResolvedValue(undefined);

    await renderChatWithRunner();

    await waitFor(() => expect(mockGetConversations).toHaveBeenCalled());
    await waitFor(() => expect(mockGetChatSettings).toHaveBeenCalled());

    const textarea = screen.getByTestId('composer-textarea');
    fireEvent.change(textarea, { target: { value: 'hello from shortcut' } });

    fireEvent.keyDown(textarea, {
      key: 'Enter',
      shiftKey: false,
    });

    expect(mockStreamChat).not.toHaveBeenCalled();

    fireEvent.keyDown(textarea, {
      key: 'Enter',
      shiftKey: true,
    });

    await waitFor(() => expect(mockStreamChat).toHaveBeenCalled());
    expect(mockStreamChat).toHaveBeenCalledWith(
      expect.objectContaining({ message: 'hello from shortcut' }),
      expect.any(Object)
    );
  });

  it('suggests and inserts slash commands in the composer', async () => {
    await renderChatWithRunner();

    await waitFor(() => expect(mockGetSlashCommands).toHaveBeenCalled());

    const textarea = screen.getByTestId('composer-textarea');
    fireEvent.change(textarea, { target: { value: '/' } });

    expect(await screen.findByTestId('slash-command-suggestions')).toBeInTheDocument();
    expect(screen.getByText('/review')).toBeInTheDocument();
    expect(screen.getByText('/review').closest('button')).not.toHaveClass('is-active');

    fireEvent.keyDown(textarea, { key: 'ArrowDown' });
    fireEvent.keyDown(textarea, { key: 'Enter' });

    expect(textarea).toHaveValue('/goal ');
  });

  it('does not cap unfiltered slash command suggestions', async () => {
    mockGetSlashCommands.mockResolvedValue({
      commands: [
        {
          name: 'goal',
          description: 'Set active goal',
        },
        ...Array.from({ length: 12 }, (_, index) => ({
          name: `workflow-${index}`,
          description: `Workflow ${index}`,
        })),
        {
          name: 'review',
          description: 'Review local git changes',
        },
      ],
    });

    await renderChatWithRunner();

    await waitFor(() => expect(mockGetSlashCommands).toHaveBeenCalled());

    const textarea = screen.getByTestId('composer-textarea');
    fireEvent.change(textarea, { target: { value: '/' } });

    expect(await screen.findByTestId('slash-command-suggestions')).toBeInTheDocument();
    expect(screen.getByText('/review')).toBeInTheDocument();
  });

  it.each([
    'submitted',
    'observed',
  ] as const)('replaces obsolete command suggestions after a %s reload', async (source) => {
    setRouteParams({ id: 'conv-123' });
    mockGetConversation.mockResolvedValue({
      id: 'conv-123',
      runnerId: 'runner-1',
      cwd: '/runner/kodelet',
      messages: [],
      toolResults: {},
    });
    mockGetSlashCommands.mockResolvedValue({
      commands: [{ name: 'obsolete', description: 'Old extension command' }],
    });
    let observedListener: ((event: ChatStreamEvent) => void) | undefined;
    let submittedListener: ((event: ChatStreamEvent) => void) | undefined;
    let finishSubmission: (() => void) | undefined;
    mockStreamConversation.mockImplementation((_id, options) => {
      observedListener = options.onEvent;
      return new Promise(() => undefined);
    });
    mockStreamChat.mockImplementation((_request, options) => {
      submittedListener = options.onEvent;
      return new Promise<void>((resolve) => {
        finishSubmission = resolve;
      });
    });

    render(<ChatPage />);
    const textarea = await screen.findByTestId('composer-textarea');
    fireEvent.change(textarea, { target: { value: '/' } });
    expect(await screen.findByText('/obsolete')).toBeInTheDocument();
    await waitFor(() => expect(observedListener).toBeDefined());
    await flushAsyncUpdates();
    const discoveryCount = mockGetSlashCommands.mock.calls.length;

    if (source === 'submitted') {
      fireEvent.change(textarea, { target: { value: '/reload' } });
      fireEvent.click(screen.getByRole('button', { name: 'Send' }));
      await waitFor(() => expect(submittedListener).toBeDefined());
      fireEvent.change(textarea, { target: { value: '/' } });
    } else {
      // Ordinary turns must not invalidate resource discovery.
      await act(async () => {
        observedListener?.({ kind: 'conversation', conversation_id: 'conv-123' });
        observedListener?.({ kind: 'done', conversation_id: 'conv-123' });
      });
      expect(mockGetSlashCommands).toHaveBeenCalledTimes(discoveryCount);
    }

    mockGetSlashCommands.mockResolvedValue({
      commands: [{ name: 'fresh', description: 'New extension command' }],
    });
    await act(async () => {
      const listener = source === 'submitted' ? submittedListener : observedListener;
      if (source === 'observed') {
        listener?.({ kind: 'conversation', conversation_id: 'conv-123' });
      }
      listener?.({ kind: 'resources-reloaded', conversation_id: 'conv-123' });
      listener?.({ kind: 'done', conversation_id: 'conv-123' });
      if (source === 'submitted') finishSubmission?.();
    });

    expect(await screen.findByText('/fresh')).toBeInTheDocument();
    expect(screen.queryByText('/obsolete')).not.toBeInTheDocument();
    expect(textarea).toHaveValue('/');
    expect(mockGetSlashCommands).toHaveBeenCalledTimes(discoveryCount + 1);
    expect(mockGetSlashCommands).toHaveBeenLastCalledWith(
      undefined,
      expect.objectContaining({ conversationId: 'conv-123', runnerId: 'runner-1' })
    );
  });

  it('uses the selected slash command placeholder for argument hints', async () => {
    await renderChatWithRunner();

    await waitFor(() => expect(mockGetSlashCommands).toHaveBeenCalled());

    const textarea = screen.getByTestId('composer-textarea');
    fireEvent.change(textarea, { target: { value: '/' } });
    await screen.findByTestId('slash-command-suggestions');

    expect(textarea).toHaveAttribute('placeholder', 'Ask kodelet...');

    fireEvent.keyDown(textarea, { key: 'ArrowDown' });

    expect(textarea).toHaveAttribute('placeholder', '/goal <objective>');
    expect(screen.getByTestId('composer-slash-usage-hint')).toHaveTextContent('/goal <objective>');

    fireEvent.keyDown(textarea, { key: 'ArrowDown' });

    expect(textarea).toHaveAttribute(
      'placeholder',
      '/review [focus="correctness, tests" target=HEAD] additional instructions'
    );
    expect(screen.getByTestId('composer-slash-usage-hint')).toHaveTextContent(
      '/review [focus="correctness, tests" target=HEAD] additional instructions'
    );
  });

  it('shows slash command usage while editing a typed command', async () => {
    await renderChatWithRunner();

    await waitFor(() => expect(mockGetSlashCommands).toHaveBeenCalled());

    const textarea = screen.getByTestId('composer-textarea');
    fireEvent.change(textarea, { target: { value: '/intro ' } });

    expect(await screen.findByTestId('composer-slash-usage-hint')).toHaveTextContent(
      '/intro [name=<value> occupation=<value>] additional instructions'
    );
  });

  it('switches the composer layout automatically for multiline drafts', async () => {
    await renderChatWithRunner();

    await waitFor(() => expect(mockGetConversations).toHaveBeenCalled());

    const textarea = screen.getByTestId('composer-textarea');
    expect(screen.queryByTestId('composer-expand-toggle')).not.toBeInTheDocument();
    expect(textarea.parentElement).not.toHaveClass('is-multiline');

    fireEvent.change(textarea, { target: { value: 'a\nb\nc' } });

    await waitFor(() => expect(textarea.parentElement).toHaveClass('is-multiline'));
  });

  it('includes image attachments when queueing steering', async () => {
    setRouteParams({ id: 'conv-123' });
    mockGetConversation.mockResolvedValue({
      runnerId: 'runner-1',
      id: 'conv-123',
      createdAt: '2023-01-01T00:00:00Z',
      updatedAt: '2023-01-02T00:00:00Z',
      messageCount: 1,
      messages: [{ role: 'user', content: 'hello' }],
      toolResults: {},
    });

    let streamOptions: { onEvent: (event: ChatStreamEvent) => void } | null = null;
    mockStreamChat.mockImplementation(async (_request, options) => {
      streamOptions = options as { onEvent: (event: ChatStreamEvent) => void };
      return new Promise(() => undefined);
    });

    stubImageFileReader('data:image/png;base64,aGVsbG8=');

    render(<ChatPage />);

    await waitFor(() => expect(mockGetConversation).toHaveBeenCalledWith('conv-123'));

    fireEvent.change(screen.getByPlaceholderText('Ask kodelet...'), {
      target: { value: 'continue' },
    });
    fireEvent.click(screen.getByRole('button', { name: 'Send' }));

    await waitFor(() => expect(mockStreamChat).toHaveBeenCalled());
    await act(async () => {
      streamOptions?.onEvent({
        kind: 'tool-use',
        tool_call_id: 'tool-1',
        tool_name: 'search',
        input: '{}',
      });
    });

    const textarea = screen.getByPlaceholderText('Ask kodelet...');
    fireEvent.change(textarea, { target: { value: 'Use this screenshot' } });

    const file = new File(['hello'], 'steer.png', { type: 'image/png' });
    fireEvent.paste(textarea, {
      clipboardData: {
        items: [
          {
            kind: 'file',
            type: 'image/png',
            getAsFile: () => file,
          },
        ],
      },
      preventDefault: vi.fn(),
    });

    await waitFor(() => expect(screen.getByAltText('steer.png')).toBeInTheDocument());

    fireEvent.click(screen.getByRole('button', { name: 'Steer' }));

    await waitFor(() =>
      expect(mockSteerConversation).toHaveBeenCalledWith(
        'conv-123',
        'Use this screenshot',
        expect.arrayContaining([
          expect.objectContaining({
            type: 'text',
            text: 'Use this screenshot',
          }),
          expect.objectContaining({
            type: 'image',
            source: expect.objectContaining({
              data: 'aGVsbG8=',
              media_type: 'image/png',
            }),
          }),
        ])
      )
    );

    expect(await screen.findByTestId('pending-steer-list')).toBeInTheDocument();
    expect(screen.getByText('Use this screenshot · with a screenshot')).toBeInTheDocument();
    expect(screen.queryByAltText('Uploaded content')).not.toBeInTheDocument();
  });

  it('shows only effort for legacy model settings and keeps workspace context above the transcript', async () => {
    await renderChatWithRunner();

    const header = screen.getByTestId('chat-workspace-header');
    const transcript = screen.getByTestId('chat-transcript-scroll');
    expect(header).toBe(screen.getByRole('region', { name: 'Workspace' }));
    expect(header).toHaveTextContent(/^\/runner\/kodelet$/);
    expect(within(header).getAllByRole('button')).toHaveLength(1);
    expect(transcript).not.toContainElement(header);
    expect(header.parentElement).toBe(transcript.parentElement);
    expect(
      header.compareDocumentPosition(transcript) & Node.DOCUMENT_POSITION_FOLLOWING
    ).toBeTruthy();
    const contextButton = screen.getByRole('button', { name: 'Model settings: medium' });
    expect(contextButton).toHaveTextContent(/^medium$/);
    expect(contextButton).not.toHaveTextContent('/runner/kodelet');
    expect(screen.queryByTestId('transcript-meta-strip')).not.toBeInTheDocument();
    expect(screen.queryByText('Shift+Enter to send')).not.toBeInTheDocument();
    expect(screen.getByRole('button', { name: 'Send' })).toHaveAttribute(
      'title',
      'Send (Shift+Enter)'
    );
    fireEvent.click(contextButton);
    const cwdInput = screen.getByLabelText('Working directory');
    await waitFor(() => {
      expect(cwdInput).toHaveValue('');
      expect(cwdInput).toHaveAttribute('placeholder', '/runner/kodelet');
      expect(cwdInput).toHaveFocus();
    });
  });
});
