import { act, fireEvent, render, screen, waitFor } from '@testing-library/react';
import { describe, expect, it } from 'vitest';
import type { ChatStreamEvent } from '../types';
import {
  ChatPage,
  flushAsyncUpdates,
  makeRunner,
  mockGetChatSettings,
  mockGetConversation,
  mockGetConversations,
  mockGetCWDHints,
  mockGetGitDiff,
  mockGetRunners,
  mockNavigate,
  mockStopConversation,
  mockStreamChat,
  renderChatWithRunner,
  selectWorkspaceRunner,
  setRouteParams,
  setupChatPageTests,
  startOptimisticRemoteConversation,
  waitForTerminalAccess,
} from './ChatPage.testSupport';

describe('ChatPage optimistic conversations and route handoff', () => {
  setupChatPageTests();

  it('keeps the draft browser through submission, route handoff and later turns without changing workspace targets', async () => {
    const runner = makeRunner({
      workspaceGitDiff: true,
      workspaceTerminal: true,
      workspaceBrowser: true,
    });
    mockGetRunners.mockResolvedValue({
      runners: [runner],
    });
    let finishStream = () => {};
    mockStreamChat.mockImplementation(
      () =>
        new Promise<void>((resolve) => {
          finishStream = resolve;
        })
    );
    mockGetConversation.mockImplementation(async (id: string) => ({
      id,
      createdAt: '2026-09-14T00:00:00Z',
      updatedAt: '2026-09-14T00:00:00Z',
      messageCount: 1,
      cwd: '/runner/kodelet',
      runnerId: runner.id,
      runner,
      messages: [{ role: 'user', content: 'hello remotely' }],
      toolResults: {},
    }));

    const { rerender } = await renderChatWithRunner();
    await waitForTerminalAccess();
    fireEvent.click(screen.getByTestId('workspace-tools-toggle'));
    fireEvent.click(screen.getByRole('tab', { name: 'Show browser' }));
    const draftBrowser = await screen.findByTestId('browser-panel');
    const draftId = draftBrowser.dataset.conversationId;
    expect(draftId).toMatch(/^\d{8}T\d{6}-[a-f0-9]{16}$/);
    fireEvent.change(screen.getByPlaceholderText('Ask kodelet anything...'), {
      target: { value: 'hello remotely' },
    });
    fireEvent.click(screen.getByRole('button', { name: 'Send' }));

    await waitFor(() => expect(mockStreamChat).toHaveBeenCalled());
    const preallocatedId = mockStreamChat.mock.calls[0]?.[0]?.conversationId;
    expect(preallocatedId).toBe(draftId);
    expect(screen.getByTestId('browser-panel')).toBe(draftBrowser);
    expect(screen.getByRole('button', { name: 'Stop' })).toBeEnabled();
    expect(mockNavigate).toHaveBeenCalledWith(`/c/${draftId}`, { replace: true });
    setRouteParams({ id: preallocatedId });
    rerender(<ChatPage />);
    expect(screen.getByTestId('browser-panel')).toBe(draftBrowser);

    fireEvent.click(screen.getByRole('tab', { name: 'Show terminal' }));
    const terminal = await screen.findByTestId('terminal-panel');
    expect(terminal).not.toHaveAttribute('data-conversation-id');
    expect(terminal).toHaveAttribute('data-runner-id', 'runner-1');
    fireEvent.click(screen.getByTestId('workspace-tools-diff-tab'));
    await waitFor(() =>
      expect(mockGetGitDiff).toHaveBeenCalledWith({
        kind: 'runner',
        runnerId: 'runner-1',
      })
    );
    expect(mockGetConversation).not.toHaveBeenCalledWith(preallocatedId);

    fireEvent.click(screen.getByRole('tab', { name: 'Show browser' }));
    const pendingBrowser = await screen.findByTestId('browser-panel');
    expect(pendingBrowser).toHaveAttribute('data-conversation-id', draftId);
    await act(async () => finishStream());
    await waitFor(() => expect(mockGetConversation).toHaveBeenCalledWith(draftId));
    expect(screen.getByTestId('browser-panel')).toBe(pendingBrowser);

    fireEvent.change(screen.getByPlaceholderText('Ask kodelet anything...'), {
      target: { value: 'second turn' },
    });
    fireEvent.click(screen.getByRole('button', { name: 'Send' }));
    await waitFor(() => expect(mockStreamChat).toHaveBeenCalledTimes(2));
    expect(mockStreamChat.mock.calls[1]?.[0]?.conversationId).toBe(draftId);
    expect(screen.getByTestId('browser-panel')).toBe(pendingBrowser);
    expect(mockStopConversation).not.toHaveBeenCalled();

    await act(async () => finishStream());
    setRouteParams({});
    rerender(<ChatPage />);
    const newDraftBrowser = await screen.findByTestId('browser-panel');
    expect(newDraftBrowser).not.toBe(pendingBrowser);
    expect(newDraftBrowser.dataset.conversationId).not.toBe(draftId);
    expect(newDraftBrowser.dataset.conversationId).toMatch(/^\d{8}T\d{6}-[a-f0-9]{16}$/);
  });

  it('allows correcting runner context before retrying a failed optimistic conversation', async () => {
    mockStreamChat
      .mockRejectedValueOnce(new Error('runner unavailable'))
      .mockImplementationOnce(async () => new Promise(() => undefined));

    const preallocatedId = await startOptimisticRemoteConversation({
      runner: makeRunner({ workspaceGitDiff: true, workspaceTerminal: true }),
    });
    await waitFor(() =>
      expect(screen.getAllByText('runner unavailable').length).toBeGreaterThan(0)
    );
    expect(mockGetConversation).not.toHaveBeenCalledWith(preallocatedId);

    const row = screen.getByTestId(`conversation-row-${preallocatedId}`);
    const listRequests = mockGetConversations.mock.calls.length;
    fireEvent(document, new Event('visibilitychange'));
    await flushAsyncUpdates();
    expect(mockGetConversations).toHaveBeenCalledTimes(listRequests + 1);
    expect(screen.getByTestId(`conversation-row-${preallocatedId}`)).toBe(row);

    fireEvent.click(screen.getByRole('button', { name: /^Change workspace:/ }));
    await screen.findByTestId('new-chat-dialog');
    fireEvent.change(screen.getByLabelText('Working directory'), {
      target: { value: '../corrected-project' },
    });
    await flushAsyncUpdates();
    fireEvent.click(screen.getByRole('button', { name: 'Start' }));

    fireEvent.change(screen.getByPlaceholderText('Ask kodelet anything...'), {
      target: { value: 'retry' },
    });
    fireEvent.click(screen.getByRole('button', { name: 'Send' }));

    await waitFor(() => expect(mockStreamChat).toHaveBeenCalledTimes(2));
    expect(mockStreamChat.mock.calls[1]?.[0]).toEqual(
      expect.objectContaining({
        conversationId: preallocatedId,
        runnerId: 'runner-1',
        cwd: '../corrected-project',
      })
    );
    const retryRow = screen.getByTestId(`conversation-row-${preallocatedId}`);
    fireEvent(document, new Event('visibilitychange'));
    await flushAsyncUpdates();
    expect(screen.getByTestId(`conversation-row-${preallocatedId}`)).toBe(retryRow);
    expect(
      screen.getByTestId(`conversation-running-indicator-${preallocatedId}`)
    ).toBeInTheDocument();
  });

  it('locks optimistic context after the server confirms the conversation', async () => {
    let streamOptions: { onEvent: (event: ChatStreamEvent) => void } | undefined;
    mockStreamChat.mockImplementation(async (_request, options) => {
      streamOptions = options as { onEvent: (event: ChatStreamEvent) => void };
      return new Promise(() => undefined);
    });

    const preallocatedId = await startOptimisticRemoteConversation({
      message: 'confirm context',
    });

    await act(async () => {
      streamOptions?.onEvent({
        kind: 'conversation',
        conversation_id: preallocatedId,
        cwd: '/runner/canonical-project',
      });
    });

    expect(screen.getByTestId('chat-workspace-header')).toHaveTextContent(
      '/runner/canonical-project'
    );
    expect(screen.getByTestId('composer-inline-context')).toHaveTextContent(/^medium$/);
    expect(screen.queryByTestId('composer-context-button')).not.toBeInTheDocument();
    expect(screen.queryByRole('button', { name: /^Change workspace:/ })).not.toBeInTheDocument();
  });

  it('keeps optimistic context editable until the server supplies a canonical cwd', async () => {
    let streamOptions: { onEvent: (event: ChatStreamEvent) => void } | undefined;
    mockStreamChat.mockImplementation(async (_request, options) => {
      streamOptions = options as { onEvent: (event: ChatStreamEvent) => void };
      return new Promise(() => undefined);
    });

    const preallocatedId = await startOptimisticRemoteConversation({
      message: 'await canonical cwd',
    });

    await act(async () => {
      streamOptions?.onEvent({
        kind: 'conversation',
        conversation_id: preallocatedId,
      });
    });

    expect(screen.getByTestId('composer-context-button')).toBeInTheDocument();
    expect(screen.getByRole('button', { name: /^Change workspace:/ })).toBeInTheDocument();
  });

  it('clears confirmed optimistic parameters when the stream later reports an error', async () => {
    mockGetConversation.mockRejectedValue(new Error('conversation refresh failed'));
    mockStreamChat
      .mockImplementationOnce(async (request, options) => {
        const streamRequest = request as { conversationId: string };
        const streamOptions = options as { onEvent: (event: ChatStreamEvent) => void };
        streamOptions.onEvent({
          kind: 'conversation',
          conversation_id: streamRequest.conversationId,
          cwd: '/runner/canonical-project',
        });
        streamOptions.onEvent({
          kind: 'error',
          conversation_id: streamRequest.conversationId,
          error: 'model failed after opening the conversation',
        });
      })
      .mockImplementationOnce(async () => new Promise(() => undefined));

    const conversationId = await startOptimisticRemoteConversation();
    await waitFor(() =>
      expect(
        screen.getAllByText('model failed after opening the conversation').length
      ).toBeGreaterThan(0)
    );
    await waitFor(() => expect(mockGetConversation).toHaveBeenCalledWith(conversationId));

    fireEvent.change(screen.getByPlaceholderText('Ask kodelet anything...'), {
      target: { value: 'retry established conversation' },
    });
    fireEvent.click(screen.getByRole('button', { name: 'Send' }));

    await waitFor(() => expect(mockStreamChat).toHaveBeenCalledTimes(2));
    expect(mockStreamChat.mock.calls[1]?.[0]).toEqual(expect.objectContaining({ conversationId }));
    expect(mockStreamChat.mock.calls[1]?.[0]?.runnerId).toBeUndefined();
    expect(mockStreamChat.mock.calls[1]?.[0]?.environmentProfile).toBeUndefined();
    expect(mockStreamChat.mock.calls[1]?.[0]?.cwd).toBeUndefined();
  });

  it('loads a confirmed conversation normally when the stream later rejects', async () => {
    mockGetConversation.mockRejectedValue(new Error('conversation refresh failed'));
    mockStreamChat.mockImplementationOnce(async (request, options) => {
      const streamRequest = request as { conversationId: string };
      const streamOptions = options as { onEvent: (event: ChatStreamEvent) => void };
      streamOptions.onEvent({
        kind: 'conversation',
        conversation_id: streamRequest.conversationId,
        cwd: '/runner/canonical-project',
      });
      throw new Error('connection lost after confirmation');
    });

    const conversationId = await startOptimisticRemoteConversation();
    await waitFor(() =>
      expect(screen.getAllByText('connection lost after confirmation').length).toBeGreaterThan(0)
    );

    await waitFor(() => expect(mockGetConversation).toHaveBeenCalledWith(conversationId));
  });

  it('clears optimistic runner parameters after a successful stream even when refresh fails', async () => {
    mockGetRunners.mockResolvedValue({
      runners: [makeRunner({ workspaceGitDiff: true, workspaceTerminal: true })],
    });
    mockStreamChat.mockResolvedValue(undefined);
    mockGetConversation.mockRejectedValue(new Error('conversation refresh failed'));

    const { rerender } = render(<ChatPage />);
    await waitFor(() => expect(mockGetRunners).toHaveBeenCalled());
    await waitForTerminalAccess();
    fireEvent.click(screen.getByTestId('sidebar-new-chat-button'));
    selectWorkspaceRunner();
    await flushAsyncUpdates();
    fireEvent.click(screen.getByRole('button', { name: 'Start' }));
    fireEvent.change(screen.getByPlaceholderText('Ask kodelet anything...'), {
      target: { value: 'first message' },
    });
    fireEvent.click(screen.getByRole('button', { name: 'Send' }));

    await waitFor(() => expect(mockStreamChat).toHaveBeenCalledTimes(1));
    const conversationId = mockStreamChat.mock.calls[0]?.[0]?.conversationId;
    expect(conversationId).toBeTruthy();
    await waitFor(() => expect(mockGetConversation).toHaveBeenCalledWith(conversationId));

    setRouteParams({ id: conversationId });
    rerender(<ChatPage />);
    fireEvent.click(screen.getByTestId('workspace-tools-toggle'));
    const terminal = await screen.findByTestId('terminal-panel');
    expect(terminal).toHaveAttribute('data-conversation-id', conversationId);
    expect(terminal).toHaveAttribute('data-show-pop-out', 'true');

    fireEvent.change(screen.getByPlaceholderText('Ask kodelet anything...'), {
      target: { value: 'second message' },
    });
    fireEvent.click(screen.getByRole('button', { name: 'Send' }));

    await waitFor(() => expect(mockStreamChat).toHaveBeenCalledTimes(2));
    expect(mockStreamChat.mock.calls[1]?.[0]).toEqual(expect.objectContaining({ conversationId }));
    expect(mockStreamChat.mock.calls[1]?.[0]?.runnerId).toBeUndefined();
    expect(mockStreamChat.mock.calls[1]?.[0]?.environmentProfile).toBeUndefined();
  });

  it('updates the URL as soon as a new chat receives a conversation id', async () => {
    let streamOptions: { onEvent: (event: ChatStreamEvent) => void } | null = null;
    mockStreamChat.mockImplementation(
      async (_request, options) =>
        new Promise<void>(() => {
          streamOptions = options as {
            onEvent: (event: ChatStreamEvent) => void;
          };
        })
    );

    await renderChatWithRunner();

    await waitFor(() => expect(mockGetConversations).toHaveBeenCalled());

    fireEvent.change(screen.getByPlaceholderText('Ask kodelet anything...'), {
      target: { value: 'hello' },
    });
    fireEvent.click(screen.getByRole('button', { name: 'Send' }));

    await waitFor(() => expect(mockStreamChat).toHaveBeenCalled());
    expect(mockNavigate).not.toHaveBeenCalledWith('/c/conv-123', expect.anything());

    await act(async () => {
      streamOptions?.onEvent({
        kind: 'conversation',
        conversation_id: 'conv-123',
      });
    });

    expect(mockNavigate).toHaveBeenCalledWith('/c/conv-123', {
      replace: true,
    });
    await waitFor(() => expect(screen.getByTestId('sidebar-new-chat-button')).toBeEnabled());
  });

  it('keeps using the selected cwd while a started conversation record is still loading', async () => {
    let streamOptions: { onEvent: (event: ChatStreamEvent) => void } | null = null;
    mockGetConversation.mockResolvedValue({
      id: 'conv-123',
      createdAt: '2024-01-01T00:00:00Z',
      updatedAt: '2024-01-01T00:00:00Z',
      messageCount: 1,
      messages: [],
      toolResults: {},
      cwd: '/workspace/from-server',
    });
    mockStreamChat.mockImplementation(
      async (_request, options) =>
        new Promise<void>(() => {
          streamOptions = options as {
            onEvent: (event: ChatStreamEvent) => void;
          };
        })
    );

    const { rerender } = render(<ChatPage />);

    await waitFor(() => expect(mockGetChatSettings).toHaveBeenCalled());

    fireEvent.click(screen.getByTestId('sidebar-new-chat-button'));
    selectWorkspaceRunner();
    fireEvent.change(screen.getByLabelText('Working directory'), {
      target: { value: '/workspace/alt' },
    });
    await waitFor(() =>
      expect(mockGetCWDHints).toHaveBeenCalledWith('/workspace/alt', {
        runnerId: 'runner-1',
        environmentProfile: '',
        profile: 'work',
      })
    );
    fireEvent.click(screen.getByRole('button', { name: 'Start' }));

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

    await waitFor(() =>
      expect(mockNavigate).toHaveBeenCalledWith('/c/conv-123', {
        replace: true,
      })
    );

    setRouteParams({ id: 'conv-123' });
    rerender(<ChatPage />);

    expect(screen.getByTestId('chat-workspace-header')).toHaveTextContent('/workspace/alt');
    expect(screen.getByTestId('composer-context-button')).toHaveTextContent(/^medium$/);
    expect(screen.queryByTestId('workspace-tools-toggle')).not.toBeInTheDocument();
    expect(mockGetGitDiff).not.toHaveBeenCalled();
  });
});
