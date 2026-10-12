import { act, fireEvent, render, screen, waitFor, within } from '@testing-library/react';
import { describe, expect, it, vi } from 'vitest';
import type { ChatStreamEvent } from '../types';
import {
  ChatPage,
  makeRunner,
  mockGetConversation,
  mockGetConversations,
  mockGetRunners,
  mockNavigate,
  mockSteerConversation,
  mockStopConversation,
  mockStreamChat,
  mockStreamConversation,
  renderChatWithRunner,
  setRouteParams,
  setupChatPageTests,
  stubImageFileReader,
} from './ChatPage.testSupport';

interface ControlledChatStream {
  onEvent: (event: ChatStreamEvent) => void;
  signal: AbortSignal;
  resolve: () => void;
  reject: (error: Error) => void;
}

const setupCommandQueueStreams = () => {
  setRouteParams({ id: 'conv-123' });
  mockGetConversation.mockImplementation(async (id: string) => ({
    id,
    runnerId: 'runner-1',
    cwd: '/runner/kodelet',
    createdAt: '2024-01-01T00:00:00Z',
    updatedAt: '2024-01-01T00:00:00Z',
    messageCount: 1,
    messages: [
      { role: 'user', content: id === 'conv-123' ? 'First conversation' : 'Other conversation' },
    ],
    toolResults: {},
  }));
  const streams: ControlledChatStream[] = [];
  mockStreamChat.mockImplementation(
    async (_request, options) =>
      new Promise<void>((resolve, reject) => {
        streams.push({ ...options, resolve, reject });
      })
  );
  // Keep observers open unless a test explicitly completes them. Closing an
  // observer must not masquerade as a successfully completed submitted run.
  mockStreamConversation.mockImplementation(async () => new Promise(() => undefined));
  return streams;
};

const queueSlashCommand = (message: string) => {
  fireEvent.change(screen.getByTestId('composer-textarea'), { target: { value: message } });
  fireEvent.click(screen.getByRole('button', { name: 'Queue command' }));
};

