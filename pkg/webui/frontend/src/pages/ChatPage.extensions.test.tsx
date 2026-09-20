import { act, fireEvent, render, screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { describe, expect, it, vi } from 'vitest';
import type { ChatStreamEvent, UIWidgetEvent } from '../types';
import {
  ChatPage,
  mockGetConversation,
  mockGetConversations,
  mockNavigate,
  mockRespondToUIInput,
  mockStopConversation,
  mockStreamChat,
  mockStreamConversation,
  renderChatWithRunner,
  setRouteParams,
  setupChatPageTests,
  waitForTerminalAccess,
} from './ChatPage.testSupport';

describe('ChatPage extension widgets and UI requests', () => {
  setupChatPageTests();

  it.each([
    'aboveComposer',
    'belowComposer',
  ])('restores %s extension widgets folded by default and preserves fold state', async (placement) => {
    setRouteParams({ id: 'conv-123' });
    mockGetConversation.mockResolvedValue({
      id: 'conv-123',
      createdAt: '2026-08-29T00:00:00Z',
      updatedAt: '2026-08-29T00:00:00Z',
      messageCount: 1,
      messages: [],
      toolResults: {},
    });
    let streamListener: ((event: ChatStreamEvent) => void) | null = null;
    mockStreamConversation.mockImplementation(async (_id, options) => {
      streamListener = (options as { onEvent: (event: ChatStreamEvent) => void }).onEvent;
      return new Promise(() => undefined);
    });
    const widget: UIWidgetEvent = {
      key: 'subagent-widget',
      extension_id: 'subagent',
      id: 'background-agents',
      placement,
      frame: {
        sequence: 4,
        lines: [
          {
            spans: [
              { text: 'Background agents', style: { bold: true } },
              { text: '  1 active', style: { dim: true } },
            ],
          },
          {
            spans: [
              { text: '● Inspect authentication', style: { foreground: 'cyan' } },
              { text: '  running', style: { foreground: '#179299', italic: true } },
            ],
          },
        ],
      },
    };

    render(<ChatPage />);

    await waitFor(() => expect(streamListener).not.toBeNull());
    await act(async () => {
      streamListener?.({
        kind: 'ui-widgets',
        conversation_id: 'conv-123',
        ui_widgets: [widget],
      });
    });

    expect(screen.getByTestId(`extension-widgets-${placement}`)).toBeInTheDocument();
    expect(screen.getByTestId('extension-widget-subagent-widget')).toHaveClass(
      'extension-widget-frame'
    );
    expect(screen.getByText(/Background agents/).closest('.extension-widget-line')).toHaveClass(
      'extension-widget-line-header'
    );
    const widgetToggle = screen.getByRole('button', { name: /Background agents/ });
    expect(widgetToggle).toHaveAttribute('aria-expanded', 'false');
    expect(screen.queryByText(/Inspect authentication/)).not.toBeInTheDocument();
    fireEvent.click(widgetToggle);
    expect(widgetToggle).toHaveAttribute('aria-expanded', 'true');
    expect(screen.getByText(/Inspect authentication/)).toHaveStyle({ color: 'var(--tui-teal)' });
    expect(screen.getByText('running')).toHaveStyle({ color: '#179299', fontStyle: 'italic' });
    expect(document.getElementById(widgetToggle.getAttribute('aria-controls') || '')).toHaveClass(
      'extension-widget-content'
    );

    for (const expanded of [true, false]) {
      if (!expanded) {
        widgetToggle.focus();
        await userEvent.keyboard('{Enter}');
      }
      const detail = expanded ? 'Expanded widget update' : 'Collapsed widget update';
      await act(async () => {
        streamListener?.({
          kind: 'ui-widget',
          conversation_id: 'conv-123',
          ui_widget: {
            ...widget,
            frame: {
              sequence: expanded ? 5 : 6,
              lines: [widget.frame.lines[0], detail],
            },
          },
        });
      });

      expect(widgetToggle).toHaveAttribute('aria-expanded', String(expanded));
      if (expanded) {
        expect(screen.getByText(detail)).toBeVisible();
      } else {
        expect(screen.queryByText(detail)).not.toBeInTheDocument();
      }
    }
    await userEvent.keyboard('{Enter}');
    expect(widgetToggle).toHaveAttribute('aria-expanded', 'true');
    expect(screen.getByText('Collapsed widget update')).toBeVisible();

    await act(async () => {
      streamListener?.({
        kind: 'ui-widget',
        conversation_id: 'conv-123',
        ui_widget: {
          key: 'subagent-widget',
          extension_id: 'subagent',
          generation: '0:2',
          id: 'background-agents',
          placement,
          frame: { sequence: 1, lines: ['Restarted agent generation'] },
        },
      });
      streamListener?.({
        kind: 'ui-widget',
        conversation_id: 'conv-123',
        ui_widget: {
          key: 'subagent-widget',
          extension_id: 'subagent',
          generation: '0:1',
          id: 'background-agents',
          placement,
          frame: { sequence: 5, lines: [] },
          removed: true,
        },
      });
    });

    expect(screen.getByText('Restarted agent generation')).toBeInTheDocument();
    expect(
      screen.queryByRole('button', { name: 'Restarted agent generation' })
    ).not.toBeInTheDocument();
    expect(
      screen
        .getByTestId('extension-widget-subagent-widget')
        .querySelector('.extension-widget-content')
    ).toBeNull();

    await act(async () => {
      streamListener?.({
        kind: 'ui-widget',
        conversation_id: 'conv-123',
        ui_widget: {
          key: 'subagent-widget',
          extension_id: 'subagent',
          generation: '0:2',
          id: 'background-agents',
          placement,
          frame: { sequence: 3, lines: [] },
          removed: true,
        },
      });
      streamListener?.({
        kind: 'ui-widget',
        conversation_id: 'conv-123',
        ui_widget: {
          key: 'subagent-widget',
          extension_id: 'subagent',
          generation: '0:2',
          id: 'background-agents',
          placement,
          frame: { sequence: 2, lines: ['Delayed stale update'] },
        },
      });
    });

    expect(screen.queryByText('Restarted agent generation')).not.toBeInTheDocument();
    expect(screen.queryByText('Delayed stale update')).not.toBeInTheDocument();
  });

  it('keeps a background-agent widget visible after the parent turn completes', async () => {
    setRouteParams({ id: 'conv-123' });
    const streamListeners: Array<(event: ChatStreamEvent) => void> = [];
    mockGetConversation.mockResolvedValue({
      runnerId: 'runner-1',
      id: 'conv-123',
      createdAt: '2026-08-29T00:00:00Z',
      updatedAt: '2026-08-29T00:00:00Z',
      messageCount: 1,
      messages: [],
      toolResults: {},
    });
    mockStreamConversation.mockImplementation(async (_id, options) => {
      streamListeners.push((options as { onEvent: (event: ChatStreamEvent) => void }).onEvent);
      return new Promise(() => undefined);
    });
    mockStreamChat.mockImplementation(async (_request, options) => {
      const onEvent = (options as { onEvent: (event: ChatStreamEvent) => void }).onEvent;
      onEvent({
        kind: 'ui-widget',
        conversation_id: 'conv-123',
        ui_widget_revision: '100:1',
        ui_widget: {
          key: 'subagent-widget',
          extension_id: 'subagent',
          id: 'background-agents',
          placement: 'aboveComposer',
          frame: { sequence: 1, lines: ['Background agents  1 active'] },
        },
      });
      onEvent({ kind: 'done', conversation_id: 'conv-123' });
    });

    render(<ChatPage />);
    await waitFor(() => expect(mockGetConversation).toHaveBeenCalledWith('conv-123'));
    fireEvent.change(await screen.findByPlaceholderText('Ask kodelet anything...'), {
      target: { value: 'delegate this task' },
    });
    fireEvent.click(screen.getByRole('button', { name: 'Send' }));

    await waitFor(() => expect(mockStreamChat).toHaveBeenCalled());
    await waitFor(() =>
      expect(screen.getByTestId('extension-widgets-aboveComposer')).toHaveTextContent(
        'Background agents 1 active'
      )
    );
    expect(screen.queryByRole('button', { name: 'Stop' })).not.toBeInTheDocument();
    await waitFor(() => expect(mockStreamConversation.mock.calls.length).toBeGreaterThanOrEqual(2));

    await act(async () => {
      streamListeners[streamListeners.length - 1]?.({
        kind: 'ui-widgets',
        conversation_id: 'conv-123',
        ui_widget_revision: '100:0',
        ui_widgets: [],
      });
    });

    expect(screen.getByTestId('extension-widgets-aboveComposer')).toHaveTextContent(
      'Background agents 1 active'
    );

    await act(async () => {
      streamListeners[streamListeners.length - 1]?.({
        kind: 'ui-widgets',
        conversation_id: 'conv-123',
        ui_widget_revision: '100:2',
        ui_widgets: [],
      });
    });

    expect(screen.queryByTestId('extension-widgets-aboveComposer')).not.toBeInTheDocument();

    await act(async () => {
      streamListeners[streamListeners.length - 1]?.({
        kind: 'ui-widget',
        conversation_id: 'conv-123',
        ui_widget_revision: '100:1',
        ui_widget: {
          key: 'subagent-widget',
          extension_id: 'subagent',
          id: 'background-agents',
          placement: 'aboveComposer',
          frame: { sequence: 1, lines: ['Delayed stale widget'] },
        },
      });
    });

    expect(screen.queryByText('Delayed stale widget')).not.toBeInTheDocument();
  });

  it('closes conversation search when a blocking UI request arrives', async () => {
    mockGetConversations.mockResolvedValue({
      conversations: [
        {
          id: 'conv-123',
          createdAt: '2024-01-01T00:00:00Z',
          updatedAt: '2024-01-03T00:00:00Z',
          messageCount: 1,
          summary: 'Running task',
          isRunning: true,
        },
      ],
      hasMore: false,
      total: 1,
      limit: 40,
      offset: 0,
    });
    let streamOptions: { onEvent: (event: ChatStreamEvent) => void } | null = null;
    mockStreamConversation.mockImplementation(
      async (_conversationId, options) =>
        new Promise<void>(() => {
          streamOptions = options as {
            onEvent: (event: ChatStreamEvent) => void;
          };
        })
    );

    render(<ChatPage />);

    await waitFor(() => expect(streamOptions).not.toBeNull());
    fireEvent.click(screen.getByTestId('sidebar-search-toggle'));
    expect(screen.getByRole('dialog', { name: 'Search conversations' })).toBeInTheDocument();

    await act(async () => {
      streamOptions?.onEvent({
        kind: 'ui-input-request',
        conversation_id: 'conv-123',
        ui_input: {
          id: 'input-1',
          title: 'Need input',
          message: 'Answer for the running task',
        },
      });
    });

    expect(screen.queryByTestId('conversation-search-dialog')).not.toBeInTheDocument();
    expect(screen.getByTestId('ui-input-dialog')).toBeInTheDocument();
    await waitFor(() => expect(screen.getByTestId('ui-input-response')).toHaveFocus());

    await act(async () => {
      streamOptions?.onEvent({
        kind: 'ui-request-end',
        conversation_id: 'other-conversation',
        ui_request_id: 'input-1',
      });
    });
    expect(screen.getByTestId('ui-input-dialog')).toBeInTheDocument();
    await act(async () => {
      streamOptions?.onEvent({
        kind: 'ui-request-end',
        conversation_id: 'conv-123',
        ui_request_id: 'old-input',
      });
    });
    expect(screen.getByTestId('ui-input-dialog')).toBeInTheDocument();
    await act(async () => {
      streamOptions?.onEvent({
        kind: 'ui-request-end',
        conversation_id: 'conv-123',
        ui_request_id: 'input-1',
      });
    });
    expect(screen.queryByTestId('ui-input-dialog')).not.toBeInTheDocument();
    expect(mockRespondToUIInput).not.toHaveBeenCalled();
    expect(mockStopConversation).not.toHaveBeenCalled();
  });

  it('observes a running conversation without offering UI takeover', async () => {
    setRouteParams({ id: 'conv-123' });
    mockGetConversations.mockResolvedValue({
      conversations: [
        {
          id: 'conv-123',
          createdAt: '2024-01-01T00:00:00Z',
          updatedAt: '2024-01-03T00:00:00Z',
          messageCount: 1,
          summary: 'Running task',
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
      messages: [],
      toolResults: {},
      isRunning: true,
    });
    mockStreamConversation.mockImplementation(async () => new Promise<void>(() => {}));
    render(<ChatPage />);
    await waitFor(() => expect(mockStreamConversation).toHaveBeenCalled());
    expect(screen.getByTestId('conversation-running-indicator-conv-123')).toBeInTheDocument();
    expect(screen.queryByRole('button', { name: 'Take control' })).not.toBeInTheDocument();
    expect(screen.queryByTestId('ui-input-dialog')).not.toBeInTheDocument();
    expect(mockRespondToUIInput).not.toHaveBeenCalled();
    expect(mockStopConversation).not.toHaveBeenCalled();
  });

  it('shows blocking UI prompts from background running conversations', async () => {
    vi.stubGlobal(
      'matchMedia',
      vi.fn().mockImplementation((query: string) => ({
        matches: query === '(max-width: 1023px)' || query === '(max-width: 1180px)',
        media: query,
        onchange: null,
        addEventListener: vi.fn(),
        removeEventListener: vi.fn(),
        addListener: vi.fn(),
        removeListener: vi.fn(),
        dispatchEvent: vi.fn(),
      }))
    );
    mockGetConversations.mockResolvedValue({
      conversations: [
        {
          id: 'conv-123',
          createdAt: '2024-01-01T00:00:00Z',
          updatedAt: '2024-01-03T00:00:00Z',
          messageCount: 1,
          summary: 'Running task',
          isRunning: true,
        },
        {
          id: 'conv-456',
          createdAt: '2024-01-01T00:00:00Z',
          updatedAt: '2024-01-02T00:00:00Z',
          messageCount: 1,
          summary: 'Current task',
        },
      ],
      hasMore: false,
      total: 2,
      limit: 40,
      offset: 0,
    });
    mockGetConversation.mockResolvedValue({
      runnerId: 'runner-1',
      id: 'conv-456',
      createdAt: '2024-01-01T00:00:00Z',
      updatedAt: '2024-01-01T00:00:00Z',
      messageCount: 1,
      summary: 'Current task',
      messages: [],
      toolResults: {},
    });

    let streamOptions: { onEvent: (event: ChatStreamEvent) => void } | null = null;
    mockStreamConversation.mockImplementation(
      async (conversationId, options) =>
        new Promise<void>(() => {
          if (conversationId === 'conv-123') {
            streamOptions = options as {
              onEvent: (event: ChatStreamEvent) => void;
            };
          }
        })
    );
    mockRespondToUIInput.mockResolvedValue({ success: true });

    setRouteParams({ id: 'conv-456' });
    render(<ChatPage />);

    await waitFor(() =>
      expect(mockStreamConversation).toHaveBeenCalledWith('conv-123', expect.any(Object))
    );
    await waitForTerminalAccess();
    fireEvent.click(screen.getByTestId('workspace-tools-toggle'));
    await screen.findByTestId('terminal-panel');

    await act(async () => {
      streamOptions?.onEvent({
        kind: 'ui-input-request',
        conversation_id: 'conv-123',
        ui_input: {
          id: 'input-1',
          title: 'Need background input',
          message: 'Answer for background run',
        },
      });
    });

    expect(screen.getByTestId('ui-input-dialog')).toBeInTheDocument();
    expect(screen.getByText('Need background input')).toBeInTheDocument();
    expect(screen.getByTestId('chat-layout')).toHaveAttribute('inert');
    await waitFor(() => expect(screen.getByTestId('ui-input-response')).toHaveFocus());
    fireEvent.keyDown(screen.getByTestId('ui-input-response'), { key: 'Tab' });
    expect(screen.getByTestId('ui-input-response')).toHaveFocus();

    fireEvent.change(screen.getByTestId('ui-input-response'), {
      target: { value: 'yes' },
    });
    fireEvent.click(screen.getByRole('button', { name: 'Submit' }));

    await waitFor(() =>
      expect(mockRespondToUIInput).toHaveBeenCalledWith('conv-123', 'input-1', {
        status: 'submitted',
        value: 'yes',
      })
    );
  });

  it('shows blocking UI prompts from a resumed stream after switching conversations', async () => {
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
          summary: 'Other task',
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
      summary: id === 'conv-456' ? 'Other task' : 'Running task',
      messages: [],
      toolResults: {},
    }));

    let streamOptions: { onEvent: (event: ChatStreamEvent) => void } | null = null;
    mockStreamConversation.mockImplementation(
      async (conversationId, options) =>
        new Promise<void>(() => {
          if (conversationId === 'conv-123') {
            streamOptions = options as {
              onEvent: (event: ChatStreamEvent) => void;
            };
          }
        })
    );
    mockRespondToUIInput.mockResolvedValue({ success: true });

    setRouteParams({ id: 'conv-123' });
    const { rerender } = render(<ChatPage />);

    await waitFor(() =>
      expect(mockStreamConversation).toHaveBeenCalledWith('conv-123', expect.any(Object))
    );

    fireEvent.click(screen.getByText('Other task'));
    expect(mockNavigate).toHaveBeenCalledWith('/c/conv-456');

    setRouteParams({ id: 'conv-456' });
    rerender(<ChatPage />);

    await waitFor(() => expect(mockGetConversation).toHaveBeenCalledWith('conv-456'));

    await act(async () => {
      streamOptions?.onEvent({
        kind: 'ui-input-request',
        conversation_id: 'conv-123',
        ui_input: {
          id: 'input-1',
          title: 'Need resumed input',
          message: 'Answer for resumed run',
        },
      });
    });

    expect(screen.getByTestId('ui-input-dialog')).toBeInTheDocument();
    expect(screen.getByText('Need resumed input')).toBeInTheDocument();

    fireEvent.change(screen.getByTestId('ui-input-response'), {
      target: { value: 'ok' },
    });
    fireEvent.click(screen.getByRole('button', { name: 'Submit' }));

    await waitFor(() =>
      expect(mockRespondToUIInput).toHaveBeenCalledWith('conv-123', 'input-1', {
        status: 'submitted',
        value: 'ok',
      })
    );
  });

  it('shows blocking UI prompts from a submitted stream after switching conversations', async () => {
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
          summary: 'Other task',
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
      summary: id === 'conv-456' ? 'Other task' : 'Running task',
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

    fireEvent.click(screen.getByText('Other task'));
    expect(mockNavigate).toHaveBeenCalledWith('/c/conv-456');

    setRouteParams({ id: 'conv-456' });
    rerender(<ChatPage />);

    await waitFor(() => expect(mockGetConversation).toHaveBeenCalledWith('conv-456'));

    await act(async () => {
      streamOptions?.onEvent({
        kind: 'ui-input-request',
        conversation_id: 'conv-123',
        ui_input: {
          id: 'input-1',
          title: 'Need switched input',
          message: 'Answer for switched run',
        },
      });
    });

    expect(screen.getByTestId('ui-input-dialog')).toBeInTheDocument();
    expect(screen.getByText('Need switched input')).toBeInTheDocument();

    fireEvent.change(screen.getByTestId('ui-input-response'), {
      target: { value: 'ok' },
    });
    fireEvent.click(screen.getByRole('button', { name: 'Submit' }));

    await waitFor(() =>
      expect(mockRespondToUIInput).toHaveBeenCalledWith('conv-123', 'input-1', {
        status: 'submitted',
        value: 'ok',
      })
    );
  });
});
