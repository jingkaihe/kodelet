import { act, fireEvent, render, screen, waitFor } from '@testing-library/react';
import { Profiler } from 'react';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import type { ChatStreamEvent, ConversationListResponse } from '../types';
import {
  ChatPage,
  flushAsyncUpdates,
  mockDeleteConversation,
  mockForkConversation,
  mockGetChatSettings,
  mockGetConversation,
  mockGetConversations,
  mockNavigate,
  mockStreamChat,
  mockStreamConversation,
  renderChatWithRunner,
  setRouteParams,
  setupChatPageTests,
} from './ChatPage.testSupport';

describe('ChatPage sidebar polling and conversation management', () => {
  setupChatPageTests();

  describe('sidebar polling', () => {
    const conversation = {
      id: 'conv-123',
      createdAt: '2024-01-01T00:00:00Z',
      updatedAt: '2024-01-02T00:00:00Z',
      messageCount: 1,
      summary: 'Running task',
      cwd: '/runner/kodelet',
      isRunning: true,
    };
    const response: ConversationListResponse = {
      conversations: [conversation],
      total: 1,
      hasMore: false,
      limit: 100,
      offset: 0,
      cwds: ['/runner/kodelet'],
    };

    beforeEach(() => {
      mockGetConversations.mockReset().mockResolvedValue(response);
      mockStreamConversation.mockImplementation(async () => new Promise(() => undefined));
    });

    it('discovers external conversations and nested subagents without reloading the transcript', async () => {
      vi.useFakeTimers();
      setRouteParams({ id: conversation.id });
      mockGetConversation.mockResolvedValue({
        ...conversation,
        messages: [{ role: 'assistant', content: 'Keep this transcript intact.' }],
      });
      mockGetConversations.mockResolvedValueOnce(response).mockResolvedValue({
        ...response,
        total: 3,
        conversations: [
          { ...conversation, id: 'conv-external', summary: 'Task from another browser' },
          {
            ...conversation,
            id: 'conv-child',
            summary: 'New subagent',
            parentConversationId: conversation.id,
          },
          conversation,
        ],
      });

      try {
        render(<ChatPage />);
        await flushAsyncUpdates();
        const transcript = screen.getByText('Keep this transcript intact.');
        const selectedRow = screen.getByTestId('conversation-row-conv-123');

        await act(async () => vi.advanceTimersByTimeAsync(5000));
        const externalRow = screen.getByTestId('conversation-row-conv-external');
        const childRow = screen.getByTestId('conversation-row-conv-child');
        expect(externalRow).toHaveAttribute('data-depth', '0');
        expect(childRow).toHaveAttribute('data-depth', '1');
        expect(screen.getByTestId('conversation-row-conv-123')).toBe(selectedRow);
        expect(selectedRow).toHaveClass('active');
        expect(screen.getByText('Keep this transcript intact.')).toBe(transcript);
        expect(mockGetConversation).toHaveBeenCalledTimes(1);
        expect(mockNavigate).not.toHaveBeenCalled();
      } finally {
        vi.useRealTimers();
      }
    });

    it('keeps unchanged polls silent without committing renders or restarting spinners and streams', async () => {
      vi.useFakeTimers();
      const intervalSpy = vi.spyOn(window, 'setInterval');
      const onRender = vi.fn();
      const pendingPoll = {
        resolve: null as ((value: ConversationListResponse) => void) | null,
      };
      mockGetConversations.mockResolvedValueOnce(response).mockImplementationOnce(
        () =>
          new Promise<ConversationListResponse>((resolve) => {
            pendingPoll.resolve = resolve;
          })
      );

      try {
        render(
          <Profiler id="chat" onRender={onRender}>
            <ChatPage />
          </Profiler>
        );
        await flushAsyncUpdates();
        const indicator = screen.getByTestId('conversation-running-indicator-conv-123');
        const spinner = indicator.querySelector('.spinner-glyph');
        expect(spinner).not.toBeNull();
        const list = screen.getByTestId('chat-sidebar-shell').querySelector('.conversation-list');
        const subscriptionSignal = mockStreamConversation.mock.calls[0][1].signal as AbortSignal;
        const intervalCount = intervalSpy.mock.calls.length;

        await act(async () => vi.advanceTimersByTimeAsync(5000));
        expect(mockGetConversations).toHaveBeenCalledTimes(2);
        expect(list).toHaveAttribute('aria-busy', 'false');
        onRender.mockClear();

        await act(async () => {
          pendingPoll.resolve?.({
            ...response,
            conversations: [{ ...conversation }],
            cwds: [...(response.cwds || [])],
          });
        });

        expect(onRender).not.toHaveBeenCalled();
        expect(screen.getByTestId('conversation-running-indicator-conv-123')).toBe(indicator);
        expect(indicator.querySelector('.spinner-glyph')).toBe(spinner);
        expect(intervalSpy).toHaveBeenCalledTimes(intervalCount);
        expect(mockStreamConversation).toHaveBeenCalledTimes(1);
        expect(subscriptionSignal.aborted).toBe(false);
      } finally {
        intervalSpy.mockRestore();
        vi.useRealTimers();
      }
    });

    it('queues visibility refreshes without overlapping polls and aborts superseded or unmounted requests', async () => {
      vi.useFakeTimers();
      const visibility = vi.spyOn(document, 'visibilityState', 'get').mockReturnValue('visible');
      const pendingPoll = {
        resolve: null as ((value: ConversationListResponse) => void) | null,
      };
      mockGetConversations.mockResolvedValueOnce(response).mockImplementation(
        () =>
          new Promise<ConversationListResponse>((resolve) => {
            pendingPoll.resolve = resolve;
          })
      );

      try {
        const { unmount } = render(<ChatPage />);
        await flushAsyncUpdates();

        visibility.mockReturnValue('hidden');
        fireEvent(document, new Event('visibilitychange'));
        await act(async () => vi.advanceTimersByTimeAsync(15000));
        expect(mockGetConversations).toHaveBeenCalledTimes(1);

        visibility.mockReturnValue('visible');
        fireEvent(document, new Event('visibilitychange'));
        await flushAsyncUpdates();
        expect(mockGetConversations).toHaveBeenCalledTimes(2);
        visibility.mockReturnValue('hidden');
        fireEvent(document, new Event('visibilitychange'));
        visibility.mockReturnValue('visible');
        fireEvent(document, new Event('visibilitychange'));
        await act(async () => vi.advanceTimersByTimeAsync(10000));
        expect(mockGetConversations).toHaveBeenCalledTimes(2);

        await act(async () => pendingPoll.resolve?.(response));
        expect(mockGetConversations).toHaveBeenCalledTimes(3);
        const pollSignal = mockGetConversations.mock.calls[2][1] as AbortSignal;
        const settlePoll = pendingPoll.resolve;
        fireEvent.click(screen.getByRole('button', { name: 'More actions for Running task' }));
        fireEvent.click(screen.getByRole('menuitem', { name: 'Copy' }));
        await flushAsyncUpdates();
        expect(mockGetConversations).toHaveBeenCalledTimes(4);
        expect(pollSignal.aborted).toBe(true);
        const signal = mockGetConversations.mock.calls[3][1] as AbortSignal;
        unmount();
        expect(signal.aborted).toBe(true);
        await act(async () => settlePoll?.(response));
        fireEvent(document, new Event('visibilitychange'));
        await act(async () => vi.advanceTimersByTimeAsync(15000));
        expect(mockGetConversations).toHaveBeenCalledTimes(4);
        expect(vi.getTimerCount()).toBe(0);
      } finally {
        visibility.mockRestore();
        vi.useRealTimers();
      }
    });

    it('retains the list after a transient failure and resets the polling delay after recovery', async () => {
      vi.useFakeTimers();
      const consoleError = vi.spyOn(console, 'error').mockImplementation(() => undefined);
      mockGetConversations
        .mockResolvedValueOnce(response)
        .mockRejectedValueOnce(new Error('Temporarily offline'))
        .mockResolvedValue({
          ...response,
          conversations: [{ ...conversation, summary: 'Recovered task' }],
        });

      try {
        render(<ChatPage />);
        await flushAsyncUpdates();
        const row = screen.getByTestId('conversation-row-conv-123');

        await act(async () => vi.advanceTimersByTimeAsync(5000));
        expect(row).toHaveTextContent('Running task');
        expect(screen.queryByRole('alert')).not.toBeInTheDocument();

        await act(async () => vi.advanceTimersByTimeAsync(5000));
        expect(mockGetConversations).toHaveBeenCalledTimes(2);
        await act(async () => vi.advanceTimersByTimeAsync(5000));
        expect(row).toHaveTextContent('Recovered task');

        await act(async () => vi.advanceTimersByTimeAsync(5000));
        expect(mockGetConversations).toHaveBeenCalledTimes(4);
      } finally {
        consoleError.mockRestore();
        vi.useRealTimers();
      }
    });

    it('discovers new rows without overwriting newer stream running states during a poll', async () => {
      vi.useFakeTimers();
      setRouteParams({ id: conversation.id });
      const idleConversation = { ...conversation, isRunning: false };
      const initialResponse = {
        ...response,
        total: 2,
        conversations: [idleConversation, { ...conversation, id: 'conv-background' }],
      };
      const pendingPoll = {
        resolve: null as ((value: ConversationListResponse) => void) | null,
      };
      const subscriptions: Record<string, { onEvent: (event: ChatStreamEvent) => void }> = {};
      mockGetConversations.mockResolvedValueOnce(initialResponse).mockImplementationOnce(
        () =>
          new Promise<ConversationListResponse>((resolve) => {
            pendingPoll.resolve = resolve;
          })
      );
      mockGetConversation.mockResolvedValue({ ...idleConversation, messages: [], toolResults: {} });
      mockStreamConversation.mockImplementation(async (id, options) => {
        subscriptions[id] = options;
        return new Promise(() => undefined);
      });

      try {
        render(<ChatPage />);
        await flushAsyncUpdates();
        expect(
          screen.getByTestId('conversation-running-indicator-conv-background')
        ).toBeInTheDocument();
        expect(
          screen.queryByTestId('conversation-running-indicator-conv-123')
        ).not.toBeInTheDocument();
        await act(async () => vi.advanceTimersByTimeAsync(5000));

        await act(async () => {
          subscriptions['conv-background'].onEvent({
            kind: 'done',
            conversation_id: 'conv-background',
          });
          subscriptions['conv-123'].onEvent({ kind: 'conversation', conversation_id: 'conv-123' });
        });
        expect(
          screen.queryByTestId('conversation-running-indicator-conv-background')
        ).not.toBeInTheDocument();
        expect(screen.getByTestId('conversation-running-indicator-conv-123')).toBeInTheDocument();

        await act(async () => {
          pendingPoll.resolve?.({
            ...initialResponse,
            total: 3,
            conversations: [
              ...initialResponse.conversations,
              { ...idleConversation, id: 'conv-unrelated', summary: 'Another new task' },
            ],
          });
        });
        expect(screen.getByTestId('conversation-row-conv-unrelated')).toHaveTextContent(
          'Another new task'
        );
        expect(
          screen.queryByTestId('conversation-running-indicator-conv-background')
        ).not.toBeInTheDocument();
        expect(screen.getByTestId('conversation-running-indicator-conv-123')).toBeInTheDocument();
        expect(mockStreamConversation).toHaveBeenCalledTimes(2);
      } finally {
        vi.useRealTimers();
      }
    });
  });

  it('adds a newly started conversation to the sidebar before refresh', async () => {
    mockGetConversations.mockResolvedValue({
      conversations: [
        {
          id: 'conv-456',
          createdAt: '2024-01-01T00:00:00Z',
          updatedAt: '2024-01-01T00:00:00Z',
          messageCount: 1,
          summary: 'Existing conversation',
        },
      ],
      hasMore: false,
      total: 1,
      limit: 40,
      offset: 0,
    });

    mockGetConversation.mockImplementation(async (id: string) => ({
      id,
      createdAt: '2024-01-01T00:00:00Z',
      updatedAt: '2024-01-01T00:00:00Z',
      messageCount: 1,
      messages: [
        {
          role: 'user',
          content: id === 'conv-456' ? 'Existing conversation' : 'Brand new task',
        },
      ],
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
      target: { value: 'Brand new task' },
    });
    fireEvent.click(screen.getByRole('button', { name: 'Send' }));

    await waitFor(() => expect(mockStreamChat).toHaveBeenCalled());

    await act(async () => {
      streamOptions?.onEvent({
        kind: 'conversation',
        conversation_id: 'conv-123',
      });
    });

    expect(screen.getAllByRole('button', { name: /Brand new task/i })[0]).toBeInTheDocument();

    setRouteParams({ id: 'conv-456' });
    rerender(<ChatPage />);

    await waitFor(() => expect(mockGetConversation).toHaveBeenCalledWith('conv-456'));
    expect(screen.getAllByRole('button', { name: /Brand new task/i })[0]).toBeInTheDocument();
  });

  it('keeps a new runner conversation in its directory group while streaming', async () => {
    mockGetConversations.mockResolvedValue({
      conversations: [
        {
          id: 'conv-existing',
          createdAt: '2024-01-01T00:00:00Z',
          updatedAt: '2024-01-01T00:00:00Z',
          messageCount: 1,
          summary: 'Existing conversation',
          cwd: '/runner/kodelet',
          runnerId: 'runner-1',
        },
      ],
      hasMore: false,
      total: 1,
      limit: 100,
      offset: 0,
    });

    let streamOptions: { onEvent: (event: ChatStreamEvent) => void } | null = null;
    mockStreamChat.mockImplementation(
      async (_request, options) =>
        new Promise<void>(() => {
          streamOptions = options as { onEvent: (event: ChatStreamEvent) => void };
        })
    );

    const { container } = await renderChatWithRunner();

    await screen.findByTestId('conversation-row-conv-existing');
    await waitFor(() => expect(mockGetChatSettings).toHaveBeenCalled());
    fireEvent.change(screen.getByPlaceholderText('Ask kodelet anything...'), {
      target: { value: 'Brand new task' },
    });
    fireEvent.click(screen.getByRole('button', { name: 'Send' }));

    await waitFor(() => expect(mockStreamChat).toHaveBeenCalled());
    const conversationId = mockStreamChat.mock.calls[0]?.[0]?.conversationId as string;

    await act(async () => {
      streamOptions?.onEvent({
        kind: 'conversation',
        conversation_id: conversationId,
        cwd: '/runner/kodelet',
      });
    });

    expect(container.querySelectorAll('.conversation-group')).toHaveLength(1);
    expect(screen.getByRole('button', { name: /\/runner\/kodelet 2/i })).toBeInTheDocument();
  });

  it('forks a conversation from the sidebar menu', async () => {
    mockGetConversations.mockResolvedValue({
      conversations: [
        {
          id: 'conv-123',
          createdAt: '2024-01-01T00:00:00Z',
          updatedAt: '2024-01-01T00:00:00Z',
          messageCount: 1,
          summary: 'Enabled resumable webUI conversation',
        },
      ],
      hasMore: false,
      total: 1,
      limit: 40,
      offset: 0,
    });

    render(<ChatPage />);

    fireEvent.click(
      await screen.findByRole('button', {
        name: /More actions for Enabled resumable webUI conversation/i,
      })
    );
    fireEvent.click(screen.getByRole('menuitem', { name: 'Copy' }));

    await waitFor(() => expect(mockForkConversation).toHaveBeenCalledWith('conv-123'));
    await waitFor(() => expect(mockNavigate).toHaveBeenCalledWith('/c/conv-copy-123'));
  });

  it('deletes the active conversation from the sidebar menu', async () => {
    setRouteParams({ id: 'conv-123' });
    mockGetConversations.mockResolvedValue({
      conversations: [
        {
          id: 'conv-123',
          createdAt: '2024-01-01T00:00:00Z',
          updatedAt: '2024-01-01T00:00:00Z',
          messageCount: 1,
          summary: 'Enabled resumable webUI conversation',
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
      summary: 'Enabled resumable webUI conversation',
      messages: [],
      toolResults: {},
    });

    render(<ChatPage />);

    await waitFor(() => expect(mockGetConversation).toHaveBeenCalledWith('conv-123'));

    fireEvent.click(
      screen.getByRole('button', {
        name: /More actions for Enabled resumable webUI conversation/i,
      })
    );
    fireEvent.click(screen.getByRole('menuitem', { name: 'Delete' }));

    await waitFor(() => expect(mockDeleteConversation).toHaveBeenCalledWith('conv-123'));
    await waitFor(() => expect(mockNavigate).toHaveBeenCalledWith('/'));
  });

  it('groups recent chats by cwd and lets directories collapse independently', async () => {
    mockGetConversations.mockResolvedValueOnce({
      conversations: Array.from({ length: 12 }, (_, index) => ({
        id: `conv-${index + 1}`,
        createdAt: `2024-01-${String(index + 1).padStart(2, '0')}T00:00:00Z`,
        updatedAt: `2024-01-${String(index + 1).padStart(2, '0')}T00:00:00Z`,
        messageCount: 1,
        summary: `Conversation ${index + 1}`,
        cwd: index < 6 ? '/workspace/a' : index < 10 ? '/workspace/b' : '/workspace/c',
      })),
      hasMore: false,
      total: 12,
      limit: 100,
      offset: 0,
    });

    render(<ChatPage />);

    await waitFor(() => expect(mockGetConversations).toHaveBeenCalled());
    expect(screen.getByRole('button', { name: /\/workspace\/a 6/i })).toBeInTheDocument();
    expect(screen.getByRole('button', { name: /\/workspace\/b 4/i })).toBeInTheDocument();
    expect(screen.getByRole('button', { name: /\/workspace\/c 2/i })).toBeInTheDocument();

    fireEvent.click(screen.getByRole('button', { name: /\/workspace\/a 6/i }));
    await waitFor(() => expect(screen.getByText('Conversation 1')).toBeInTheDocument());

    fireEvent.click(screen.getByRole('button', { name: /\/workspace\/b 4/i }));
    await waitFor(() => expect(screen.getByText('Conversation 7')).toBeInTheDocument());

    fireEvent.click(screen.getByRole('button', { name: /\/workspace\/b/i }));

    await waitFor(() => expect(screen.queryByText('Conversation 7')).not.toBeInTheDocument());
    expect(screen.getByText('Conversation 1')).toBeInTheDocument();

    fireEvent.click(screen.getByRole('button', { name: /\/workspace\/b/i }));
    await waitFor(() => expect(screen.getByText('Conversation 7')).toBeInTheDocument());
  });

  it('shows a compact home cwd label in recent chats and hides sidebar metadata', async () => {
    mockGetConversation.mockResolvedValue({
      id: 'conv-123',
      cwd: '~/workspace/kodelet',
      messages: [],
      toolResults: {},
    });
    mockGetConversations.mockResolvedValue({
      conversations: [
        {
          id: 'conv-123',
          createdAt: '2024-01-01T00:00:00Z',
          updatedAt: '2024-01-01T00:00:00Z',
          messageCount: 1,
          summary: 'Conversation 1',
          cwd: '~/workspace/kodelet',
        },
      ],
      hasMore: false,
      total: 1,
      limit: 100,
      offset: 0,
    });

    setRouteParams({ id: 'conv-123' });
    render(<ChatPage />);

    expect(
      await screen.findByRole('button', { name: /~\/workspace\/kodelet 1/i })
    ).toBeInTheDocument();
    expect(screen.queryByText(/^ID:/)).not.toBeInTheDocument();
    expect(screen.queryByText(/^Mode:/)).not.toBeInTheDocument();
  });

  it('reveals more conversations within an expanded directory', async () => {
    mockGetConversations.mockResolvedValue({
      conversations: Array.from({ length: 12 }, (_, index) => ({
        id: `conv-${index + 1}`,
        createdAt: `2024-01-${String(index + 1).padStart(2, '0')}T00:00:00Z`,
        updatedAt: `2024-01-${String(index + 1).padStart(2, '0')}T00:00:00Z`,
        messageCount: 1,
        summary: `Conversation ${index + 1}`,
        cwd: '/workspace/kodelet',
      })),
      hasMore: false,
      total: 12,
      limit: 100,
      offset: 0,
    });

    render(<ChatPage />);

    await waitFor(() => expect(mockGetConversations).toHaveBeenCalled());
    expect(screen.getByText('Conversation 10')).toBeInTheDocument();
    expect(screen.queryByText('Conversation 11')).not.toBeInTheDocument();
    expect(screen.getByRole('button', { name: 'Show 2 more' })).toBeInTheDocument();

    fireEvent.click(screen.getByRole('button', { name: 'Show 2 more' }));

    await waitFor(() => expect(screen.getByText('Conversation 11')).toBeInTheDocument());
    expect(screen.getByText('Conversation 12')).toBeInTheDocument();
    expect(screen.getByRole('button', { name: 'Show less' })).toBeInTheDocument();
  });

  it('lets an expanded directory show less before all conversations are revealed', async () => {
    mockGetConversations.mockResolvedValue({
      conversations: Array.from({ length: 25 }, (_, index) => ({
        id: `conv-${index + 1}`,
        createdAt: `2024-01-${String((index % 28) + 1).padStart(2, '0')}T00:00:00Z`,
        updatedAt: `2024-01-${String((index % 28) + 1).padStart(2, '0')}T00:00:00Z`,
        messageCount: 1,
        summary: `Conversation ${index + 1}`,
        cwd: '/workspace/kodelet',
      })),
      hasMore: false,
      total: 25,
      limit: 100,
      offset: 0,
    });

    render(<ChatPage />);

    await waitFor(() => expect(mockGetConversations).toHaveBeenCalled());
    expect(screen.getByRole('button', { name: 'Show 10 more' })).toBeInTheDocument();

    fireEvent.click(screen.getByRole('button', { name: 'Show 10 more' }));

    await waitFor(() => expect(screen.getByText('Conversation 20')).toBeInTheDocument());
    expect(screen.queryByText('Conversation 21')).not.toBeInTheDocument();
    expect(screen.getByRole('button', { name: 'Show less' })).toBeInTheDocument();
    expect(screen.getByRole('button', { name: 'Show 5 more' })).toBeInTheDocument();

    fireEvent.click(screen.getByRole('button', { name: 'Show less' }));

    await waitFor(() => expect(screen.queryByText('Conversation 11')).not.toBeInTheDocument());
    expect(screen.getByRole('button', { name: 'Show 10 more' })).toBeInTheDocument();
  });

  it('disables delete for the active conversation while it is streaming', async () => {
    setRouteParams({ id: 'conv-123' });

    mockGetConversations.mockResolvedValue({
      conversations: [
        {
          id: 'conv-123',
          createdAt: '2024-01-01T00:00:00Z',
          updatedAt: '2024-01-01T00:00:00Z',
          messageCount: 1,
          summary: 'Enabled resumable webUI conversation',
        },
      ],
      hasMore: false,
      total: 1,
      limit: 40,
      offset: 0,
    });
    mockGetConversation.mockResolvedValue({
      runnerId: 'runner-1',
      id: 'conv-123',
      createdAt: '2024-01-01T00:00:00Z',
      updatedAt: '2024-01-01T00:00:00Z',
      messageCount: 1,
      summary: 'Enabled resumable webUI conversation',
      messages: [],
      toolResults: {},
    });
    mockStreamChat.mockImplementation(
      async (_request, options) =>
        new Promise(() => {
          options.onEvent({
            kind: 'conversation',
            conversation_id: 'conv-123',
          } as ChatStreamEvent);
        })
    );

    render(<ChatPage />);

    await waitFor(() => expect(mockGetConversation).toHaveBeenCalledWith('conv-123'));

    fireEvent.change(await screen.findByPlaceholderText('Ask kodelet anything...'), {
      target: { value: 'continue' },
    });
    fireEvent.click(screen.getByRole('button', { name: 'Send' }));

    await waitFor(() => expect(mockStreamChat).toHaveBeenCalled());

    fireEvent.click(
      screen.getByRole('button', {
        name: /More actions for Enabled resumable webUI conversation/i,
      })
    );
    expect(screen.getByRole('menuitem', { name: 'Delete' })).toBeDisabled();
    expect(mockDeleteConversation).not.toHaveBeenCalled();
  });
});