describe('ChatPage streaming, steering, and navigation', () => {
  setupChatPageTests();

  it('streams a future TUI turn into an already-open conversation', async () => {
    setRouteParams({ id: 'conv-123' });
    mockGetConversation
      .mockResolvedValueOnce({
        id: 'conv-123',
        createdAt: '2024-01-01T00:00:00Z',
        updatedAt: '2024-01-01T00:00:00Z',
        messageCount: 1,
        messages: [],
        toolResults: {},
      })
      .mockResolvedValue({
        id: 'conv-123',
        createdAt: '2024-01-01T00:00:00Z',
        updatedAt: '2024-01-01T00:01:00Z',
        messageCount: 2,
        messages: [
          { role: 'user', content: 'sent from the tui' },
          { role: 'assistant', content: 'hello from the runner' },
        ],
        toolResults: {},
      });
    let streamListener: ((event: ChatStreamEvent) => void) | null = null;
    mockStreamConversation.mockImplementation(async (_id, options) => {
      streamListener = (options as { onEvent: (event: ChatStreamEvent) => void }).onEvent;
      return new Promise(() => undefined);
    });

    render(<ChatPage />);

    await waitFor(() => expect(mockGetConversation).toHaveBeenCalledWith('conv-123'));
    await waitFor(() =>
      expect(mockStreamConversation).toHaveBeenCalledWith('conv-123', expect.any(Object))
    );

    await act(async () => {
      streamListener?.({ kind: 'conversation', conversation_id: 'conv-123' });
      streamListener?.({
        kind: 'user-message',
        conversation_id: 'conv-123',
        content: 'sent from the tui',
      });
      streamListener?.({
        kind: 'text-delta',
        conversation_id: 'conv-123',
        delta: 'hello from the runner',
      });
      streamListener?.({ kind: 'done', conversation_id: 'conv-123' });
    });

    await waitFor(() => expect(screen.getByText('sent from the tui')).toBeInTheDocument());
    expect(screen.getByText('hello from the runner')).toBeInTheDocument();
  });

  it('allows steering a TUI-started conversation while its remote runner is busy', async () => {
    setRouteParams({ id: 'conv-123' });
    const busyRunner = makeRunner({
      displayName: 'kodelet',
      status: 'busy',
      activeRunId: 'run-1',
      activeRunIds: ['run-1'],
    });
    mockGetConversation.mockResolvedValue({
      id: 'conv-123',
      createdAt: '2026-08-11T00:00:00Z',
      updatedAt: '2026-08-11T00:00:00Z',
      messageCount: 1,
      cwd: '/runner/kodelet',
      runnerId: busyRunner.id,
      runner: busyRunner,
      messages: [{ role: 'user', content: 'existing turn' }],
      toolResults: {},
    });
    mockGetRunners.mockResolvedValue({ runners: [busyRunner] });
    let streamListener: ((event: ChatStreamEvent) => void) | null = null;
    mockStreamConversation.mockImplementation(async (_id, options) => {
      streamListener = (options as { onEvent: (event: ChatStreamEvent) => void }).onEvent;
      return new Promise(() => undefined);
    });

    render(<ChatPage />);

    fireEvent.click(await screen.findByTestId('transcript-meta-strip'));
    await waitFor(() =>
      expect(
        within(screen.getByTestId('transcript-meta-details')).getByText('Status').nextElementSibling
      ).toHaveTextContent('1 active')
    );
    await waitFor(() => expect(streamListener).not.toBeNull());

    await act(async () => {
      streamListener?.({ kind: 'conversation', conversation_id: 'conv-123' });
    });

    fireEvent.change(screen.getByPlaceholderText('Ask kodelet...'), {
      target: { value: 'Focus on the failing tests' },
    });
    const steerButton = screen.getByRole('button', { name: 'Steer' });
    expect(steerButton).toBeEnabled();
    fireEvent.click(steerButton);

    await waitFor(() =>
      expect(mockSteerConversation).toHaveBeenCalledWith(
        'conv-123',
        'Focus on the failing tests',
        expect.arrayContaining([
          expect.objectContaining({ type: 'text', text: 'Focus on the failing tests' }),
        ])
      )
    );
  });

  it('queues steering while a conversation is streaming', async () => {
    setRouteParams({ id: 'conv-123' });
    mockGetConversation.mockResolvedValue({
      runnerId: 'runner-1',
      id: 'conv-123',
      createdAt: '2023-01-01T00:00:00Z',
      updatedAt: '2023-01-02T00:00:00Z',
      messageCount: 1,
      profile: 'anthropic',
      profileLocked: true,
      messages: [
        {
          role: 'user',
          content: 'hello',
        },
      ],
      toolResults: {},
    });

    let streamOptions: { onEvent: (event: ChatStreamEvent) => void } | null = null;
    mockStreamChat.mockImplementation(async (_request, options) => {
      streamOptions = options as { onEvent: (event: ChatStreamEvent) => void };
      return new Promise(() => undefined);
    });

    render(<ChatPage />);

    await waitFor(() => expect(mockGetConversation).toHaveBeenCalledWith('conv-123'));

    fireEvent.change(screen.getByPlaceholderText('Ask kodelet...'), {
      target: { value: 'continue' },
    });
    fireEvent.click(screen.getByRole('button', { name: 'Send' }));

    await waitFor(() => expect(mockStreamChat).toHaveBeenCalled());
    expect(screen.getByRole('button', { name: 'Stop' })).toBeInTheDocument();
    expect(screen.getByRole('button', { name: 'Steer' })).toHaveAttribute(
      'title',
      'Steer (Shift+Enter)'
    );

    await act(async () => {
      streamOptions?.onEvent({
        kind: 'tool-use',
        tool_call_id: 'tool-1',
        tool_name: 'search',
        input: '{}',
      });
    });

    fireEvent.change(screen.getByPlaceholderText('Ask kodelet...'), {
      target: { value: 'Focus on tests' },
    });

    await waitFor(() => expect(screen.getByRole('button', { name: 'Steer' })).toBeEnabled());

    fireEvent.click(screen.getByRole('button', { name: 'Steer' }));

    await waitFor(() =>
      expect(mockSteerConversation).toHaveBeenCalledWith(
        'conv-123',
        'Focus on tests',
        expect.arrayContaining([
          expect.objectContaining({
            type: 'text',
            text: 'Focus on tests',
          }),
        ])
      )
    );

    const pendingGuidance = await screen.findByRole('region', { name: 'Queued message' });
    expect(within(pendingGuidance).getByText('Queued message')).toBeInTheDocument();
    const queuedMessages = within(pendingGuidance).getByRole('list');
    expect(within(queuedMessages).getAllByRole('listitem')).toHaveLength(1);
    expect(within(queuedMessages).getByText('Focus on tests')).toBeInTheDocument();
    expect(pendingGuidance).not.toHaveTextContent('You');

    await act(async () => {
      streamOptions?.onEvent({
        kind: 'user-message',
        conversation_id: 'conv-123',
        role: 'user',
        content: 'Focus on tests',
      });
      streamOptions?.onEvent({
        kind: 'conversation',
        conversation_id: 'conv-123',
      });
    });

    await waitFor(() => expect(screen.queryByTestId('pending-steer-list')).not.toBeInTheDocument());
  });

  it('queues slash commands FIFO separately from steering and waits for successful stream cleanup', async () => {
    const streams = setupCommandQueueStreams();
    stubImageFileReader('data:image/png;base64,aGVsbG8=');
    render(<ChatPage />);
    await screen.findByText('First conversation');

    const textarea = screen.getByTestId('composer-textarea');
    fireEvent.change(textarea, { target: { value: 'continue' } });
    fireEvent.click(screen.getByRole('button', { name: 'Send' }));
    await waitFor(() => expect(streams).toHaveLength(1));

    queueSlashCommand('/reload');
    const screenshot = new File(['hello'], 'review.png', { type: 'image/png' });
    fireEvent.paste(textarea, {
      clipboardData: {
        items: [{ kind: 'file', type: 'image/png', getAsFile: () => screenshot }],
      },
    });
    await screen.findByAltText('review.png');
    queueSlashCommand('/review focus=tests');
    const queued = screen.getByRole('region', { name: 'Queued commands' });
    expect(
      within(queued)
        .getAllByRole('listitem')
        .map((item) => item.textContent)
    ).toEqual(['/reload', '/review focus=tests · with a screenshot']);
    expect(screen.queryByAltText('review.png')).not.toBeInTheDocument();
    expect(textarea).toHaveValue('');
    expect(mockSteerConversation).not.toHaveBeenCalled();
    expect(mockStreamChat).toHaveBeenCalledTimes(1);

    fireEvent.change(textarea, { target: { value: 'Keep this draft' } });

    await act(async () => {
      streams[0].onEvent({ kind: 'done', conversation_id: 'conv-123' });
      streams[0].onEvent({ kind: 'done', conversation_id: 'conv-123' });
    });
    expect(mockStreamChat).toHaveBeenCalledTimes(1);

    await act(async () => streams[0].resolve());
    await waitFor(() => expect(mockStreamChat).toHaveBeenCalledTimes(2));
    expect(mockStreamChat).toHaveBeenLastCalledWith(
      expect.objectContaining({
        conversationId: 'conv-123',
        message: '/reload',
        content: [{ type: 'text', text: '/reload' }],
      }),
      expect.any(Object)
    );
    expect(
      within(screen.getByRole('region', { name: 'Queued commands' })).getAllByRole('listitem')
    ).toHaveLength(1);
    expect(textarea).toHaveValue('Keep this draft');

    await act(async () => {
      streams[0].onEvent({ kind: 'done', conversation_id: 'conv-123' });
      streams[1].onEvent({ kind: 'done', conversation_id: 'conv-123' });
    });
    expect(mockStreamChat).toHaveBeenCalledTimes(2);
    await act(async () => streams[1].resolve());
    await waitFor(() => expect(mockStreamChat).toHaveBeenCalledTimes(3));
    expect(mockStreamChat).toHaveBeenLastCalledWith(
      expect.objectContaining({
        conversationId: 'conv-123',
        message: '/review focus=tests',
        content: [
          { type: 'text', text: '/review focus=tests' },
          { type: 'image', source: { data: 'aGVsbG8=', media_type: 'image/png' } },
        ],
      }),
      expect.any(Object)
    );
    expect(screen.queryByRole('region', { name: 'Queued commands' })).not.toBeInTheDocument();

    await act(async () => {
      streams[2].onEvent({ kind: 'done', conversation_id: 'conv-123' });
      streams[2].resolve();
    });
    expect(mockStreamChat).toHaveBeenCalledTimes(3);
    expect(mockSteerConversation).not.toHaveBeenCalled();
    expect(textarea).toHaveValue('Keep this draft');
  });

  it.each([
    'error',
    'cancelled',
    'stop',
    'EOF without done',
  ])('discards queued commands with a notice after %s instead of executing them', async (outcome) => {
    const streams = setupCommandQueueStreams();
    render(<ChatPage />);
    await screen.findByText('First conversation');
    fireEvent.change(screen.getByTestId('composer-textarea'), { target: { value: 'continue' } });
    fireEvent.click(screen.getByRole('button', { name: 'Send' }));
    await waitFor(() => expect(streams).toHaveLength(1));
    queueSlashCommand('/reload');
    queueSlashCommand('/review tests');
    fireEvent.change(screen.getByTestId('composer-textarea'), {
      target: { value: 'Keep my draft' },
    });

    if (outcome === 'stop') {
      fireEvent.click(screen.getByRole('button', { name: 'Stop' }));
      expect(mockStopConversation).toHaveBeenCalledWith('conv-123');
      expect(streams[0].signal.aborted).toBe(true);
    }
    await act(async () => {
      if (outcome === 'error') {
        streams[0].onEvent({ kind: 'error', conversation_id: 'conv-123', error: 'Run failed' });
      } else if (outcome === 'cancelled') {
        streams[0].onEvent({ kind: 'done', conversation_id: 'conv-123', cancelled: true });
      }
      if (outcome === 'stop') {
        streams[0].reject(new DOMException('Stopped', 'AbortError'));
      } else {
        streams[0].resolve();
      }
    });

    await waitFor(() =>
      expect(screen.queryByRole('region', { name: 'Queued commands' })).not.toBeInTheDocument()
    );
    expect(
      screen.getByText(/queued commands.*discarded|discarded.*queued commands/i)
    ).toBeInTheDocument();
    expect(mockStreamChat).toHaveBeenCalledTimes(1);
    expect(mockSteerConversation).not.toHaveBeenCalled();
    expect(screen.getByTestId('composer-textarea')).toHaveValue('Keep my draft');
  });

  it('retains a queued run observer across navigation and unrelated run completion', async () => {
    const streams = setupCommandQueueStreams();
    const observers: Array<ControlledChatStream & { conversationId: string }> = [];
    mockStreamConversation.mockImplementation(
      async (conversationId, options) =>
        new Promise<void>((resolve, reject) => {
          observers.push({ ...options, conversationId, resolve, reject });
        })
    );
    const { rerender } = render(<ChatPage />);
    await screen.findByText('First conversation');
    await waitFor(() => expect(observers).toHaveLength(1));
    const foreground = observers[0];
    await act(async () =>
      foreground.onEvent({ kind: 'conversation', conversation_id: 'conv-123' })
    );
    queueSlashCommand('/reload');

    setRouteParams({ id: 'conv-456' });
    rerender(<ChatPage />);
    await screen.findByText('Other conversation');
    expect(foreground.signal.aborted).toBe(false);
    expect(observers.filter((observer) => observer.conversationId === 'conv-123')).toHaveLength(1);
    expect(screen.queryByRole('region', { name: 'Queued commands' })).not.toBeInTheDocument();
    const textarea = screen.getByTestId('composer-textarea');
    fireEvent.change(textarea, { target: { value: 'Unrelated request' } });
    fireEvent.click(screen.getByRole('button', { name: 'Send' }));
    await waitFor(() => expect(streams).toHaveLength(1));
    expect(mockStreamChat).toHaveBeenLastCalledWith(
      expect.objectContaining({ conversationId: 'conv-456', message: 'Unrelated request' }),
      expect.any(Object)
    );
    await act(async () => {
      streams[0].onEvent({ kind: 'done', conversation_id: 'conv-456' });
      streams[0].resolve();
    });
    await waitFor(() =>
      expect(screen.queryByRole('button', { name: 'Stop' })).not.toBeInTheDocument()
    );
    expect(foreground.signal.aborted).toBe(false);
    expect(observers.filter((observer) => observer.conversationId === 'conv-123')).toHaveLength(1);
    expect(mockStreamChat).toHaveBeenCalledTimes(1);
    fireEvent.change(textarea, { target: { value: 'Other draft' } });
    mockNavigate.mockClear();

    // Completion is not replayed by the server: the original subscription
    // must remain authoritative after it becomes a background conversation.
    await act(async () => {
      foreground.onEvent({ kind: 'done', conversation_id: 'conv-123' });
      foreground.onEvent({ kind: 'done', conversation_id: 'conv-123' });
    });
    await waitFor(() => expect(streams).toHaveLength(2));
    expect(mockStreamChat).toHaveBeenLastCalledWith(
      expect.objectContaining({ conversationId: 'conv-123', message: '/reload' }),
      expect.any(Object)
    );
    expect(mockSteerConversation).not.toHaveBeenCalled();
    expect(mockNavigate).not.toHaveBeenCalled();
    expect(screen.getByTestId('composer-textarea')).toHaveValue('Other draft');
    expect(screen.getByText('Other conversation')).toBeInTheDocument();
  });

  it('keeps the current observer active when the retained command observer disconnects', async () => {
    setupCommandQueueStreams();
    const observers: Array<ControlledChatStream & { conversationId: string }> = [];
    mockStreamConversation.mockImplementation(
      async (conversationId, options) =>
        new Promise<void>((resolve, reject) => {
          observers.push({ ...options, conversationId, resolve, reject });
        })
    );
    const { rerender } = render(<ChatPage />);
    await screen.findByText('First conversation');
    await waitFor(() => expect(observers).toHaveLength(1));
    const retained = observers[0];
    await act(async () => retained.onEvent({ kind: 'conversation', conversation_id: 'conv-123' }));
    queueSlashCommand('/reload');

    setRouteParams({ id: 'conv-456' });
    rerender(<ChatPage />);
    await screen.findByText('Other conversation');
    expect(retained.signal.aborted).toBe(false);
    setRouteParams({ id: 'conv-123' });
    rerender(<ChatPage />);
    await screen.findByText('First conversation');
    await waitFor(() =>
      expect(observers.filter((observer) => observer.conversationId === 'conv-123')).toHaveLength(2)
    );
    const current = observers.filter((observer) => observer.conversationId === 'conv-123')[1];
    await act(async () => current.onEvent({ kind: 'conversation', conversation_id: 'conv-123' }));
    expect(screen.getByRole('region', { name: 'Queued commands' })).toHaveTextContent('/reload');

    await act(async () => retained.reject(new TypeError('Failed to fetch')));
    await waitFor(() =>
      expect(screen.queryByRole('region', { name: 'Queued commands' })).not.toBeInTheDocument()
    );
    expect(
      screen.getByText(/queued commands.*discarded|discarded.*queued commands/i)
    ).toBeInTheDocument();
    expect(current.signal.aborted).toBe(false);
    expect(screen.getByRole('button', { name: 'Stop' })).toBeInTheDocument();
    fireEvent.change(screen.getByTestId('composer-textarea'), {
      target: { value: 'Continue with the failing tests' },
    });
    fireEvent.click(screen.getByRole('button', { name: 'Steer' }));
    await waitFor(() =>
      expect(mockSteerConversation).toHaveBeenCalledWith(
        'conv-123',
        'Continue with the failing tests',
        [{ type: 'text', text: 'Continue with the failing tests' }]
      )
    );
    expect(mockStreamChat).not.toHaveBeenCalled();
  });

  it.each([
    'cancelled',
    'EOF without done',
  ])('does not drain queued commands when an observed run ends with %s', async (outcome) => {
    setupCommandQueueStreams();
    const observers: ControlledChatStream[] = [];
    mockStreamConversation.mockImplementation(
      async (_id, options) =>
        new Promise<void>((resolve, reject) => {
          observers.push({ ...options, resolve, reject });
        })
    );
    render(<ChatPage />);
    await screen.findByText('First conversation');
    await waitFor(() => expect(observers).toHaveLength(1));
    await act(async () =>
      observers[0].onEvent({ kind: 'conversation', conversation_id: 'conv-123' })
    );
    queueSlashCommand('/reload');
    queueSlashCommand('/review tests');
    await act(async () => {
      if (outcome === 'cancelled') {
        observers[0].onEvent({ kind: 'done', conversation_id: 'conv-123', cancelled: true });
      }
      observers[0].resolve();
    });
    await waitFor(() =>
      expect(screen.queryByRole('region', { name: 'Queued commands' })).not.toBeInTheDocument()
    );
    expect(
      screen.getByText(/queued commands.*discarded|discarded.*queued commands/i)
    ).toBeInTheDocument();
    expect(mockStreamChat).not.toHaveBeenCalled();
    expect(mockSteerConversation).not.toHaveBeenCalled();
  });

  it('keeps commands scoped to the original conversation without disturbing another composer', async () => {
    const streams = setupCommandQueueStreams();
    stubImageFileReader('data:image/png;base64,aGVsbG8=');
    const { rerender } = render(<ChatPage />);
    await screen.findByText('First conversation');
    fireEvent.change(screen.getByTestId('composer-textarea'), { target: { value: 'continue' } });
    fireEvent.click(screen.getByRole('button', { name: 'Send' }));
    await waitFor(() => expect(streams).toHaveLength(1));
    queueSlashCommand('/reload');

    setRouteParams({ id: 'conv-456' });
    rerender(<ChatPage />);
    await screen.findByText('Other conversation');
    expect(screen.queryByRole('region', { name: 'Queued commands' })).not.toBeInTheDocument();
    const textarea = screen.getByTestId('composer-textarea');
    fireEvent.change(textarea, { target: { value: 'Other conversation draft' } });
    const file = new File(['hello'], 'keep.png', { type: 'image/png' });
    fireEvent.paste(textarea, {
      clipboardData: {
        items: [{ kind: 'file', type: 'image/png', getAsFile: () => file }],
      },
    });
    await screen.findByAltText('keep.png');
    mockNavigate.mockClear();
    await act(async () => {
      streams[0].onEvent({ kind: 'done', conversation_id: 'conv-123' });
      streams[0].resolve();
    });
    await waitFor(() => expect(streams).toHaveLength(2));
    expect(mockStreamChat).toHaveBeenLastCalledWith(
      expect.objectContaining({
        conversationId: 'conv-123',
        message: '/reload',
        content: [{ type: 'text', text: '/reload' }],
      }),
      expect.any(Object)
    );
    await act(async () => {
      streams[1].onEvent({ kind: 'conversation', conversation_id: 'conv-123' });
      streams[1].onEvent({ kind: 'text', conversation_id: 'conv-123', content: 'Reload complete' });
      streams[1].onEvent({ kind: 'done', conversation_id: 'conv-123' });
      streams[1].resolve();
    });
    expect(mockStreamChat).toHaveBeenCalledTimes(2);
    expect(mockNavigate).not.toHaveBeenCalled();
    expect(textarea).toHaveValue('Other conversation draft');
    expect(screen.getByAltText('keep.png')).toBeInTheDocument();
    expect(screen.queryByText('Reload complete')).not.toBeInTheDocument();
    expect(screen.getByText('Other conversation')).toBeInTheDocument();
  });

  it('allows sidebar navigation while a conversation is streaming', async () => {
    mockGetConversations.mockResolvedValue({
      conversations: [
        {
          id: 'conv-123',
          createdAt: '2024-01-01T00:00:00Z',
          updatedAt: '2024-01-01T00:00:00Z',
          messageCount: 1,
          summary: 'Active conversation',
        },
        {
          id: 'conv-456',
          createdAt: '2024-01-01T00:00:00Z',
          updatedAt: '2024-01-01T00:00:00Z',
          messageCount: 1,
          summary: 'Other conversation',
        },
      ],
      hasMore: false,
      total: 2,
      limit: 40,
      offset: 0,
    });

    mockStreamChat.mockImplementation(async (_request, options) => {
      options.onEvent({ kind: 'conversation', conversation_id: 'conv-123' });
      return new Promise(() => undefined);
    });

    await renderChatWithRunner();

    await waitFor(() => expect(mockGetConversations).toHaveBeenCalled());

    fireEvent.change(screen.getByPlaceholderText('Ask kodelet...'), {
      target: { value: 'hello' },
    });
    fireEvent.click(screen.getByRole('button', { name: 'Send' }));

    await waitFor(() => expect(mockStreamChat).toHaveBeenCalled());

    fireEvent.click(screen.getByTestId('sidebar-hide-button'));
    expect(screen.queryByTestId('chat-sidebar-shell')).not.toBeInTheDocument();

    fireEvent.click(screen.getByTestId('sidebar-attached-toggle'));
    fireEvent.click(screen.getByRole('button', { name: /No directory 1/i }));
    fireEvent.click(screen.getAllByRole('button', { name: /Other conversation/i })[0]);

    expect(mockNavigate).toHaveBeenCalledWith('/c/conv-456');
  });

  it('ignores stale new-chat stream events after switching conversations', async () => {
    mockGetConversation.mockImplementation(async (id: string) => ({
      id,
      createdAt: '2024-01-01T00:00:00Z',
      updatedAt: '2024-01-01T00:00:00Z',
      messageCount: 1,
      messages:
        id === 'conv-456'
          ? [
              {
                role: 'user',
                content: 'Existing conversation',
              },
            ]
          : [],
      toolResults: {},
    }));

    let streamOptions: { onEvent: (event: ChatStreamEvent) => void } | null = null;
    let resolveStream: (() => void) | null = null;
    mockStreamChat.mockImplementation(
      async (_request, options) =>
        new Promise<void>((resolve) => {
          streamOptions = options as {
            onEvent: (event: ChatStreamEvent) => void;
          };
          resolveStream = resolve;
        })
    );

    const { rerender } = await renderChatWithRunner();

    await waitFor(() => expect(mockGetConversations).toHaveBeenCalled());

    fireEvent.change(screen.getByPlaceholderText('Ask kodelet...'), {
      target: { value: 'hello' },
    });
    fireEvent.click(screen.getByRole('button', { name: 'Send' }));

    await waitFor(() => expect(mockStreamChat).toHaveBeenCalled());

    await act(async () => {
      streamOptions?.onEvent({
        kind: 'conversation',
        conversation_id: 'conv-123',
      });
    });

    setRouteParams({ id: 'conv-456' });
    rerender(<ChatPage />);

    await waitFor(() => expect(mockGetConversation).toHaveBeenCalledWith('conv-456'));
    await waitFor(() => expect(screen.getByText('Existing conversation')).toBeInTheDocument());

    await act(async () => {
      streamOptions?.onEvent({
        kind: 'text-delta',
        conversation_id: 'conv-123',
        delta: 'Leaked streamed text',
      });
      resolveStream?.();
    });

    expect(screen.queryByText('Leaked streamed text')).not.toBeInTheDocument();
    expect(mockNavigate).not.toHaveBeenCalledWith('/c/conv-123');
    expect(mockGetConversation).not.toHaveBeenCalledWith('conv-123');
  });

  it('keeps the running indicator on the streaming conversation after switching conversations', async () => {
    mockGetConversations.mockResolvedValue({
      conversations: [
        {
          id: 'conv-123',
          createdAt: '2024-01-01T00:00:00Z',
          updatedAt: '2024-01-03T00:00:00Z',
          messageCount: 1,
          summary: 'Running task',
        },
        {
          id: 'conv-456',
          createdAt: '2024-01-01T00:00:00Z',
          updatedAt: '2024-01-02T00:00:00Z',
          messageCount: 1,
          summary: 'Other conversation',
        },
      ],
      hasMore: false,
      total: 2,
      limit: 40,
      offset: 0,
    });
    mockGetConversation.mockImplementation(async (id: string) => ({
      id,
      createdAt: '2024-01-01T00:00:00Z',
      updatedAt: '2024-01-01T00:00:00Z',
      messageCount: 1,
      summary: id === 'conv-456' ? 'Other conversation' : 'Running task',
      messages: [],
      toolResults: {},
    }));

    let streamOptions: { onEvent: (event: ChatStreamEvent) => void } | null = null;
    mockStreamChat.mockImplementation(
      async (_request, options) =>
        new Promise<void>(() => {
          streamOptions = options as {
            onEvent: (event: ChatStreamEvent) => void;
          };
        })
    );

    const { rerender } = await renderChatWithRunner();

    await waitFor(() => expect(mockGetConversations).toHaveBeenCalled());

    fireEvent.change(screen.getByPlaceholderText('Ask kodelet...'), {
      target: { value: 'start running task' },
    });
    fireEvent.click(screen.getByRole('button', { name: 'Send' }));

    await waitFor(() => expect(mockStreamChat).toHaveBeenCalled());

    await act(async () => {
      streamOptions?.onEvent({
        kind: 'conversation',
        conversation_id: 'conv-123',
      });
    });

    expect(screen.getByTestId('conversation-running-indicator-conv-123')).toBeInTheDocument();

    setRouteParams({ id: 'conv-456' });
    rerender(<ChatPage />);

    await waitFor(() => expect(mockGetConversation).toHaveBeenCalledWith('conv-456'));

    expect(screen.getByTestId('conversation-running-indicator-conv-123')).toBeInTheDocument();
    expect(screen.getByTestId('conversation-row-conv-123')).toHaveClass('running');
    expect(screen.queryByTestId('conversation-running-indicator-conv-456')).not.toBeInTheDocument();
  });

  it('offers reload when a conversation fails to load', async () => {
    setRouteParams({ id: 'conv-123' });
    mockGetConversation.mockRejectedValueOnce(new TypeError('Failed to fetch'));

    render(<ChatPage />);

    expect(await screen.findByText('Load error')).toBeInTheDocument();
    expect(screen.getByText('Failed to fetch')).toBeInTheDocument();
    expect(screen.getByRole('button', { name: 'Reload' })).toBeInTheDocument();
  });

  it.each([
    false,
    true,
  ])('offers reload on stream failure (received events: %s)', async (receivedEvents) => {
    setRouteParams({ id: 'conv-123' });
    mockGetConversation.mockResolvedValue({
      id: 'conv-123',
      runnerId: 'runner-1',
      createdAt: '2024-01-01T00:00:00Z',
      updatedAt: '2024-01-01T00:00:00Z',
      messageCount: 0,
      messages: [],
      toolResults: {},
    });
    mockStreamConversation.mockImplementation(async (_id, options) => {
      if (receivedEvents) {
        options.onEvent({ kind: 'conversation', conversation_id: 'conv-123' });
      }
      throw new TypeError('Failed to fetch');
    });

    render(<ChatPage />);

    expect(await screen.findByRole('alert')).toHaveTextContent('Failed to fetch');
    expect(screen.getByRole('button', { name: 'Reload' })).toBeInTheDocument();
    expect(mockStreamChat).not.toHaveBeenCalled();
  });

  it('clears selected conversation running state when stream attach is stale', async () => {
    setRouteParams({ id: 'conv-123' });
    mockGetConversations.mockResolvedValue({
      conversations: [
        {
          id: 'conv-123',
          createdAt: '2024-01-01T00:00:00Z',
          updatedAt: '2024-01-03T00:00:00Z',
          messageCount: 1,
          summary: 'Stale running task',
          isRunning: true,
        },
      ],
      hasMore: false,
      total: 1,
      limit: 40,
      offset: 0,
    });
    mockGetConversation.mockResolvedValue({
      id: 'conv-123',
      createdAt: '2024-01-01T00:00:00Z',
      updatedAt: '2024-01-01T00:00:00Z',
      messageCount: 1,
      summary: 'Stale running task',
      messages: [],
      toolResults: {},
      isRunning: true,
    });
    mockStreamConversation.mockRejectedValue(new Error('conversation is not actively streaming'));

    render(<ChatPage />);

    await waitFor(() =>
      expect(mockStreamConversation).toHaveBeenCalledWith('conv-123', expect.any(Object))
    );
    await waitFor(() =>
      expect(screen.queryByRole('button', { name: 'Stop' })).not.toBeInTheDocument()
    );
    expect(screen.getByRole('button', { name: 'Send' })).toBeInTheDocument();
    expect(screen.queryByTestId('conversation-running-indicator-conv-123')).not.toBeInTheDocument();
    expect(screen.queryByRole('button', { name: 'Reload' })).not.toBeInTheDocument();
  });

  it('allows sending in another conversation while one conversation is streaming', async () => {
    mockGetConversations.mockResolvedValue({
      conversations: [
        {
          id: 'conv-123',
          createdAt: '2024-01-01T00:00:00Z',
          updatedAt: '2024-01-03T00:00:00Z',
          messageCount: 1,
          summary: 'Running task',
        },
        {
          id: 'conv-456',
          createdAt: '2024-01-01T00:00:00Z',
          updatedAt: '2024-01-02T00:00:00Z',
          messageCount: 1,
          summary: 'Second task',
        },
      ],
      hasMore: false,
      total: 2,
      limit: 40,
      offset: 0,
    });
    mockGetConversation.mockImplementation(async (id: string) => ({
      runnerId: 'runner-1',
      id,
      createdAt: '2024-01-01T00:00:00Z',
      updatedAt: '2024-01-01T00:00:00Z',
      messageCount: 1,
      summary: id === 'conv-456' ? 'Second task' : 'Running task',
      messages: [],
      toolResults: {},
    }));

    const streamOptionsByCall: Array<{
      onEvent: (event: ChatStreamEvent) => void;
    }> = [];
    mockStreamChat.mockImplementation(
      async (_request, options) =>
        new Promise<void>(() => {
          streamOptionsByCall.push(
            options as {
              onEvent: (event: ChatStreamEvent) => void;
            }
          );
        })
    );

    const { rerender } = await renderChatWithRunner();

    await waitFor(() => expect(mockGetConversations).toHaveBeenCalled());

    fireEvent.change(screen.getByPlaceholderText('Ask kodelet...'), {
      target: { value: 'start first task' },
    });
    fireEvent.click(screen.getByRole('button', { name: 'Send' }));

    await waitFor(() => expect(mockStreamChat).toHaveBeenCalledTimes(1));

    await act(async () => {
      streamOptionsByCall[0]?.onEvent({
        kind: 'conversation',
        conversation_id: 'conv-123',
      });
    });

    setRouteParams({ id: 'conv-456' });
    rerender(<ChatPage />);

    const secondComposer = await screen.findByPlaceholderText('Ask kodelet...');
    expect(secondComposer).toBeEnabled();

    expect(screen.queryByRole('button', { name: 'Stop' })).not.toBeInTheDocument();
    expect(screen.getByRole('button', { name: 'Send' })).toBeInTheDocument();

    fireEvent.change(secondComposer, {
      target: { value: 'start second task' },
    });
    fireEvent.click(screen.getByRole('button', { name: 'Send' }));

    await waitFor(() => expect(mockStreamChat).toHaveBeenCalledTimes(2));
    expect(mockStreamChat).toHaveBeenLastCalledWith(
      expect.objectContaining({ conversationId: 'conv-456' }),
      expect.any(Object)
    );
    expect(screen.getByTestId('conversation-running-indicator-conv-123')).toBeInTheDocument();
  });

  it('queues steering immediately while a conversation is streaming', async () => {
    setRouteParams({ id: 'conv-123' });
    mockGetConversation.mockResolvedValue({
      runnerId: 'runner-1',
      id: 'conv-123',
      createdAt: '2023-01-01T00:00:00Z',
      updatedAt: '2023-01-02T00:00:00Z',
      messageCount: 1,
      profile: 'anthropic',
      profileLocked: true,
      messages: [
        {
          role: 'user',
          content: 'hello',
        },
      ],
      toolResults: {},
    });

    mockStreamChat.mockImplementation(async () => new Promise(() => undefined));

    render(<ChatPage />);

    await waitFor(() => expect(mockGetConversation).toHaveBeenCalledWith('conv-123'));

    fireEvent.change(screen.getByPlaceholderText('Ask kodelet...'), {
      target: { value: 'continue' },
    });
    fireEvent.click(screen.getByRole('button', { name: 'Send' }));

    await waitFor(() => expect(mockStreamChat).toHaveBeenCalled());

    const steerButton = await screen.findByRole('button', { name: 'Steer' });
    expect(steerButton).toBeDisabled();

    const textarea = screen.getByPlaceholderText('Ask kodelet...');
    fireEvent.change(textarea, { target: { value: 'Focus on tests' } });
    await waitFor(() => expect(steerButton).toBeEnabled());
    fireEvent.click(steerButton);

    await waitFor(() =>
      expect(mockSteerConversation).toHaveBeenCalledWith(
        'conv-123',
        'Focus on tests',
        expect.arrayContaining([
          expect.objectContaining({
            type: 'text',
            text: 'Focus on tests',
          }),
        ])
      )
    );
  });

  it('stops an active streaming conversation', async () => {
    const abortSpy = vi.fn();
    const originalAbortController = global.AbortController;
    let rejectStream: ((reason?: unknown) => void) | null = null;

    class MockAbortController {
      signal = {} as AbortSignal;
      abort = abortSpy;
    }

    global.AbortController = MockAbortController as unknown as typeof AbortController;

    mockStreamChat.mockImplementation(
      async (_request, options) =>
        new Promise((_, reject) => {
          options.onEvent({
            kind: 'conversation',
            conversation_id: 'conv-123',
          } as ChatStreamEvent);
          rejectStream = reject;
        })
    );

    await renderChatWithRunner();

    await waitFor(() => expect(mockGetConversations).toHaveBeenCalled());

    fireEvent.change(screen.getByPlaceholderText('Ask kodelet...'), {
      target: { value: 'hello' },
    });
    fireEvent.click(screen.getByRole('button', { name: 'Send' }));

    await waitFor(() => expect(mockStreamChat).toHaveBeenCalled());
    fireEvent.click(screen.getByRole('button', { name: 'Stop' }));

    expect(abortSpy).toHaveBeenCalled();
    await waitFor(() => expect(mockStopConversation).toHaveBeenCalledWith('conv-123'));
    expect(screen.queryByRole('button', { name: 'Stop' })).not.toBeInTheDocument();
    expect(screen.getByRole('button', { name: 'Send' })).toBeInTheDocument();

    await act(async () => {
      rejectStream?.(new DOMException('The operation was aborted', 'AbortError'));
    });

    await waitFor(() => expect(screen.getByRole('button', { name: 'Send' })).toBeInTheDocument());

    global.AbortController = originalAbortController;
  });
});
