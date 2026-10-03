import { act, fireEvent, render, screen, waitFor, within } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { describe, expect, it, vi } from 'vitest';
import type { ChatSettings } from '../types';
import {
  ChatPage,
  flushAsyncUpdates,
  makeRunner,
  mockDiscardDraftWorkspace,
  mockGetAuthPrincipal,
  mockGetChatSettings,
  mockGetConversation,
  mockGetConversations,
  mockGetGitDiff,
  mockGetRunners,
  mockGetSlashCommands,
  mockHeartbeatDraftWorkspace,
  mockStreamChat,
  renderChatWithRunner,
  selectNewChatOption,
  selectWorkspaceRunner,
  setRouteParams,
  setupChatPageTests,
  waitForTerminalAccess,
} from './ChatPage.testSupport';

describe('ChatPage workspace tools and browsers', () => {
  setupChatPageTests();

  it.each([
    { name: 'enabled and authorized', capability: true, roles: ['terminal'], available: true },
    { name: 'enabled for admin', capability: true, roles: ['admin'], available: true },
    {
      name: 'disabled by the server or runner',
      capability: false,
      roles: ['admin'],
      available: false,
    },
    { name: 'principal lacks access', capability: true, roles: ['user'], available: false },
  ])('gates the browser tab when $name', async ({ capability, roles, available }) => {
    mockGetAuthPrincipal.mockResolvedValue({ id: 'user', roles });
    mockGetRunners.mockResolvedValue({
      runners: [makeRunner({ workspaceGitDiff: true, workspaceBrowser: capability })],
    });
    await renderChatWithRunner();
    await waitForTerminalAccess();
    expect(screen.queryAllByRole('button', { name: 'Show browser' })).toHaveLength(
      available ? 1 : 0
    );
    fireEvent.click(screen.getByTestId('workspace-tools-toggle'));
    await flushAsyncUpdates();
    if (!available) {
      expect(screen.queryByRole('tab', { name: 'Show browser' })).not.toBeInTheDocument();
      return;
    }
    fireEvent.click(screen.getByRole('tab', { name: 'Show browser' }));
    expect(await screen.findByTestId('browser-panel')).toHaveAttribute(
      'data-runner-id',
      'runner-1'
    );
    expect(screen.queryByTestId('terminal-panel')).not.toBeInTheDocument();
    expect(screen.getByRole('tab', { name: 'Show browser' })).toHaveAttribute(
      'aria-selected',
      'true'
    );
  });

  it('gives each new chat its own draft browser even with the same runner and directory', async () => {
    mockGetRunners.mockResolvedValue({ runners: [makeRunner({ workspaceBrowser: true })] });
    await renderChatWithRunner();
    await waitForTerminalAccess();
    fireEvent.click(screen.getByTestId('workspace-tools-toggle'));
    const firstBrowser = await screen.findByTestId('browser-panel');
    const firstId = firstBrowser.dataset.conversationId;
    expect(firstId).toMatch(/^\d{8}T\d{6}-[a-f0-9]{16}$/);
    expect(screen.queryByRole('tab', { name: 'Show terminal' })).not.toBeInTheDocument();
    expect(screen.queryByRole('tab', { name: 'Show changes' })).not.toBeInTheDocument();
    let expireHeartbeat = () => {};
    mockHeartbeatDraftWorkspace.mockImplementationOnce(
      () =>
        new Promise<void>((_, reject) => {
          expireHeartbeat = () => reject(Object.assign(new Error('Expired'), { status: 410 }));
        })
    );
    window.dispatchEvent(new Event('pageshow'));
    window.dispatchEvent(new Event('pageshow'));
    expect(mockHeartbeatDraftWorkspace).toHaveBeenCalledOnce();

    fireEvent.click(screen.getByTestId('sidebar-new-chat-button'));
    selectWorkspaceRunner();
    await flushAsyncUpdates();
    fireEvent.click(screen.getByRole('button', { name: 'Start' }));
    fireEvent.click(screen.getByTestId('workspace-tools-toggle'));
    const nextBrowser = await screen.findByTestId('browser-panel');
    expect(nextBrowser).not.toBe(firstBrowser);
    expect(nextBrowser.dataset.conversationId).toMatch(/^\d{8}T\d{6}-[a-f0-9]{16}$/);
    expect(nextBrowser.dataset.conversationId).not.toBe(firstId);
    expect(nextBrowser).toHaveAttribute('data-runner-id', 'runner-1');
    const nextId = nextBrowser.dataset.conversationId;
    await act(async () => expireHeartbeat());
    expect(screen.getByTestId('browser-panel')).toBe(nextBrowser);
    expect(nextBrowser.dataset.conversationId).toBe(nextId);
    expect(mockStreamChat).not.toHaveBeenCalled();
    expect(mockDiscardDraftWorkspace).toHaveBeenCalledExactlyOnceWith({
      runnerId: 'runner-1',
      conversationId: firstId,
    });
  });

  it('discards visited runners sequentially on history navigation', async () => {
    mockGetRunners.mockResolvedValue({
      runners: [
        makeRunner({ workspaceBrowser: true }),
        makeRunner({ id: 'runner-2', workspaceBrowser: true }),
      ],
    });
    const { rerender } = await renderChatWithRunner();
    fireEvent.click(screen.getByTestId('workspace-tools-toggle'));
    const conversationId = (await screen.findByTestId('browser-panel')).dataset.conversationId;
    fireEvent.click(screen.getByRole('button', { name: /^Change workspace:/ }));
    selectNewChatOption('Environment', 'runner-2');
    await flushAsyncUpdates();
    fireEvent.click(screen.getByRole('button', { name: 'Start' }));
    expect(screen.getByTestId('browser-panel')).toHaveAttribute('data-runner-id', 'runner-2');
    expect(mockDiscardDraftWorkspace).not.toHaveBeenCalled();
    let finishFirst = () => {};
    mockDiscardDraftWorkspace.mockImplementationOnce(
      () =>
        new Promise<void>((resolve) => {
          finishFirst = resolve;
        })
    );
    mockGetConversation.mockResolvedValue({ id: 'saved', runnerId: 'runner-1', messages: [] });
    setRouteParams({ id: 'saved' });
    rerender(<ChatPage />);
    await flushAsyncUpdates();
    expect(mockDiscardDraftWorkspace).toHaveBeenCalledExactlyOnceWith({
      runnerId: 'runner-1',
      conversationId,
    });
    await act(async () => finishFirst());
    expect(mockDiscardDraftWorkspace.mock.calls).toEqual([
      [{ runnerId: 'runner-1', conversationId }],
      [{ runnerId: 'runner-2', conversationId }],
    ]);
  });

  it('renews closed-panel drafts and recovers expired sessions on resume without losing text or settings', async () => {
    vi.useFakeTimers({ toFake: ['setInterval', 'clearInterval'] });
    try {
      mockGetRunners.mockResolvedValue({
        runners: [makeRunner({ workspaceBrowser: true, workspaceCwd: true })],
      });
      const { unmount } = await renderChatWithRunner();
      fireEvent.click(screen.getByRole('button', { name: /^Change workspace:/ }));
      fireEvent.change(screen.getByLabelText('Working directory'), {
        target: { value: '/runner/custom' },
      });
      await flushAsyncUpdates();
      fireEvent.click(screen.getByRole('button', { name: 'Start' }));
      fireEvent.click(screen.getByTestId('workspace-tools-toggle'));
      const draftId = (await screen.findByTestId('browser-panel')).dataset.conversationId;
      fireEvent.change(screen.getByPlaceholderText('Ask kodelet anything...'), {
        target: { value: 'Keep my unsent message' },
      });
      fireEvent.click(screen.getByTestId('workspace-tools-toggle'));
      await act(async () => vi.advanceTimersByTimeAsync(30_000));
      expect(mockHeartbeatDraftWorkspace).toHaveBeenCalledExactlyOnceWith({
        runnerId: 'runner-1',
        conversationId: draftId,
      });
      expect(mockDiscardDraftWorkspace).not.toHaveBeenCalled();
      mockHeartbeatDraftWorkspace.mockRejectedValueOnce(
        Object.assign(new Error('Expired'), { status: 410 })
      );
      window.dispatchEvent(new PageTransitionEvent('pageshow', { persisted: true }));
      await flushAsyncUpdates();
      fireEvent.click(screen.getByTestId('workspace-tools-toggle'));
      const recovered = await screen.findByTestId('browser-panel');
      expect(recovered.dataset.conversationId).not.toBe(draftId);
      expect(recovered).toHaveAttribute('data-cwd', '/runner/custom');
      expect(screen.getByPlaceholderText('Ask kodelet anything...')).toHaveValue(
        'Keep my unsent message'
      );
      expect(mockDiscardDraftWorkspace).toHaveBeenCalledExactlyOnceWith({
        runnerId: 'runner-1',
        conversationId: draftId,
      });
      document.dispatchEvent(new Event('visibilitychange'));
      await flushAsyncUpdates();
      expect(mockHeartbeatDraftWorkspace).toHaveBeenLastCalledWith({
        runnerId: 'runner-1',
        conversationId: recovered.dataset.conversationId,
      });
      unmount();
      await act(async () => vi.advanceTimersByTimeAsync(60_000));
      expect(mockHeartbeatDraftWorkspace).toHaveBeenCalledTimes(3);
    } finally {
      vi.useRealTimers();
    }
  });

  it('does not restore browser access from an older conversation runner snapshot', async () => {
    setRouteParams({ id: 'conv-browser' });
    const savedRunner = makeRunner({ workspaceBrowser: true, workspaceTerminal: true });
    mockGetRunners.mockResolvedValue({ runners: [{ ...savedRunner, workspaceBrowser: false }] });
    mockGetConversation.mockResolvedValue({
      id: 'conv-browser',
      createdAt: '2026-09-14T00:00:00Z',
      updatedAt: '2026-09-14T00:00:00Z',
      messageCount: 1,
      cwd: '/runner/kodelet',
      runnerId: savedRunner.id,
      runner: savedRunner,
      messages: [{ role: 'user', content: 'Browser permission changed' }],
      toolResults: {},
    });
    render(<ChatPage />);
    await screen.findByText('Browser permission changed');
    await waitForTerminalAccess();
    fireEvent.click(screen.getByTestId('workspace-tools-toggle'));
    await screen.findByTestId('terminal-panel');
    expect(screen.queryByRole('tab', { name: 'Show browser' })).not.toBeInTheDocument();
  });

  it('uses conversation identity rather than cwd when switching between saved browsers', async () => {
    const runner = makeRunner({ workspaceBrowser: true });
    mockGetRunners.mockResolvedValue({ runners: [runner] });
    mockGetConversation.mockImplementation(async (id: string) => ({
      id,
      createdAt: '2026-09-14T00:00:00Z',
      updatedAt: '2026-09-14T00:00:00Z',
      messageCount: 1,
      cwd: '/runner/kodelet',
      runnerId: runner.id,
      runner,
      messages: [{ role: 'user', content: `Saved ${id}` }],
      toolResults: {},
    }));
    setRouteParams({ id: 'conv-browser-a' });
    const { rerender } = render(<ChatPage />);
    let previousPanel: HTMLElement | undefined;
    for (const id of ['conv-browser-a', 'conv-browser-b', 'conv-browser-a']) {
      setRouteParams({ id });
      rerender(<ChatPage />);
      await screen.findByText(`Saved ${id}`);
      fireEvent.click(screen.getByTestId('workspace-tools-toggle'));
      const panel = await screen.findByTestId('browser-panel');
      expect(panel).toHaveAttribute('data-conversation-id', id);
      expect(panel).toHaveAttribute('data-runner-id', runner.id);
      expect(panel).not.toBe(previousPanel);
      previousPanel = panel;
    }
  });

  it.each([
    false,
    true,
  ])('gates draft directory tools on runner support (%s), not conversation history', async (workspaceCwd) => {
    mockGetRunners.mockResolvedValue({
      runners: [
        makeRunner({
          workspaceBrowser: true,
          workspaceTerminal: true,
          workspaceGitDiff: true,
          workspaceCwd,
        }),
      ],
    });
    render(<ChatPage />);
    await waitFor(() => expect(mockGetRunners).toHaveBeenCalled());
    await waitForTerminalAccess();
    fireEvent.click(screen.getByTestId('sidebar-new-chat-button'));
    selectWorkspaceRunner();
    fireEvent.change(screen.getByLabelText('Working directory'), {
      target: { value: '/runner/other-project' },
    });
    await flushAsyncUpdates();
    fireEvent.click(screen.getByRole('button', { name: 'Start' }));
    expect(mockStreamChat).not.toHaveBeenCalled();
    expect(mockGetConversation).not.toHaveBeenCalled();
    if (!workspaceCwd) {
      expect(screen.queryByTestId('workspace-tools-shell')).not.toBeInTheDocument();
      return;
    }

    fireEvent.click(screen.getByRole('button', { name: 'Show terminal' }));
    const terminal = await screen.findByTestId('terminal-panel');
    expect(terminal).toHaveAttribute('data-runner-id', 'runner-1');
    expect(terminal).toHaveAttribute('data-cwd', '/runner/other-project');
    expect(terminal.dataset.conversationId).toMatch(/^\d{8}T\d{6}-[a-f0-9]{16}$/);

    fireEvent.click(screen.getByRole('tab', { name: 'Show changes' }));
    await waitFor(() =>
      expect(mockGetGitDiff).toHaveBeenCalledWith({
        kind: 'runner',
        runnerId: 'runner-1',
        cwd: '/runner/other-project',
      })
    );

    fireEvent.click(screen.getByRole('tab', { name: 'Show browser' }));
    const browser = await screen.findByTestId('browser-panel');
    expect(browser).toHaveAttribute('data-runner-id', 'runner-1');
    expect(browser).toHaveAttribute('data-cwd', '/runner/other-project');
    expect(browser.dataset.conversationId).toBe(terminal.dataset.conversationId);
    expect(mockStreamChat).not.toHaveBeenCalled();
  });

  it.each([
    { name: 'terminal and diff', terminal: true, diff: true },
    { name: 'terminal only', terminal: true, diff: false },
    { name: 'diff only', terminal: false, diff: true },
    { name: 'neither workspace tool', terminal: false, diff: false },
  ])('handles keyboard navigation with $name capability', async ({ terminal, diff }) => {
    mockGetRunners.mockResolvedValue({
      runners: [
        makeRunner({
          workspaceTerminal: terminal,
          workspaceGitDiff: diff,
        }),
      ],
    });

    render(<ChatPage />);
    await waitFor(() => expect(mockGetRunners).toHaveBeenCalled());
    await waitForTerminalAccess();
    fireEvent.click(screen.getByTestId('sidebar-new-chat-button'));
    selectWorkspaceRunner();
    await flushAsyncUpdates();
    fireEvent.click(screen.getByRole('button', { name: 'Start' }));

    if (!terminal && !diff) {
      expect(screen.queryByTestId('workspace-tools-shell')).not.toBeInTheDocument();
      return;
    }

    expect(screen.queryAllByRole('button', { name: 'Show terminal' })).toHaveLength(
      terminal ? 1 : 0
    );
    expect(screen.queryAllByRole('button', { name: 'Show changes' })).toHaveLength(diff ? 1 : 0);
    fireEvent.click(screen.getByTestId('workspace-tools-toggle'));
    if (terminal) {
      expect(screen.getByTestId('workspace-tools-terminal-tab')).toBeInTheDocument();
    } else {
      expect(screen.queryByTestId('workspace-tools-terminal-tab')).not.toBeInTheDocument();
    }
    if (diff) {
      expect(screen.getByTestId('workspace-tools-diff-tab')).toBeInTheDocument();
    } else {
      expect(screen.queryByTestId('workspace-tools-diff-tab')).not.toBeInTheDocument();
    }

    if (!terminal) {
      expect(await screen.findByTestId('git-diff-panel')).toBeInTheDocument();
      return;
    }

    const terminalHost = await screen.findByTestId('terminal-host');
    terminalHost.focus();
    fireEvent.keyDown(terminalHost, { key: 'F6', shiftKey: true });
    expect(
      screen.getByTestId(diff ? 'workspace-tools-diff-tab' : 'workspace-tools-terminal-tab')
    ).toHaveFocus();
  });

  it.each([
    { name: 'offline', connected: false, status: 'offline' as const },
    { name: 'incompatible', connected: true, status: 'incompatible' as const },
  ])('hides remote workspace tools for an $name runner', async ({ connected, status }) => {
    setRouteParams({ id: 'conv-remote' });
    const runner = makeRunner({
      connected,
      status,
      workspaceGitDiff: true,
      workspaceTerminal: true,
    });
    mockGetRunners.mockResolvedValue({ runners: [runner] });
    mockGetConversation.mockResolvedValue({
      id: 'conv-remote',
      createdAt: '2026-08-19T00:00:00Z',
      updatedAt: '2026-08-19T00:00:00Z',
      messageCount: 1,
      cwd: '/runner/kodelet',
      runnerId: runner.id,
      runner,
      messages: [{ role: 'user', content: 'remote' }],
      toolResults: {},
    });

    render(<ChatPage />);

    await waitFor(() => expect(mockGetConversation).toHaveBeenCalledWith('conv-remote'));
    await waitForTerminalAccess();
    expect(screen.queryByTestId('workspace-tools-shell')).not.toBeInTheDocument();
  });

  it.each([
    'terminal',
    'browser',
  ])('remounts the %s after runner reconnection without changing its target', async (panel) => {
    vi.useFakeTimers();
    setRouteParams({ id: 'conv-remote' });
    const firstGeneration = makeRunner({
      generation: 1,
      workspaceGitDiff: true,
      workspaceTerminal: true,
      workspaceBrowser: true,
    });
    const secondGeneration = makeRunner({
      generation: 2,
      workspaceGitDiff: true,
      workspaceTerminal: true,
      workspaceBrowser: true,
    });
    mockGetRunners
      .mockResolvedValueOnce({ runners: [firstGeneration] })
      .mockResolvedValue({ runners: [secondGeneration] });
    mockGetConversation.mockResolvedValue({
      id: 'conv-remote',
      createdAt: '2026-08-19T00:00:00Z',
      updatedAt: '2026-08-19T00:00:00Z',
      messageCount: 1,
      cwd: '/runner/kodelet',
      runnerId: firstGeneration.id,
      runner: firstGeneration,
      messages: [{ role: 'user', content: 'remote' }],
      toolResults: {},
    });

    try {
      render(<ChatPage />);
      await flushAsyncUpdates();
      await flushAsyncUpdates();
      fireEvent.click(screen.getByTestId('workspace-tools-toggle'));
      if (panel === 'browser') fireEvent.click(screen.getByRole('tab', { name: 'Show browser' }));
      const firstPanel = screen.getByTestId(`${panel}-panel`);
      expect(firstPanel).toHaveAttribute('data-runner-id', 'runner-1');
      expect(firstPanel).toHaveAttribute('data-conversation-id', 'conv-remote');

      await act(async () => {
        vi.advanceTimersByTime(5000);
        await Promise.resolve();
        await Promise.resolve();
      });

      expect(mockGetRunners).toHaveBeenCalledTimes(2);
      const reconnectedPanel = screen.getByTestId(`${panel}-panel`);
      expect(reconnectedPanel).not.toBe(firstPanel);
      expect(reconnectedPanel).toHaveAttribute('data-runner-id', 'runner-1');
      expect(reconnectedPanel).toHaveAttribute('data-conversation-id', 'conv-remote');
      if (panel === 'terminal')
        expect(reconnectedPanel).toHaveAttribute('data-show-pop-out', 'true');
    } finally {
      vi.useRealTimers();
    }
  });

  it('opens terminal in the workspace side panel from the right rail by default', async () => {
    await renderChatWithRunner();

    await waitFor(() => expect(mockGetConversations).toHaveBeenCalled());
    await waitForTerminalAccess();

    expect(screen.getByTestId('workspace-tools-shell')).toBeInTheDocument();
    expect(screen.queryByTestId('workspace-tools-dock')).not.toBeInTheDocument();

    fireEvent.click(screen.getByTestId('workspace-tools-toggle'));

    expect(screen.getByTestId('workspace-tools-dock')).toBeInTheDocument();
    expect(await screen.findByTestId('terminal-panel')).toBeInTheDocument();
    expect(screen.getByTestId('composer-textarea')).toBeInTheDocument();
    expect(screen.queryByTestId('terminal-modal-backdrop')).not.toBeInTheDocument();
  });

  it('opens each workspace view directly from the folded rail', async () => {
    const user = userEvent.setup();
    mockGetRunners.mockResolvedValue({
      runners: [
        makeRunner({ workspaceTerminal: true, workspaceGitDiff: true, workspaceBrowser: true }),
      ],
    });
    await renderChatWithRunner();

    const rail = within(screen.getByTestId('workspace-tools-rail'));
    for (const [name, panel] of [
      ['terminal', 'terminal-panel'],
      ['changes', 'git-diff-panel'],
      ['browser', 'browser-panel'],
    ]) {
      rail.getByRole('button', { name: `Show ${name}` }).focus();
      await user.keyboard('{Enter}');
      expect(await screen.findByTestId(panel)).toBeInTheDocument();
      expect(screen.getByRole('tab', { name: `Show ${name}` })).toHaveAttribute(
        'aria-selected',
        'true'
      );
      expect(rail.getAllByRole('button')).toHaveLength(1);
      await user.click(rail.getByRole('button', { name: 'Hide workspace panel' }));
    }
    expect(rail.getAllByRole('button')).toHaveLength(4);
  });

  it('switches to changes in the workspace side panel', async () => {
    await renderChatWithRunner();

    await waitFor(() => expect(mockGetConversations).toHaveBeenCalled());
    await waitFor(() => expect(mockGetChatSettings).toHaveBeenCalled());
    await waitForTerminalAccess();

    fireEvent.click(screen.getByTestId('workspace-tools-toggle'));
    await waitFor(() => expect(screen.getByTestId('terminal-panel')).toBeInTheDocument());
    fireEvent.click(screen.getByTestId('workspace-tools-diff-tab'));

    await waitFor(() =>
      expect(mockGetGitDiff).toHaveBeenCalledWith({
        kind: 'runner',
        runnerId: 'runner-1',
        cwd: '/runner/kodelet',
      })
    );
    expect(screen.getByTestId('workspace-tools-dock')).toBeInTheDocument();
    await waitFor(() => expect(screen.getByTestId('git-diff-panel')).toBeInTheDocument());
    expect(screen.queryByRole('heading', { name: 'Changes' })).not.toBeInTheDocument();
    expect(screen.getByTestId('composer-textarea')).toBeInTheDocument();
    expect(screen.queryByTestId('git-diff-modal-backdrop')).not.toBeInTheDocument();
  });

  it('refreshes commands and an open diff only when the runner generation changes', async () => {
    vi.useFakeTimers({ toFake: ['setInterval', 'clearInterval'] });
    const runner = makeRunner({
      workspaceDiscovery: true,
      workspaceTerminal: true,
      workspaceGitDiff: true,
    });
    mockGetRunners.mockResolvedValue({ runners: [runner] });

    try {
      await renderChatWithRunner();
      fireEvent.click(screen.getByTestId('workspace-tools-toggle'));
      await screen.findByTestId('terminal-panel');
      fireEvent.click(screen.getByTestId('workspace-tools-diff-tab'));
      await screen.findByTestId('git-diff-panel');
      await flushAsyncUpdates();
      expect(mockGetGitDiff).toHaveBeenCalledTimes(1);
      expect(mockGetSlashCommands).toHaveBeenCalledTimes(1);

      mockGetRunners.mockImplementation(async () => ({
        runners: [{ ...runner, generation: 2 }],
      }));
      await act(async () => {
        vi.advanceTimersByTime(5000);
      });
      await flushAsyncUpdates();
      expect(mockGetGitDiff).toHaveBeenCalledTimes(2);
      expect(mockGetSlashCommands).toHaveBeenCalledTimes(2);
      expect(mockGetGitDiff).toHaveBeenLastCalledWith({
        kind: 'runner',
        runnerId: 'runner-1',
        cwd: '/runner/kodelet',
      });

      await act(async () => {
        vi.advanceTimersByTime(5000);
      });
      await flushAsyncUpdates();
      expect(mockGetRunners).toHaveBeenCalledTimes(3);
      expect(mockGetGitDiff).toHaveBeenCalledTimes(2);
      expect(mockGetSlashCommands).toHaveBeenCalledTimes(2);
      expect(screen.getByTestId('git-diff-panel')).toBeInTheDocument();
    } finally {
      vi.useRealTimers();
    }
  });

  it('switches and closes the workspace side panel', async () => {
    await renderChatWithRunner();

    await waitFor(() => expect(mockGetConversations).toHaveBeenCalled());
    await waitFor(() => expect(mockGetChatSettings).toHaveBeenCalled());
    await waitForTerminalAccess();

    fireEvent.click(screen.getByTestId('workspace-tools-toggle'));
    await waitFor(() => expect(screen.getByTestId('terminal-panel')).toBeInTheDocument());

    fireEvent.click(screen.getByTestId('workspace-tools-diff-tab'));
    await waitFor(() => expect(screen.getByTestId('git-diff-panel')).toBeInTheDocument());
    expect(screen.queryByTestId('terminal-panel')).not.toBeInTheDocument();

    fireEvent.click(screen.getByTestId('workspace-tools-toggle'));
    expect(screen.queryByTestId('workspace-tools-dock')).not.toBeInTheDocument();
    expect(screen.getByTestId('workspace-tools-rail')).toBeInTheDocument();
    expect(screen.getByTestId('composer-textarea')).toBeInTheDocument();
  });

  it.each([
    false,
    true,
  ])('gates conversation-directory workspace tools on runner support (%s)', async (workspaceCwd) => {
    setRouteParams({ id: 'conv-selected-directory' });
    const runner = makeRunner({
      workspaceCwd,
      workspaceDiscovery: true,
      workspaceTerminal: true,
      workspaceGitDiff: true,
      workspaceBrowser: true,
    });
    mockGetRunners.mockResolvedValue({ runners: [runner] });
    mockGetConversation.mockResolvedValue({
      id: 'conv-selected-directory',
      cwd: '/runner/different-directory',
      runnerId: runner.id,
      environmentProfile: 'review',
      profile: workspaceCwd ? 'default' : 'anthropic',
      runner,
      messages: [{ role: 'user', content: 'remote' }],
      toolResults: {},
    });
    render(<ChatPage />);
    await waitFor(() =>
      expect(mockGetSlashCommands).toHaveBeenCalledWith(undefined, {
        runnerId: 'runner-1',
        conversationId: 'conv-selected-directory',
        environmentProfile: 'review',
        profile: undefined,
      })
    );
    await waitForTerminalAccess();
    if (!workspaceCwd) {
      expect(screen.queryByTestId('workspace-tools-shell')).not.toBeInTheDocument();
      return;
    }
    fireEvent.click(screen.getByTestId('workspace-tools-toggle'));
    expect(await screen.findByTestId('terminal-panel')).toHaveAttribute(
      'data-conversation-id',
      'conv-selected-directory'
    );
    expect(screen.getByTestId('terminal-panel')).toHaveAttribute(
      'data-cwd',
      '/runner/different-directory'
    );
    fireEvent.click(screen.getByTestId('workspace-tools-diff-tab'));
    await waitFor(() =>
      expect(mockGetGitDiff).toHaveBeenCalledWith({
        kind: 'runner',
        runnerId: 'runner-1',
        conversationId: 'conv-selected-directory',
        cwd: '/runner/different-directory',
      })
    );
    fireEvent.click(screen.getByRole('tab', { name: 'Show browser' }));
    expect(await screen.findByTestId('browser-panel')).toHaveAttribute(
      'data-conversation-id',
      'conv-selected-directory'
    );
  });

  it('shows remote terminal and changes when the runner advertises workspace tools', async () => {
    mockGetRunners.mockResolvedValue({
      runners: [makeRunner({ workspaceGitDiff: true, workspaceTerminal: true })],
    });

    render(<ChatPage />);
    await waitFor(() => expect(mockGetRunners).toHaveBeenCalled());
    await waitForTerminalAccess();
    fireEvent.click(screen.getByTestId('sidebar-new-chat-button'));
    selectWorkspaceRunner();
    await flushAsyncUpdates();
    fireEvent.click(screen.getByRole('button', { name: 'Start' }));

    expect(screen.getByTestId('workspace-tools-shell')).toBeInTheDocument();
    fireEvent.click(screen.getByTestId('workspace-tools-toggle'));

    const terminal = await screen.findByTestId('terminal-panel');
    expect(terminal).toHaveAttribute('data-runner-id', 'runner-1');
    expect(terminal).toHaveAttribute('data-show-pop-out', 'true');
    expect(terminal.dataset.conversationId).toMatch(/^\d{8}T\d{6}-[a-f0-9]{16}$/);
    expect(terminal).toHaveAttribute('data-cwd', '/runner/kodelet');

    fireEvent.click(screen.getByTestId('workspace-tools-diff-tab'));
    await waitFor(() =>
      expect(mockGetGitDiff).toHaveBeenCalledWith({
        kind: 'runner',
        runnerId: 'runner-1',
        cwd: '/runner/kodelet',
      })
    );
  });

  it('enables terminal pop-out for an established remote conversation', async () => {
    setRouteParams({ id: 'conv-remote' });
    const runner = makeRunner({ workspaceGitDiff: true, workspaceTerminal: true });
    mockGetRunners.mockResolvedValue({ runners: [runner] });
    mockGetConversation.mockResolvedValue({
      id: 'conv-remote',
      createdAt: '2026-08-19T00:00:00Z',
      updatedAt: '2026-08-19T00:00:00Z',
      messageCount: 1,
      cwd: '/runner/kodelet',
      runnerId: runner.id,
      runner,
      messages: [{ role: 'user', content: 'remote' }],
      toolResults: {},
    });

    render(<ChatPage />);

    await waitFor(() => expect(mockGetConversation).toHaveBeenCalledWith('conv-remote'));
    await waitForTerminalAccess();
    await waitFor(() => expect(screen.getByTestId('workspace-tools-shell')).toBeInTheDocument());
    fireEvent.click(screen.getByTestId('workspace-tools-toggle'));

    const terminal = await screen.findByTestId('terminal-panel');
    expect(terminal).toHaveAttribute('data-conversation-id', 'conv-remote');
    expect(terminal).toHaveAttribute('data-runner-id', 'runner-1');
    expect(terminal).toHaveAttribute('data-show-pop-out', 'true');
    expect(terminal).toHaveAttribute('data-cwd', '/runner/kodelet');
  });

  it('hides terminal access from principals without the terminal role', async () => {
    mockGetAuthPrincipal.mockResolvedValue({
      id: 'https://issuer.example.com|user-only',
      issuer: 'https://issuer.example.com',
      subject: 'user-only',
      name: 'User Only',
      email: 'user@example.com',
      roles: ['user'],
    });
    mockGetRunners.mockResolvedValue({
      runners: [makeRunner({ workspaceGitDiff: true, workspaceTerminal: true })],
    });

    render(<ChatPage />);
    await screen.findByRole('button', { name: 'User Only account menu' });
    await screen.findByText('No saved conversations yet.');
    await waitFor(() => expect(screen.getByTestId('sidebar-new-chat-button')).toBeEnabled());
    fireEvent.click(screen.getByTestId('sidebar-new-chat-button'));
    selectWorkspaceRunner();
    await flushAsyncUpdates();
    fireEvent.click(screen.getByRole('button', { name: 'Start' }));
    fireEvent.click(screen.getByTestId('workspace-tools-toggle'));

    expect(screen.queryByTestId('workspace-tools-terminal-tab')).not.toBeInTheDocument();
    expect(screen.getByTestId('workspace-tools-diff-tab')).toBeInTheDocument();
    await screen.findByTestId('git-diff-panel');
    await waitFor(() => expect(mockGetGitDiff).toHaveBeenCalled());
  });

  it.each([
    undefined,
    'saved-local-model',
  ])('hides stale model, workspace path, and tools while loading a conversation with model %s', async (model) => {
    setRouteParams({ id: 'conv-remote' });
    const defaults: ChatSettings = await mockGetChatSettings();
    mockGetChatSettings.mockResolvedValue({ ...defaults, model: 'profile-default-model' });
    const runner = makeRunner({ workspaceGitDiff: true, workspaceTerminal: true });
    mockGetRunners.mockResolvedValue({ runners: [runner] });
    let resolveLocalConversation: ((value: unknown) => void) | undefined;
    mockGetConversation.mockImplementation((id: string) => {
      if (id === 'conv-remote') {
        return Promise.resolve({
          id,
          createdAt: '2026-08-19T00:00:00Z',
          updatedAt: '2026-08-19T00:00:00Z',
          messageCount: 1,
          cwd: '/runner/kodelet',
          runnerId: runner.id,
          profile: 'remote-profile',
          model: 'saved-remote-model',
          runner,
          messages: [{ role: 'user', content: 'remote' }],
          toolResults: {},
        });
      }
      return new Promise((resolve) => {
        resolveLocalConversation = resolve;
      });
    });

    const { rerender } = render(<ChatPage />);
    await waitFor(() => expect(screen.getByTestId('workspace-tools-shell')).toBeInTheDocument());
    expect(screen.getByTestId('composer-inline-context')).toHaveTextContent(
      /^remote-profile\/saved-remote-model$/
    );
    expect(screen.getByTestId('chat-workspace-header')).toHaveTextContent('/runner/kodelet');

    setRouteParams({ id: 'conv-local' });
    rerender(<ChatPage />);

    await waitFor(() => expect(mockGetConversation).toHaveBeenCalledWith('conv-local'));
    expect(screen.queryByTestId('workspace-tools-shell')).not.toBeInTheDocument();
    expect(screen.getByTestId('composer-inline-context')).toHaveTextContent(/^Saved settings$/);
    const header = screen.getByTestId('chat-workspace-header');
    expect(header).toHaveTextContent('Loading workspace…');
    expect(header).not.toHaveTextContent('/runner/kodelet');
    expect(within(header).queryByTitle('/runner/kodelet')).not.toBeInTheDocument();
    expect(
      within(header).queryByRole('button', { name: /^Change workspace:/ })
    ).not.toBeInTheDocument();

    await act(async () => {
      resolveLocalConversation?.({
        id: 'conv-local',
        createdAt: '2026-08-19T00:00:00Z',
        updatedAt: '2026-08-19T00:00:00Z',
        messageCount: 1,
        cwd: '/workspace/local',
        model,
        messages: [{ role: 'user', content: 'local' }],
        toolResults: {},
      });
      await Promise.resolve();
    });
    const context = screen.getByTestId('composer-inline-context');
    if (model) {
      expect(context.textContent).toBe(model);
    } else {
      expect(context).toHaveTextContent(/^Saved settings$/);
    }
    expect(context).not.toHaveTextContent('profile-default-model');
    expect(context).not.toHaveTextContent('remote-profile');
    expect(header).toHaveTextContent('/workspace/local');
    expect(header).not.toHaveTextContent('/runner/kodelet');
    expect(context).not.toHaveTextContent('/workspace/local');
  });

  it('keeps existing local conversations read-only without workspace discovery or tools', async () => {
    setRouteParams({ id: 'conv-123' });
    const localConversation = {
      id: 'conv-123',
      createdAt: '2023-01-01T00:00:00Z',
      updatedAt: '2023-01-02T00:00:00Z',
      messageCount: 1,
      cwd: '/workspace/project',
      cwdLocked: true,
      messages: [
        {
          role: 'user',
          content: 'hello',
        },
      ],
      toolResults: {},
    };
    let resolveConversation!: (value: typeof localConversation) => void;
    mockGetConversation.mockReturnValue(
      new Promise((resolve) => {
        resolveConversation = resolve;
      })
    );

    render(<ChatPage />);

    await waitFor(() => expect(mockGetConversation).toHaveBeenCalledWith('conv-123'));
    expect(screen.getByText('Loading conversation…')).toBeVisible();
    expect(screen.queryByText('hello')).not.toBeInTheDocument();

    await act(async () => {
      resolveConversation(localConversation);
    });

    expect(screen.getByText('hello')).toBeVisible();
    expect(screen.queryByText('Loading conversation…')).not.toBeInTheDocument();
    expect(mockGetConversation).toHaveBeenCalledTimes(1);
    expect(screen.getByTestId('composer-inline-context')).toHaveTextContent(/^Saved settings$/);
    const header = screen.getByTestId('chat-workspace-header');
    expect(header).toHaveTextContent('/workspace/project');
    expect(within(header).getByTitle('/workspace/project')).toBeInTheDocument();
    expect(
      within(header).queryByRole('button', { name: /^Change workspace:/ })
    ).not.toBeInTheDocument();
    expect(within(header).queryByRole('button')).not.toBeInTheDocument();
    expect(screen.getByPlaceholderText('This local conversation is read-only')).toBeDisabled();
    expect(screen.getByRole('button', { name: 'Send' })).toBeDisabled();
    expect(screen.queryByTestId('workspace-tools-shell')).not.toBeInTheDocument();
    expect(mockGetSlashCommands).not.toHaveBeenCalled();
    expect(mockGetGitDiff).not.toHaveBeenCalled();
    expect(screen.queryByLabelText('Working directory')).not.toBeInTheDocument();
  });

  it('uses the live runner status instead of the conversation runner snapshot', async () => {
    setRouteParams({ id: 'conv-123' });
    const idleRunner = makeRunner({
      displayName: 'kodelet',
      host: {
        instanceId: 'host-1',
        hostname: 'worker',
        os: 'darwin',
        arch: 'arm64',
      },
    });
    mockGetConversation.mockResolvedValue({
      id: 'conv-123',
      createdAt: '2026-08-09T00:00:00Z',
      updatedAt: '2026-08-09T00:00:00Z',
      messageCount: 1,
      cwd: '/runner/kodelet',
      runnerId: idleRunner.id,
      environmentProfile: 'gpu',
      runner: idleRunner,
      messages: [{ role: 'user', content: 'hello' }],
      toolResults: {},
    });
    mockGetRunners.mockResolvedValue({
      runners: [
        {
          ...idleRunner,
          status: 'busy',
          activeRunId: 'run-1',
          activeRunIds: ['run-1', 'run-2'],
        },
      ],
    });

    render(<ChatPage />);

    const meta = await screen.findByTestId('transcript-meta-strip');
    expect(meta).not.toHaveTextContent('kodelet');
    expect(meta).not.toHaveTextContent('active');
    expect(meta).not.toHaveTextContent('gpu');
    fireEvent.click(meta);
    const details = within(screen.getByTestId('transcript-meta-details'));
    expect(details.getByText('Runner').nextElementSibling).toHaveTextContent('kodelet');
    await waitFor(() =>
      expect(details.getByText('Status').nextElementSibling).toHaveTextContent('2 active')
    );
    expect(details.getByText('Runner profile').nextElementSibling).toHaveTextContent('gpu');
    expect(screen.getByTestId('composer-inline-context')).toHaveTextContent(/^Saved settings$/);
  });
});
