import { act, fireEvent, render, screen, waitFor, within } from '@testing-library/react';
import { describe, expect, it } from 'vitest';
import type { ChatStreamEvent, Conversation } from '../types';
import {
  ChatPage,
  flushAsyncUpdates,
  getActiveTab,
  makeRunner,
  mockDeleteConversation,
  mockGetConversation,
  mockGetConversations,
  mockNavigate,
  mockSteerConversation,
  mockStopConversation,
  mockStreamChat,
  mockStreamConversation,
  renderChatWithRunner,
  setRouteParams,
  setupChatPageTests,
} from './ChatPage.testSupport';

const runner = makeRunner();

const summaries: Conversation[] = [
  ['conv-a', 'Alpha task'],
  ['conv-b', 'Beta task'],
  ['conv-c', 'Gamma task'],
].map(([id, summary]) => ({
  id,
  summary,
  createdAt: '2026-11-01T00:00:00Z',
  updatedAt: '2026-11-01T00:00:00Z',
  messageCount: 1,
  cwd: '/runner/kodelet',
  runnerId: runner.id,
}));

const listConversations = (conversations: Conversation[]) =>
  mockGetConversations.mockResolvedValue({
    conversations,
    hasMore: false,
    total: conversations.length,
    limit: 100,
    offset: 0,
  });

const useConversations = (running: string[] = []) => {
  const conversations = summaries.map((summary) => ({
    ...summary,
    isRunning: running.includes(summary.id),
  }));
  listConversations(conversations);
  mockGetConversation.mockImplementation(async (id: string) => ({
    ...conversations.find((conversation) => conversation.id === id),
    runner,
    messages: [{ role: 'user', content: `Message in ${id}` }],
    toolResults: {},
  }));
};

const tabTitles = () =>
  screen
    .getAllByRole('tab', { hidden: true })
    .map((tab) => tab.querySelector('.chat-tab-title')?.textContent);

/** Renders the chat at each conversation in turn, as the router would. */
const visit = async (...ids: (string | null)[]) => {
  const [first, ...rest] = ids;
  setRouteParams(first ? { id: first } : {});
  const view = render(<ChatPage />);
  await flushAsyncUpdates();
  for (const id of rest) await revisit(view.rerender, id);
  return view;
};

const revisit = async (rerender: (ui: React.ReactElement) => void, id: string | null) => {
  setRouteParams(id ? { id } : {});
  rerender(<ChatPage />);
  await flushAsyncUpdates();
};

const streamListeners = () => {
  const listeners: Record<string, (event: ChatStreamEvent) => void> = {};
  mockStreamConversation.mockImplementation(
    (id: string, options: { onEvent: (event: ChatStreamEvent) => void }) => {
      listeners[id] = options.onEvent;
      return new Promise(() => {});
    }
  );
  return listeners;
};

