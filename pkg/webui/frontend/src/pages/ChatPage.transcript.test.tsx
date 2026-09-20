import { act, fireEvent, render, screen, waitFor } from '@testing-library/react';
import { describe, expect, it, vi } from 'vitest';
import type { ChatStreamEvent, CompactionMarker, Conversation } from '../types';
import {
  ChatPage,
  flushAsyncUpdates,
  mockGetConversation,
  mockStreamConversation,
  setRouteParams,
  setupChatPageTests,
} from './ChatPage.testSupport';

describe('ChatPage transcript statistics and scrolling', () => {
  setupChatPageTests();

  it('preserves compaction markers and old chat through live updates and completion reload', async () => {
    setRouteParams({ id: 'conv-123' });
    const apiCompaction: CompactionMarker = { id: 'compact-1', method: 'api', createdAt: '' };
    const summaryCompaction: CompactionMarker = {
      id: 'compact-2',
      method: 'summary',
      summary: '**Progress**\n\nThe work continues.',
      createdAt: '',
    };
    const conversation: Conversation = {
      id: 'conv-123',
      createdAt: '',
      updatedAt: '',
      messageCount: 3,
      messages: [
        { role: 'user', content: 'First question.' },
        { role: 'assistant', content: 'First answer.' },
        { role: 'assistant', kind: 'context-compacted', compaction: apiCompaction, content: '' },
      ],
    };
    mockGetConversation.mockResolvedValueOnce(conversation).mockResolvedValue({
      ...conversation,
      messageCount: 6,
      messages: [
        ...(conversation.messages || []),
        {
          role: 'assistant',
          kind: 'context-compacted',
          compaction: summaryCompaction,
          content: '',
        },
        { role: 'user', content: 'Second question.' },
        { role: 'assistant', content: 'Second answer.' },
      ],
    });
    let streamListener: ((event: ChatStreamEvent) => void) | null = null;
    mockStreamConversation.mockImplementation(async (_id, options) => {
      streamListener = (options as { onEvent: (event: ChatStreamEvent) => void }).onEvent;
      return new Promise(() => undefined);
    });

    const { container } = render(<ChatPage />);
    await waitFor(() => expect(streamListener).not.toBeNull());
    expect(screen.getByText('Context compacted')).toBeVisible();
    expect(screen.getByText('First answer.')).toBeVisible();

    await act(async () => {
      streamListener?.({ kind: 'conversation', conversation_id: 'conv-123' });
      streamListener?.({ kind: 'user-message', content: 'Second question.' });
      streamListener?.({
        kind: 'context-compacted',
        compaction: summaryCompaction,
        before_current_user: true,
      });
      streamListener?.({ kind: 'text-delta', delta: 'Second answer.' });
      streamListener?.({ kind: 'content-end' });
    });

    expect(screen.getAllByText('Context compacted')).toHaveLength(2);
    expect(screen.getByText('Progress')).not.toBeVisible();
    const marker = container.querySelector('[data-compaction-id="compact-2"]');
    expect(marker?.compareDocumentPosition(screen.getByText('Second question.'))).toBe(
      Node.DOCUMENT_POSITION_FOLLOWING
    );

    await act(async () => {
      streamListener?.({ kind: 'done', conversation_id: 'conv-123' });
    });
    await waitFor(() => expect(mockGetConversation).toHaveBeenCalledTimes(2));
    expect(screen.getAllByText('Context compacted')).toHaveLength(2);
    expect(screen.getAllByText('First question.')).toHaveLength(1);
    expect(screen.getAllByText('First answer.')).toHaveLength(1);
    expect(screen.getAllByText('Second question.')).toHaveLength(1);
    expect(screen.getAllByText('Second answer.')).toHaveLength(1);
    fireEvent.click(screen.getAllByText('Context compacted')[1]);
    expect(screen.getByText('Progress')).toBeVisible();
  });

  it('shows compact statistics with expandable exact usage and cost', async () => {
    setRouteParams({ id: 'conv-123' });
    const updatedAt = new Date(Date.now() - 3 * 60 * 1000).toISOString();
    mockGetConversation.mockResolvedValue({
      id: 'conv-123',
      createdAt: '2023-01-02T11:00:00Z',
      updatedAt,
      messageCount: 1,
      messages: [
        {
          role: 'user',
          content: 'hello',
        },
      ],
      toolResults: {},
      usage: {
        currentContextWindow: 14200,
        maxContextWindow: 272000,
        inputTokens: 1200,
        outputTokens: 340,
        cacheReadInputTokens: 8000,
        cacheCreationInputTokens: 2200,
        inputCost: 4,
        outputCost: 2,
        cacheCreationCost: 0,
        cacheReadCost: 2.2509,
      },
    });

    render(<ChatPage />);

    await waitFor(() => expect(mockGetConversation).toHaveBeenCalledWith('conv-123'));

    const meta = await screen.findByTestId('transcript-meta-strip');
    expect(meta).toHaveAttribute('aria-label', 'Conversation statistics');
    expect(meta).toHaveTextContent('ctx 5%');
    expect(meta).toHaveTextContent('in 1.2K');
    expect(meta).toHaveTextContent('out 340');
    expect(meta).toHaveTextContent('cache 8K');
    expect(meta).toHaveTextContent('cache write 2.2K');
    expect(meta).toHaveTextContent('$8.25');
    expect(meta.textContent).toContain('ctx 5% · in 1.2K · out 340 · cache 8K · cache write 2.2K');
    expect(meta.textContent).toMatch(/\d+m ago|just now/);

    const details = screen.getByTestId('transcript-meta-details');
    expect(meta.closest('details')).not.toHaveAttribute('open');
    expect(details).not.toBeVisible();
    expect(meta).not.toHaveTextContent('$8.2509');
    fireEvent.click(meta);
    expect(details).toBeVisible();
    expect(details).toHaveTextContent('14,200 / 272,000 tokens (5%)');
    expect(details).toHaveTextContent('1,200');
    expect(details).toHaveTextContent('8,000');
    expect(details).toHaveTextContent('2,200');
    expect(details).toHaveTextContent('$8.2509');
    fireEvent.click(meta);
    expect(details).not.toBeVisible();
  });

  it('updates relative conversation time without reloading the conversation', async () => {
    vi.useFakeTimers();
    vi.setSystemTime(new Date('2026-09-12T12:00:00Z'));
    setRouteParams({ id: 'conv-123' });
    mockGetConversation.mockResolvedValue({
      id: 'conv-123',
      updatedAt: '2026-09-12T11:58:30Z',
      messages: [{ role: 'user', content: 'hello' }],
      toolResults: {},
    });

    try {
      render(<ChatPage />);
      await flushAsyncUpdates();
      expect(screen.getByTestId('transcript-meta-strip')).toHaveTextContent('1m ago');

      await act(async () => {
        vi.advanceTimersByTime(30000);
      });
      expect(screen.getByTestId('transcript-meta-strip')).toHaveTextContent('2m ago');
      expect(mockGetConversation).toHaveBeenCalledTimes(1);
    } finally {
      vi.useRealTimers();
    }
  });

  it('updates compact usage metadata when a streamed usage event arrives', async () => {
    setRouteParams({ id: 'conv-123' });
    mockGetConversation.mockResolvedValue({
      id: 'conv-123',
      createdAt: '2023-01-02T11:00:00Z',
      updatedAt: '2023-01-02T11:05:00Z',
      messageCount: 1,
      messages: [
        {
          role: 'user',
          content: 'hello',
        },
      ],
      toolResults: {},
      usage: {
        currentContextWindow: 1000,
        maxContextWindow: 272000,
        inputTokens: 100,
        outputTokens: 20,
        inputCost: 0,
        outputCost: 0,
        cacheCreationCost: 0,
        cacheReadCost: 0,
      },
    });

    const streamListeners: Array<(event: ChatStreamEvent) => void> = [];
    mockStreamConversation.mockImplementation(async (_id, options) => {
      streamListeners.push((options as { onEvent: (event: ChatStreamEvent) => void }).onEvent);
      return new Promise(() => undefined);
    });

    render(<ChatPage />);

    await waitFor(() => expect(mockGetConversation).toHaveBeenCalledWith('conv-123'));
    await waitFor(() =>
      expect(mockStreamConversation).toHaveBeenCalledWith('conv-123', expect.any(Object))
    );
    expect(streamListeners).toHaveLength(1);

    expect(screen.getByTestId('transcript-meta-strip')).toHaveTextContent('in 100');
    expect(screen.getByTestId('transcript-meta-strip')).toHaveTextContent('$0.00');
    fireEvent.click(screen.getByTestId('transcript-meta-strip'));
    expect(screen.getByTestId('transcript-meta-details')).toBeVisible();

    await act(async () => {
      streamListeners[0]?.({
        kind: 'usage',
        conversation_id: 'conv-123',
        usage: {
          currentContextWindow: 2400,
          maxContextWindow: 272000,
          inputTokens: 100,
          outputTokens: 140,
          cacheReadInputTokens: 50,
          inputCost: 0.0001,
          outputCost: 0.0002,
          cacheCreationCost: 0,
          cacheReadCost: 0,
        },
      });
    });

    await waitFor(() => {
      const meta = screen.getByTestId('transcript-meta-strip');
      expect(meta).toHaveTextContent('ctx 1%');
      expect(meta).toHaveTextContent('in 100');
      expect(meta).toHaveTextContent('out 140');
      expect(meta).toHaveTextContent('cache 50');
      expect(meta).toHaveTextContent('<$0.01');
      const details = screen.getByTestId('transcript-meta-details');
      expect(details).toBeVisible();
      expect(details).toHaveTextContent('2,400 / 272,000 tokens (1%)');
      expect(details).toHaveTextContent('$0.0003');
    });

    expect(mockStreamConversation).toHaveBeenCalledTimes(1);

    await act(async () => {
      streamListeners[0]?.({
        kind: 'text-delta',
        conversation_id: 'conv-123',
        delta: 'stream continues',
      });
    });

    expect(screen.getByText('stream continues')).toBeInTheDocument();
  });

  it('only auto-scrolls streamed updates while the transcript is at the bottom', async () => {
    setRouteParams({ id: 'conv-123' });

    mockGetConversation.mockResolvedValue({
      id: 'conv-123',
      createdAt: '2024-01-01T00:00:00Z',
      updatedAt: '2024-01-01T00:00:00Z',
      messageCount: 1,
      messages: [
        {
          role: 'user',
          content: 'Existing conversation',
        },
      ],
      toolResults: {},
    });

    let streamListener: ((event: ChatStreamEvent) => void) | null = null;
    mockStreamConversation.mockImplementation(async (_id, options) => {
      streamListener = (options as { onEvent: (event: ChatStreamEvent) => void }).onEvent;
      return new Promise(() => undefined);
    });

    render(<ChatPage />);

    await waitFor(() => expect(streamListener).not.toBeNull());

    const scrollIntoView = vi.mocked(window.HTMLElement.prototype.scrollIntoView);
    scrollIntoView.mockClear();

    const transcriptScroll = screen.getByTestId('chat-transcript-scroll');
    Object.defineProperties(transcriptScroll, {
      clientHeight: { configurable: true, value: 500 },
      scrollHeight: { configurable: true, value: 1500 },
      scrollTop: { configurable: true, value: 200 },
    });

    fireEvent.scroll(transcriptScroll);

    await act(async () => {
      streamListener?.({
        kind: 'text-delta',
        conversation_id: 'conv-123',
        delta: 'while reading earlier content',
      });
    });

    expect(screen.getByText('while reading earlier content')).toBeInTheDocument();
    expect(scrollIntoView).not.toHaveBeenCalled();

    Object.defineProperty(transcriptScroll, 'scrollTop', {
      configurable: true,
      value: 1000,
    });
    fireEvent.scroll(transcriptScroll);

    await act(async () => {
      streamListener?.({
        kind: 'text-delta',
        conversation_id: 'conv-123',
        delta: ' after returning to bottom',
      });
    });

    expect(scrollIntoView).toHaveBeenCalledWith({
      behavior: 'smooth',
      block: 'end',
    });
  });
});
