import { act, fireEvent, render, screen, waitFor } from '@testing-library/react';
import { describe, expect, it, vi } from 'vitest';
import type { ConversationListResponse } from '../types';
import {
  ChatPage,
  flushAsyncUpdates,
  mockGetConversations,
  mockNavigate,
  mockStreamConversation,
  setupChatPageTests,
} from './ChatPage.testSupport';

describe('ChatPage conversation search', () => {
  setupChatPageTests();

  it('searches conversations and filters them by workspace', async () => {
    vi.useFakeTimers();

    mockGetConversations
      .mockResolvedValueOnce({
        conversations: [
          {
            id: 'conv-a',
            createdAt: '2024-01-01T00:00:00Z',
            updatedAt: '2024-01-02T00:00:00Z',
            messageCount: 1,
            summary: 'Alpha conversation',
            cwd: '/workspace/a',
          },
          {
            id: 'conv-b',
            createdAt: '2024-01-01T00:00:00Z',
            updatedAt: '2024-01-01T00:00:00Z',
            messageCount: 1,
            summary: 'Beta conversation',
            cwd: '/workspace/b',
          },
        ],
        hasMore: false,
        total: 2,
        cwds: ['/workspace/a', '/workspace/b', '/workspace/archive'],
        limit: 100,
        offset: 0,
      })
      .mockResolvedValue({
        conversations: [
          {
            id: 'conv-b',
            createdAt: '2024-01-01T00:00:00Z',
            updatedAt: '2024-01-01T00:00:00Z',
            messageCount: 1,
            summary: 'Beta conversation',
            cwd: '/workspace/b',
          },
        ],
        hasMore: false,
        total: 1,
        limit: 100,
        offset: 0,
      });

    try {
      render(<ChatPage />);
      await flushAsyncUpdates();

      expect(mockGetConversations).toHaveBeenCalledWith(
        {
          limit: 100,
          sortBy: 'updated',
          sortOrder: 'desc',
        },
        expect.any(AbortSignal)
      );

      fireEvent.click(screen.getByTestId('sidebar-search-toggle'));
      expect(screen.getByRole('dialog', { name: 'Search conversations' })).toBeInTheDocument();
      expect(screen.getByRole('option', { name: '/workspace/archive' })).toBeInTheDocument();
      fireEvent.change(screen.getByRole('searchbox', { name: 'Search conversations' }), {
        target: { value: 'beta' },
      });
      await act(async () => {
        vi.advanceTimersByTime(200);
        await Promise.resolve();
        await Promise.resolve();
      });

      expect(mockGetConversations).toHaveBeenLastCalledWith({
        searchTerm: 'beta',
        cwd: '',
        limit: 100,
        sortBy: 'updated',
        sortOrder: 'desc',
      });
      expect(screen.getByText('Beta conversation')).toBeInTheDocument();
      expect(screen.getByTestId('conversation-row-conv-a')).toBeInTheDocument();

      fireEvent.change(screen.getByLabelText('Search workspace'), {
        target: { value: '/workspace/b' },
      });
      await flushAsyncUpdates();

      expect(mockGetConversations).toHaveBeenLastCalledWith({
        searchTerm: 'beta',
        cwd: '/workspace/b',
        limit: 100,
        sortBy: 'updated',
        sortOrder: 'desc',
      });

      fireEvent.click(screen.getByRole('button', { name: /Beta conversation/i }));
      await flushAsyncUpdates();

      expect(screen.queryByTestId('conversation-search-dialog')).not.toBeInTheDocument();
      expect(mockNavigate).toHaveBeenCalledWith('/c/conv-b');
      expect(mockGetConversations).toHaveBeenCalledTimes(3);
    } finally {
      vi.useRealTimers();
    }
  });

  it('ignores an in-flight search after the search term changes', async () => {
    vi.useFakeTimers();
    const pendingAlphaSearch = {
      resolve: null as ((response: ConversationListResponse) => void) | null,
    };
    mockGetConversations
      .mockResolvedValueOnce({
        conversations: [],
        hasMore: false,
        total: 0,
        limit: 100,
        offset: 0,
      })
      .mockImplementationOnce(
        () =>
          new Promise<ConversationListResponse>((resolve) => {
            pendingAlphaSearch.resolve = resolve;
          })
      )
      .mockResolvedValueOnce({
        conversations: [
          {
            id: 'conv-beta',
            createdAt: '2024-01-01T00:00:00Z',
            updatedAt: '2024-01-01T00:00:00Z',
            messageCount: 1,
            summary: 'Beta conversation',
          },
        ],
        hasMore: false,
        total: 1,
        limit: 100,
        offset: 0,
      });

    try {
      render(<ChatPage />);
      await flushAsyncUpdates();

      fireEvent.click(screen.getByTestId('sidebar-search-toggle'));
      const searchInput = screen.getByRole('searchbox', { name: 'Search conversations' });
      fireEvent.change(searchInput, { target: { value: 'alpha' } });
      await act(async () => {
        vi.advanceTimersByTime(200);
        await Promise.resolve();
      });
      expect(pendingAlphaSearch.resolve).not.toBeNull();

      fireEvent.change(searchInput, { target: { value: 'beta' } });
      await act(async () => {
        const resolve = pendingAlphaSearch.resolve;
        if (!resolve) {
          throw new Error('alpha search did not start');
        }
        resolve({
          conversations: [
            {
              id: 'conv-alpha',
              createdAt: '2024-01-01T00:00:00Z',
              updatedAt: '2024-01-01T00:00:00Z',
              messageCount: 1,
              summary: 'Alpha conversation',
            },
          ],
          hasMore: false,
          total: 1,
          limit: 100,
          offset: 0,
        });
        await Promise.resolve();
      });

      expect(screen.queryByText('Alpha conversation')).not.toBeInTheDocument();

      await act(async () => {
        vi.advanceTimersByTime(200);
        await Promise.resolve();
        await Promise.resolve();
      });

      expect(screen.getByText('Beta conversation')).toBeInTheDocument();
    } finally {
      vi.useRealTimers();
    }
  });

  it('loads additional pages of conversation search results', async () => {
    vi.useFakeTimers();
    mockGetConversations
      .mockResolvedValueOnce({
        conversations: [],
        hasMore: false,
        total: 0,
        limit: 100,
        offset: 0,
      })
      .mockResolvedValueOnce({
        conversations: [
          {
            id: 'conv-alpha',
            createdAt: '2024-01-01T00:00:00Z',
            updatedAt: '2024-01-02T00:00:00Z',
            messageCount: 1,
            summary: 'Alpha conversation',
          },
        ],
        hasMore: true,
        total: 2,
        limit: 100,
        offset: 0,
      })
      .mockResolvedValueOnce({
        conversations: [
          {
            id: 'conv-beta',
            createdAt: '2024-01-01T00:00:00Z',
            updatedAt: '2024-01-01T00:00:00Z',
            messageCount: 1,
            summary: 'Beta conversation',
          },
        ],
        hasMore: false,
        total: 2,
        limit: 100,
        offset: 1,
      });

    try {
      render(<ChatPage />);
      await flushAsyncUpdates();

      fireEvent.click(screen.getByTestId('sidebar-search-toggle'));
      fireEvent.change(screen.getByRole('searchbox', { name: 'Search conversations' }), {
        target: { value: 'conversation' },
      });
      await act(async () => {
        vi.advanceTimersByTime(200);
        await Promise.resolve();
        await Promise.resolve();
      });

      expect(screen.getByRole('button', { name: /Alpha conversation/i })).toBeInTheDocument();
      fireEvent.click(screen.getByRole('button', { name: 'Load more' }));
      await flushAsyncUpdates();

      expect(mockGetConversations).toHaveBeenLastCalledWith({
        searchTerm: 'conversation',
        cwd: '',
        limit: 100,
        offset: 1,
        sortBy: 'updated',
        sortOrder: 'desc',
      });
      expect(screen.getByRole('button', { name: /Alpha conversation/i })).toBeInTheDocument();
      expect(screen.getByRole('button', { name: /Beta conversation/i })).toBeInTheDocument();
      expect(screen.queryByRole('button', { name: 'Load more' })).not.toBeInTheDocument();
    } finally {
      vi.useRealTimers();
    }
  });

  it('restores recent conversations without issuing an empty search request', async () => {
    vi.useFakeTimers();
    const recentConversation = {
      id: 'conv-recent',
      createdAt: '2024-01-01T00:00:00Z',
      updatedAt: '2024-01-02T00:00:00Z',
      messageCount: 1,
      summary: 'Recent conversation',
    };
    const filteredConversation = {
      id: 'conv-filtered',
      createdAt: '2024-01-01T00:00:00Z',
      updatedAt: '2024-01-01T00:00:00Z',
      messageCount: 1,
      summary: 'Filtered conversation',
    };
    mockGetConversations.mockImplementation(async (filters?: { searchTerm?: string }) => ({
      conversations: filters?.searchTerm ? [filteredConversation] : [recentConversation],
      hasMore: false,
      total: 1,
      limit: 100,
      offset: 0,
    }));

    try {
      render(<ChatPage />);
      await flushAsyncUpdates();

      fireEvent.click(screen.getByTestId('sidebar-search-toggle'));
      fireEvent.change(screen.getByRole('searchbox', { name: 'Search conversations' }), {
        target: { value: 'filtered' },
      });
      await act(async () => {
        vi.advanceTimersByTime(200);
        await Promise.resolve();
        await Promise.resolve();
      });
      expect(screen.getByText('Filtered conversation')).toBeInTheDocument();
      expect(mockGetConversations).toHaveBeenCalledTimes(2);

      fireEvent.click(screen.getByRole('button', { name: 'Clear conversation search' }));
      await act(async () => {
        vi.advanceTimersByTime(200);
        await Promise.resolve();
      });

      expect(screen.getAllByText('Recent conversation')).toHaveLength(2);
      expect(mockGetConversations).toHaveBeenCalledTimes(2);
    } finally {
      vi.useRealTimers();
    }
  });

  it('keeps background stream subscriptions active while search results are filtered', async () => {
    const conversations = [
      {
        id: 'conv-running',
        createdAt: '2024-01-01T00:00:00Z',
        updatedAt: '2024-01-02T00:00:00Z',
        messageCount: 1,
        summary: 'Running conversation',
        cwd: '/workspace/a',
        isRunning: true,
      },
      {
        id: 'conv-beta',
        createdAt: '2024-01-01T00:00:00Z',
        updatedAt: '2024-01-01T00:00:00Z',
        messageCount: 1,
        summary: 'Beta conversation',
        cwd: '/workspace/b',
      },
    ];
    mockGetConversations.mockImplementation(async (filters?: { searchTerm?: string }) => ({
      conversations: filters?.searchTerm ? [conversations[1]] : conversations,
      hasMore: false,
      total: filters?.searchTerm ? 1 : 2,
      limit: 100,
      offset: 0,
    }));
    const subscription = { signal: null as AbortSignal | null };
    mockStreamConversation.mockImplementation(async (conversationID, options) => {
      if (conversationID === 'conv-running') {
        subscription.signal = (options as { signal: AbortSignal }).signal;
      }
      return new Promise(() => undefined);
    });

    render(<ChatPage />);

    await waitFor(() => expect(subscription.signal).not.toBeNull());
    fireEvent.click(screen.getByTestId('sidebar-search-toggle'));
    fireEvent.change(screen.getByRole('searchbox', { name: 'Search conversations' }), {
      target: { value: 'beta' },
    });

    await waitFor(() =>
      expect(mockGetConversations).toHaveBeenLastCalledWith({
        searchTerm: 'beta',
        cwd: '',
        limit: 100,
        sortBy: 'updated',
        sortOrder: 'desc',
      })
    );

    expect(subscription.signal?.aborted).toBe(false);
    expect(screen.getByTestId('conversation-row-conv-running')).toBeInTheDocument();
    expect(screen.getByRole('button', { name: /Beta conversation/i })).toBeInTheDocument();
  });

  it('shows conversation search failures without replacing the sidebar list', async () => {
    vi.useFakeTimers();
    const consoleError = vi.spyOn(console, 'error').mockImplementation(() => undefined);
    mockGetConversations
      .mockResolvedValueOnce({
        conversations: [
          {
            id: 'conv-a',
            createdAt: '2024-01-01T00:00:00Z',
            updatedAt: '2024-01-01T00:00:00Z',
            messageCount: 1,
            summary: 'Alpha conversation',
          },
        ],
        hasMore: false,
        total: 1,
        limit: 100,
        offset: 0,
      })
      .mockRejectedValueOnce(new Error('Search is temporarily unavailable'));

    try {
      render(<ChatPage />);
      await flushAsyncUpdates();

      fireEvent.click(screen.getByTestId('sidebar-search-toggle'));
      fireEvent.change(screen.getByRole('searchbox', { name: 'Search conversations' }), {
        target: { value: 'needle' },
      });
      await act(async () => {
        vi.advanceTimersByTime(200);
        await Promise.resolve();
        await Promise.resolve();
      });

      expect(screen.getByRole('alert')).toHaveTextContent('Search is temporarily unavailable');
      expect(screen.getByTestId('conversation-row-conv-a')).toBeInTheDocument();
      expect(
        screen.queryByRole('button', { name: /Alpha conversation · No directory/i })
      ).not.toBeInTheDocument();
    } finally {
      consoleError.mockRestore();
      vi.useRealTimers();
    }
  });

  it('keeps a selected search result visible in the recent conversation list', async () => {
    vi.useFakeTimers();
    mockGetConversations.mockImplementation(async (filters?: { searchTerm?: string }) => ({
      conversations: filters?.searchTerm
        ? [
            {
              id: 'conv-found',
              createdAt: '2024-01-01T00:00:00Z',
              updatedAt: '2024-01-03T00:00:00Z',
              messageCount: 1,
              summary: 'Found conversation',
              cwd: '/workspace/a',
            },
          ]
        : [
            {
              id: 'conv-recent',
              createdAt: '2024-01-01T00:00:00Z',
              updatedAt: '2024-01-02T00:00:00Z',
              messageCount: 1,
              summary: 'Recent conversation',
              cwd: '/workspace/a',
            },
          ],
      hasMore: false,
      total: filters?.searchTerm ? 1 : 2,
      limit: 100,
      offset: 0,
    }));

    try {
      render(<ChatPage />);
      await flushAsyncUpdates();

      fireEvent.click(screen.getByTestId('sidebar-search-toggle'));
      fireEvent.change(screen.getByRole('searchbox', { name: 'Search conversations' }), {
        target: { value: 'found' },
      });
      await act(async () => {
        vi.advanceTimersByTime(200);
        await Promise.resolve();
        await Promise.resolve();
      });

      fireEvent.click(screen.getByRole('button', { name: /Found conversation/i }));

      expect(screen.getByTestId('conversation-row-conv-found')).toBeInTheDocument();
      expect(mockNavigate).toHaveBeenCalledWith('/c/conv-found');
    } finally {
      vi.useRealTimers();
    }
  });
});