describe('ChatPage conversation tabs', () => {
  setupChatPageTests();

  it('opens each visited conversation next to the active tab and switches between them', async () => {
    useConversations();
    const { rerender } = await visit('conv-a', 'conv-b', 'conv-a');
    expect(tabTitles()).toEqual(['Alpha task', 'Beta task']);

    fireEvent.click(within(screen.getByTestId('conversation-row-conv-c')).getByText('Gamma task'));
    expect(mockNavigate).toHaveBeenLastCalledWith('/c/conv-c');
    await revisit(rerender, 'conv-c');
    expect(tabTitles()).toEqual(['Alpha task', 'Gamma task', 'Beta task']);
    expect(getActiveTab()).toHaveTextContent('Gamma task');
    expect(getActiveTab()).toHaveAttribute('title', 'Gamma task\n/runner/kodelet');

    // Opening a conversation that already has a tab reuses it.
    fireEvent.click(within(screen.getByTestId('conversation-row-conv-b')).getByText('Beta task'));
    await revisit(rerender, 'conv-b');
    expect(tabTitles()).toEqual(['Alpha task', 'Gamma task', 'Beta task']);

    fireEvent.click(screen.getByRole('tab', { name: 'Alpha task' }));
    expect(mockNavigate).toHaveBeenLastCalledWith('/c/conv-a');
  });

  it('replaces an untouched new chat with the conversation opened from the sidebar', async () => {
    useConversations();
    const { rerender } = await visit(null);
    expect(tabTitles()).toEqual(['New chat']);

    fireEvent.click(within(screen.getByTestId('conversation-row-conv-a')).getByText('Alpha task'));
    expect(mockNavigate).toHaveBeenLastCalledWith('/c/conv-a');
    await revisit(rerender, 'conv-a');

    expect(tabTitles()).toEqual(['Alpha task']);
  });

  it('keeps a new chat with unsent text when opening a conversation from the sidebar', async () => {
    useConversations();
    const { rerender } = await visit(null);
    fireEvent.change(screen.getByTestId('composer-textarea'), {
      target: { value: 'Half-written request' },
    });

    fireEvent.click(within(screen.getByTestId('conversation-row-conv-a')).getByText('Alpha task'));
    await revisit(rerender, 'conv-a');

    expect(tabTitles()).toEqual(['New chat', 'Alpha task']);
    expect(screen.getByTestId('composer-textarea')).toHaveValue('');
  });

  it('closes a tab without stopping its running agent', async () => {
    useConversations(['conv-a']);
    streamListeners();
    const { rerender } = await visit('conv-a', 'conv-b', 'conv-a');

    fireEvent.click(screen.getByRole('button', { name: 'Alpha task actions' }));
    fireEvent.click(screen.getByRole('menuitem', { name: 'Close tab' }));

    expect(mockNavigate).toHaveBeenLastCalledWith('/c/conv-b');
    await revisit(rerender, 'conv-b');
    expect(tabTitles()).toEqual(['Beta task']);
    expect(mockStopConversation).not.toHaveBeenCalled();
    expect(screen.getByTestId('conversation-running-indicator-conv-a')).toBeInTheDocument();
  });

  it('starts over with a fresh new chat when its only tab closes', async () => {
    await renderChatWithRunner();
    fireEvent.change(screen.getByTestId('composer-textarea'), {
      target: { value: 'Never mind' },
    });

    fireEvent.keyDown(getActiveTab(), { key: 'Delete' });

    expect(tabTitles()).toEqual(['New chat']);
    expect(screen.getByTestId('composer-textarea')).toHaveValue('');
    expect(mockNavigate).not.toHaveBeenCalled();
  });

  it.each([
    [{ kind: 'done' }, 'New activity: Alpha task'],
    [{ kind: 'error', error: 'Provider overloaded' }, 'Failed: Alpha task'],
  ] as const)('flags a run that finishes in a background tab: %o', async (event, name) => {
    useConversations(['conv-a']);
    const listeners = streamListeners();
    const { rerender } = await visit('conv-a', 'conv-b');
    expect(screen.getByRole('tab', { name: 'Running: Alpha task' })).toBeInTheDocument();
    await waitFor(() =>
      expect(
        mockStreamConversation.mock.calls.filter(([id]) => id === 'conv-a').length
      ).toBeGreaterThan(1)
    );

    await act(async () => {
      listeners['conv-a']({ ...event, conversation_id: 'conv-a' });
    });

    expect(screen.getByRole('tab', { name })).toBeInTheDocument();
    expect(screen.getByRole('tab', { name: 'Beta task' })).toBeInTheDocument();

    // Viewing the finished conversation clears its flag.
    useConversations();
    await revisit(rerender, 'conv-a');
    expect(screen.getByRole('tab', { name: 'Alpha task' })).toBeInTheDocument();
  });

  it('flags a background tab that waits for an answer', async () => {
    useConversations(['conv-a']);
    const listeners = streamListeners();
    await visit('conv-a', 'conv-b');
    await waitFor(() =>
      expect(
        mockStreamConversation.mock.calls.filter(([id]) => id === 'conv-a').length
      ).toBeGreaterThan(1)
    );

    await act(async () => {
      listeners['conv-a']({
        kind: 'ui-input-request',
        conversation_id: 'conv-a',
        ui_input: { id: 'input-1', title: 'Which branch?' },
      });
    });

    expect(screen.getByTestId('ui-input-dialog')).toBeInTheDocument();
    expect(
      screen.getByRole('tab', { name: 'Needs input: Alpha task', hidden: true })
    ).toBeInTheDocument();
  });

  it('keeps unsent text with the tab it was typed in', async () => {
    useConversations();
    const { rerender } = await visit('conv-a');
    const composer = () => screen.getByTestId('composer-textarea');

    fireEvent.change(composer(), { target: { value: 'Draft for alpha' } });
    await revisit(rerender, 'conv-b');
    expect(composer()).toHaveValue('');
    fireEvent.change(composer(), { target: { value: 'Draft for beta' } });

    await revisit(rerender, 'conv-a');
    expect(composer()).toHaveValue('Draft for alpha');
    await revisit(rerender, 'conv-b');
    expect(composer()).toHaveValue('Draft for beta');

    // Closing a tab discards its unsent text.
    fireEvent.keyDown(getActiveTab(), { key: 'Delete' });
    await revisit(rerender, 'conv-a');
    await revisit(rerender, 'conv-b');
    expect(composer()).toHaveValue('');
  });

  it('clears a steering message from its own tab when it finishes after a switch', async () => {
    useConversations(['conv-a']);
    streamListeners();
    let finishSteer = () => {};
    mockSteerConversation.mockImplementation(
      () =>
        new Promise((resolve) => {
          finishSteer = () => resolve({ success: true, conversation_id: 'conv-a', queued: true });
        })
    );
    const { rerender } = await visit('conv-b');
    const composer = () => screen.getByTestId('composer-textarea');
    fireEvent.change(composer(), { target: { value: 'Unsent beta work' } });
    await revisit(rerender, 'conv-a');
    fireEvent.change(composer(), { target: { value: 'Also cover the edge case' } });
    fireEvent.click(screen.getByRole('button', { name: 'Steer' }));
    await waitFor(() =>
      expect(mockSteerConversation).toHaveBeenCalledWith(
        'conv-a',
        'Also cover the edge case',
        expect.anything()
      )
    );

    await revisit(rerender, 'conv-b');
    expect(composer()).toHaveValue('Unsent beta work');
    await act(async () => finishSteer());

    expect(composer()).toHaveValue('Unsent beta work');
    await revisit(rerender, 'conv-a');
    expect(composer()).toHaveValue('');
  });

  it('cycles tabs with Alt+[ and Alt+]', async () => {
    useConversations();
    await visit('conv-a', 'conv-b', 'conv-c', 'conv-b');
    expect(tabTitles()).toEqual(['Alpha task', 'Beta task', 'Gamma task']);

    fireEvent.keyDown(screen.getByTestId('composer-textarea'), {
      key: ']',
      code: 'BracketRight',
      altKey: true,
    });
    expect(mockNavigate).toHaveBeenLastCalledWith('/c/conv-c');

    fireEvent.keyDown(window, { key: '[', code: 'BracketLeft', altKey: true });
    expect(mockNavigate).toHaveBeenLastCalledWith('/c/conv-a');

    // Other modifiers belong to the browser or other shortcuts.
    mockNavigate.mockClear();
    fireEvent.keyDown(window, { key: ']', code: 'BracketRight', altKey: true, ctrlKey: true });
    fireEvent.keyDown(window, { key: '}', code: 'BracketRight', altKey: true, shiftKey: true });
    expect(mockNavigate).not.toHaveBeenCalled();
  });

  it('returns to the open new chat instead of resetting it', async () => {
    useConversations();
    const { rerender } = await renderChatWithRunner();
    fireEvent.change(screen.getByTestId('composer-textarea'), {
      target: { value: 'An unsent idea' },
    });
    await revisit(rerender, 'conv-a');
    expect(tabTitles()).toEqual(['New chat', 'Alpha task']);

    fireEvent.click(screen.getByTestId('sidebar-new-chat-button'));

    expect(mockNavigate).toHaveBeenLastCalledWith('/');
    expect(screen.queryByTestId('new-chat-dialog')).not.toBeInTheDocument();
    await revisit(rerender, null);
    expect(screen.getByTestId('composer-textarea')).toHaveValue('An unsent idea');
    expect(tabTitles()).toEqual(['New chat', 'Alpha task']);
  });

  it('opens new chat settings from a background new chat tab', async () => {
    useConversations();
    const { rerender } = await renderChatWithRunner();
    await revisit(rerender, 'conv-a');

    fireEvent.click(screen.getByRole('button', { name: 'New chat actions' }));
    fireEvent.click(screen.getByRole('menuitem', { name: 'Config' }));
    expect(mockNavigate).toHaveBeenLastCalledWith('/');
    expect(screen.queryByTestId('new-chat-dialog')).not.toBeInTheDocument();

    await revisit(rerender, null);
    expect(screen.getByTestId('new-chat-dialog')).toBeInTheDocument();
  });

  it('turns the new chat tab into the conversation it starts', async () => {
    useConversations();
    mockStreamChat.mockImplementation(() => new Promise(() => {}));
    const { rerender } = await renderChatWithRunner();
    fireEvent.change(screen.getByTestId('composer-textarea'), {
      target: { value: 'Build the feature' },
    });
    fireEvent.click(screen.getByRole('button', { name: 'Send' }));

    await waitFor(() => expect(mockStreamChat).toHaveBeenCalledTimes(1));
    const { conversationId } = mockStreamChat.mock.calls[0][0] as { conversationId: string };
    expect(mockNavigate).toHaveBeenCalledWith(`/c/${conversationId}`, { replace: true });
    await revisit(rerender, conversationId);

    expect(screen.getAllByRole('tab')).toHaveLength(1);
    expect(getActiveTab()).toHaveAccessibleName('Running: Build the feature');
  });

  it('closes the tab of a deleted conversation and shows its neighbour', async () => {
    useConversations();
    await visit('conv-a', 'conv-b');

    fireEvent.click(screen.getByRole('button', { name: /More actions for Beta task/i }));
    fireEvent.click(screen.getByRole('menuitem', { name: 'Delete' }));

    await waitFor(() => expect(mockDeleteConversation).toHaveBeenCalledWith('conv-b'));
    await waitFor(() => expect(mockNavigate).toHaveBeenLastCalledWith('/c/conv-a'));
    expect(tabTitles()).toEqual(['Alpha task']);
  });

  it('restores the window tabs and their titles after a reload', async () => {
    useConversations();
    const { unmount } = await visit('conv-a', 'conv-b');
    unmount();

    // The reloaded list no longer includes the older conversation.
    listConversations(summaries.slice(1, 2));
    await visit('conv-b');

    expect(tabTitles()).toEqual(['Alpha task', 'Beta task']);
    expect(screen.getByRole('tab', { name: 'Alpha task' })).toHaveAttribute(
      'title',
      'Alpha task\n/runner/kodelet'
    );
  });
});
