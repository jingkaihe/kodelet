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
  mockGetChatSettings,
  mockGetConversation,
  mockGetConversations,
  mockGetSlashCommands,
  mockSteerConversation,
  mockStreamChat,
  renderChatWithRunner,
  setRouteParams,
  setupChatPageTests,
  stubImageFileReader,
} from './ChatPage.testSupport';

describe('ChatPage composer, attachments, and slash commands', () => {
  setupChatPageTests();

  it('includes pasted image attachments in the streamed chat request', async () => {
    mockStreamChat.mockResolvedValue(undefined);
    stubImageFileReader('data:image/png;base64,aGVsbG8=');

    await renderChatWithRunner();

    await waitFor(() => expect(mockGetConversations).toHaveBeenCalled());
    await waitFor(() => expect(mockGetChatSettings).toHaveBeenCalled());

    const textarea = screen.getByPlaceholderText('Ask kodelet anything...');
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

  it('uses the selected slash command placeholder for argument hints', async () => {
    await renderChatWithRunner();

    await waitFor(() => expect(mockGetSlashCommands).toHaveBeenCalled());

    const textarea = screen.getByTestId('composer-textarea');
    fireEvent.change(textarea, { target: { value: '/' } });
    await screen.findByTestId('slash-command-suggestions');

    expect(textarea).toHaveAttribute('placeholder', 'Ask kodelet anything...');

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

  it('keeps the composer layout stable when editing and clearing multiline drafts', async () => {
    await renderChatWithRunner();

    await waitFor(() => expect(mockGetConversations).toHaveBeenCalled());

    const textarea = screen.getByTestId('composer-textarea');
    expect(screen.queryByTestId('composer-expand-toggle')).not.toBeInTheDocument();
    const gridClassName = textarea.parentElement?.className;
    expect(textarea).toHaveAttribute('rows', '3');

    fireEvent.change(textarea, { target: { value: 'a\nb\nc' } });

    expect(textarea).toHaveValue('a\nb\nc');
    expect(textarea.parentElement).toHaveAttribute('class', gridClassName);
    expect(textarea).not.toHaveAttribute('style');

    fireEvent.change(textarea, { target: { value: '' } });

    expect(textarea).toHaveValue('');
    expect(textarea.parentElement).toHaveAttribute('class', gridClassName);
    expect(textarea).toHaveAttribute('rows', '3');
    expect(textarea).not.toHaveAttribute('style');
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

    fireEvent.change(screen.getByPlaceholderText('Ask kodelet anything...'), {
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

    const textarea = screen.getByPlaceholderText('Steer the active conversation…');
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
