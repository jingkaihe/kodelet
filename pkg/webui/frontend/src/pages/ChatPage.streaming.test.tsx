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
} from './ChatPage.testSupport';

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
      concurrentRuns: true,
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

    fireEvent.change(screen.getByPlaceholderText('Steer the active conversation…'), {
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

    fireEvent.change(screen.getByPlaceholderText('Ask kodelet anything...'), {
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

    fireEvent.change(screen.getByPlaceholderText('Steer the active conversation…'), {
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

    fireEvent.change(screen.getByPlaceholderText('Ask kodelet anything...'), {
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

    fireEvent.change(screen.getByPlaceholderText('Ask kodelet anything...'), {
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

    fireEvent.change(screen.getByPlaceholderText('Ask kodelet anything...'), {
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

    fireEvent.change(screen.getByPlaceholderText('Ask kodelet anything...'), {
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

    const secondComposer = await screen.findByPlaceholderText('Ask kodelet anything...');
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

    fireEvent.change(screen.getByPlaceholderText('Ask kodelet anything...'), {
      target: { value: 'continue' },
    });
    fireEvent.click(screen.getByRole('button', { name: 'Send' }));

    await waitFor(() => expect(mockStreamChat).toHaveBeenCalled());

    const steerButton = await screen.findByRole('button', { name: 'Steer' });
    expect(steerButton).toBeDisabled();

    const textarea = screen.getByPlaceholderText('Steer the active conversation…');
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

    fireEvent.change(screen.getByPlaceholderText('Ask kodelet anything...'), {
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
