import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { act, fireEvent, render, screen, waitFor } from '@testing-library/react';
import ChatPage from './ChatPage';
import type { ChatStreamEvent, ConversationListResponse, Runner, WorkspaceTarget } from '../types';

vi.mock('../components/workspace/TerminalModal', () => ({
  default: ({
    open,
    target,
    allowPopOut = true,
  }: {
    open: boolean;
    target: WorkspaceTarget;
    allowPopOut?: boolean;
  }) =>
    open ? (
      <div
        data-conversation-id={target.kind === 'runner' ? target.conversationId : undefined}
        data-runner-id={target.kind === 'runner' ? target.runnerId : undefined}
        data-show-pop-out={String(
          allowPopOut && (target.kind === 'local' || Boolean(target.conversationId))
        )}
        data-testid="terminal-panel"
      >
        <div className="workspace-terminal-host" data-testid="terminal-host" tabIndex={0}>
          Terminal
        </div>
      </div>
    ) : null,
}));

const mockNavigate = vi.fn();
const mockGetAuthPrincipal = vi.fn();
const mockGetConversations = vi.fn();
const mockGetConversation = vi.fn();
const mockGetChatSettings = vi.fn();
const mockGetCodexProviderStatus = vi.fn();
const mockStartCodexDeviceLogin = vi.fn();
const mockGetCodexDeviceLogin = vi.fn();
const mockCancelCodexDeviceLogin = vi.fn();
const mockGetCopilotProviderStatus = vi.fn();
const mockStartCopilotDeviceLogin = vi.fn();
const mockGetCopilotDeviceLogin = vi.fn();
const mockCancelCopilotDeviceLogin = vi.fn();
const mockGetAnthropicProviderStatus = vi.fn();
const mockStartAnthropicOAuthLogin = vi.fn();
const mockCompleteAnthropicOAuthLogin = vi.fn();
const mockCancelAnthropicOAuthLogin = vi.fn();
const mockGetRunners = vi.fn();
const mockGetSlashCommands = vi.fn();
const mockStreamChat = vi.fn();
const mockStreamConversation = vi.fn();
const mockGetCWDHints = vi.fn();
const mockGetGitDiff = vi.fn();
const mockSteerConversation = vi.fn();
const mockStopConversation = vi.fn();
const mockDeleteConversation = vi.fn();
const mockForkConversation = vi.fn();
const mockRespondToUIInput = vi.fn();
let routeParams: { id?: string } = {};

const makeRunner = (overrides: Partial<Runner> = {}): Runner => ({
  id: 'runner-1',
  displayName: 'kodelet-gpu',
  host: {
    instanceId: 'host-1',
    hostname: 'worker',
    os: 'linux',
    arch: 'amd64',
  },
  workspace: { path: '/runner/kodelet', name: 'kodelet' },
  manifestChanged: false,
  status: 'idle',
  connected: true,
  generation: 1,
  ...overrides,
});

const flushAsyncUpdates = async () => {
  await act(async () => {
    await Promise.resolve();
    await Promise.resolve();
  });
};

const runCwdSuggestionDebounce = async () => {
  await act(async () => {
    vi.advanceTimersByTime(150);
    await Promise.resolve();
    await Promise.resolve();
  });
};

const flushCwdBlurTimer = async () => {
  await act(async () => {
    vi.advanceTimersByTime(120);
    await Promise.resolve();
  });
};

vi.mock('react-router', async () => {
  const actual = await vi.importActual<typeof import('react-router')>('react-router');

  return {
    ...actual,
    useNavigate: () => mockNavigate,
    useParams: () => routeParams,
  };
});

vi.mock('../services/api', () => ({
  default: {
    getAuthPrincipal: (...args: unknown[]) => mockGetAuthPrincipal(...args),
    getConversations: (...args: unknown[]) => mockGetConversations(...args),
    getConversation: (...args: unknown[]) => mockGetConversation(...args),
    getChatSettings: (...args: unknown[]) => mockGetChatSettings(...args),
    getCodexProviderStatus: (...args: unknown[]) => mockGetCodexProviderStatus(...args),
    startCodexDeviceLogin: (...args: unknown[]) => mockStartCodexDeviceLogin(...args),
    getCodexDeviceLogin: (...args: unknown[]) => mockGetCodexDeviceLogin(...args),
    cancelCodexDeviceLogin: (...args: unknown[]) => mockCancelCodexDeviceLogin(...args),
    getCopilotProviderStatus: (...args: unknown[]) => mockGetCopilotProviderStatus(...args),
    startCopilotDeviceLogin: (...args: unknown[]) => mockStartCopilotDeviceLogin(...args),
    getCopilotDeviceLogin: (...args: unknown[]) => mockGetCopilotDeviceLogin(...args),
    cancelCopilotDeviceLogin: (...args: unknown[]) => mockCancelCopilotDeviceLogin(...args),
    getAnthropicProviderStatus: (...args: unknown[]) => mockGetAnthropicProviderStatus(...args),
    startAnthropicOAuthLogin: (...args: unknown[]) => mockStartAnthropicOAuthLogin(...args),
    completeAnthropicOAuthLogin: (...args: unknown[]) => mockCompleteAnthropicOAuthLogin(...args),
    cancelAnthropicOAuthLogin: (...args: unknown[]) => mockCancelAnthropicOAuthLogin(...args),
    getRunners: (...args: unknown[]) => mockGetRunners(...args),
    getSlashCommands: (...args: unknown[]) => mockGetSlashCommands(...args),
    getCWDHints: (...args: unknown[]) => mockGetCWDHints(...args),
    getGitDiff: (...args: unknown[]) => mockGetGitDiff(...args),
    streamChat: (...args: unknown[]) => mockStreamChat(...args),
    streamConversation: (...args: unknown[]) => mockStreamConversation(...args),
    steerConversation: (...args: unknown[]) => mockSteerConversation(...args),
    stopConversation: (...args: unknown[]) => mockStopConversation(...args),
    deleteConversation: (...args: unknown[]) => mockDeleteConversation(...args),
    forkConversation: (...args: unknown[]) => mockForkConversation(...args),
    respondToUIInput: (...args: unknown[]) => mockRespondToUIInput(...args),
  },
}));

describe('ChatPage', () => {
  const waitForTerminalAccess = async () => {
    await waitFor(() => expect(mockGetAuthPrincipal).toHaveBeenCalled());
    await act(async () => {
      await Promise.resolve();
    });
  };

  afterEach(() => {
    vi.unstubAllGlobals();
  });

  beforeEach(() => {
    vi.clearAllMocks();
    routeParams = {};
    window.localStorage.clear();
    window.HTMLElement.prototype.scrollIntoView = vi.fn();
    mockGetAuthPrincipal.mockResolvedValue({ id: 'anonymous', roles: ['admin'] });
    mockGetCodexProviderStatus.mockResolvedValue({ provider: 'codex', connected: false });
    mockCancelCodexDeviceLogin.mockResolvedValue(undefined);
    mockGetCopilotProviderStatus.mockResolvedValue({ provider: 'copilot', connected: false });
    mockCancelCopilotDeviceLogin.mockResolvedValue(undefined);
    mockGetAnthropicProviderStatus.mockResolvedValue({
      provider: 'anthropic',
      connected: false,
    });
    mockCancelAnthropicOAuthLogin.mockResolvedValue(undefined);
    mockGetRunners.mockResolvedValue({ runners: [] });
    mockGetChatSettings.mockImplementation((profile?: string) => {
      const selectedProfile = profile || 'work';
      const reasoningSettings =
        selectedProfile === 'anthropic'
          ? {
              reasoningEffort: 'max',
              reasoningEffortOptions: ['medium', 'high', 'max'],
            }
          : selectedProfile === 'restricted'
            ? {
                reasoningEffort: 'low',
                reasoningEffortOptions: ['low'],
              }
            : {
                reasoningEffort: 'medium',
                reasoningEffortOptions: ['low', 'medium', 'high'],
              };

      return Promise.resolve({
        currentProfile: selectedProfile,
        defaultCWD: '/workspace/default',
        profiles: [
          { name: 'default', scope: 'built-in' },
          { name: 'work', scope: 'repo' },
          { name: 'anthropic', scope: 'global' },
          { name: 'restricted', scope: 'global' },
        ],
        ...reasoningSettings,
      });
    });
    mockGetConversations.mockResolvedValue({
      conversations: [],
      hasMore: false,
      total: 0,
      limit: 10,
      offset: 0,
    });
    mockGetSlashCommands.mockResolvedValue({
      commands: [
        {
          name: 'goal',
          description: 'Set active goal',
          hint: 'objective',
          placeholder: '/goal <objective>',
        },
        {
          name: 'review',
          description: 'Review local git changes',
          hint: '[focus="correctness, tests" target=HEAD] additional instructions',
          placeholder: '/review [focus="correctness, tests" target=HEAD] additional instructions',
        },
        {
          name: 'init',
          description: 'Initialize repository context',
          hint: 'additional instructions (optional)',
        },
        {
          name: 'github/pr',
          description: 'Draft a pull request',
          hint: 'target=main additional instructions',
          placeholder: '/github/pr target=main additional instructions',
        },
        {
          name: 'intro',
          description: 'Write a personal introduction',
          hint: '[name=<value> occupation=<value>] additional instructions',
          placeholder: '/intro [name=<value> occupation=<value>] additional instructions',
        },
      ],
    });
    mockSteerConversation.mockResolvedValue({
      success: true,
      conversation_id: 'conv-123',
      queued: false,
    });
    mockStreamConversation.mockRejectedValue(new Error('conversation is not actively streaming'));
    mockStopConversation.mockResolvedValue({
      success: true,
      conversation_id: 'conv-123',
      stopped: true,
    });
    mockDeleteConversation.mockResolvedValue(undefined);
    mockForkConversation.mockResolvedValue({
      success: true,
      conversation_id: 'conv-copy-123',
    });
    mockRespondToUIInput.mockResolvedValue({ success: true });
    mockGetCWDHints.mockResolvedValue({
      hints: [{ path: '/workspace/default' }],
    });
    mockGetGitDiff.mockResolvedValue({
      cwd: '/workspace/default',
      diff: 'diff --git a/file.txt b/file.txt\n--- a/file.txt\n+++ b/file.txt\n@@ -1 +1 @@\n-old\n+new\n',
      has_diff: true,
      git_root: '/workspace/default',
      exit_code: 0,
    });
  });

  const getGreeting = (): string => {
    const hour = new Date().getHours();
    if (hour < 12) {
      return 'Good morning';
    }
    if (hour < 18) {
      return 'Good afternoon';
    }
    return 'Good evening';
  };

  it('toggles the sidebar shell from the panel controls', async () => {
    render(<ChatPage />);

    await waitFor(() => expect(mockGetConversations).toHaveBeenCalled());
    expect(screen.getAllByText(getGreeting())).toHaveLength(1);
    expect(screen.getByTestId('chat-sidebar-shell')).toBeInTheDocument();
    expect(screen.getByTestId('sidebar-new-chat-button').querySelector('svg')).toHaveClass(
      'lucide-square-pen'
    );
    expect(screen.queryByLabelText('Filter conversations by workspace')).not.toBeInTheDocument();
    const sidebarHideButton = screen.getByTestId('sidebar-hide-button');
    expect(sidebarHideButton).toHaveClass('sidebar-toggle-button');
    expect(sidebarHideButton.querySelector('svg')).toHaveClass('lucide-panel-left');

    fireEvent.click(sidebarHideButton);
    expect(screen.queryByTestId('chat-sidebar-shell')).not.toBeInTheDocument();
    expect(screen.getByTestId('sidebar-collapsed-rail')).toBeInTheDocument();
    expect(screen.getByTestId('sidebar-attached-toggle').querySelector('svg')).toHaveClass(
      'lucide-panel-left'
    );
    expect(screen.getByTestId('sidebar-collapsed-search').querySelector('svg')).toHaveClass(
      'lucide-search'
    );
    expect(screen.getByTestId('sidebar-collapsed-new-chat').querySelector('svg')).toHaveClass(
      'lucide-square-pen'
    );

    fireEvent.click(screen.getByTestId('sidebar-attached-toggle'));
    expect(screen.getByTestId('chat-sidebar-shell')).toBeInTheDocument();
  });

  it('opens the search dialog and new chat from the collapsed sidebar rail', async () => {
    render(<ChatPage />);

    await waitFor(() => expect(mockGetConversations).toHaveBeenCalled());
    fireEvent.click(screen.getByTestId('sidebar-hide-button'));

    fireEvent.click(screen.getByTestId('sidebar-collapsed-search'));
    expect(screen.queryByTestId('chat-sidebar-shell')).not.toBeInTheDocument();
    expect(screen.getByRole('dialog', { name: 'Search conversations' })).toBeInTheDocument();
    await waitFor(() =>
      expect(screen.getByRole('searchbox', { name: 'Search conversations' })).toHaveFocus()
    );

    await act(async () => {
      fireEvent.click(screen.getByRole('button', { name: 'Close conversation search' }));
      await Promise.resolve();
    });
    const newChatButton = screen.getByTestId('sidebar-collapsed-new-chat');
    await waitFor(() => expect(newChatButton).toBeEnabled());
    fireEvent.click(newChatButton);

    expect(screen.getByTestId('new-chat-dialog')).toBeInTheDocument();
  });

  it('returns focus to the mobile sidebar toggle after closing conversation search', async () => {
    vi.stubGlobal(
      'matchMedia',
      vi.fn().mockImplementation((query: string) => ({
        matches: query === '(max-width: 1023px)',
        media: query,
        onchange: null,
        addEventListener: vi.fn(),
        removeEventListener: vi.fn(),
        addListener: vi.fn(),
        removeListener: vi.fn(),
        dispatchEvent: vi.fn(),
      }))
    );

    render(<ChatPage />);

    await waitFor(() => expect(mockGetConversations).toHaveBeenCalled());
    fireEvent.click(screen.getByTestId('sidebar-attached-toggle-mobile'));
    fireEvent.click(screen.getByTestId('sidebar-search-toggle'));
    await waitFor(() =>
      expect(screen.getByRole('searchbox', { name: 'Search conversations' })).toHaveFocus()
    );

    fireEvent.click(screen.getByRole('button', { name: 'Close conversation search' }));

    await waitFor(() => expect(screen.getByTestId('sidebar-attached-toggle-mobile')).toHaveFocus());
  });

  it('shows the account control for an authenticated OIDC session', async () => {
    mockGetAuthPrincipal.mockResolvedValue({
      id: 'https://issuer.example.com|jingkai-he',
      issuer: 'https://issuer.example.com',
      subject: 'jingkai-he',
      name: 'Jingkai He',
      email: 'jingkai@example.com',
      roles: ['user'],
    });

    render(<ChatPage />);

    expect(
      await screen.findByRole('button', { name: 'Jingkai He account menu' })
    ).toHaveTextContent('J He');
  });

  it('opens provider settings from the administrator account menu', async () => {
    mockGetAuthPrincipal.mockResolvedValue({
      id: 'https://issuer.example.com|admin',
      issuer: 'https://issuer.example.com',
      subject: 'admin',
      name: 'Admin User',
      roles: ['admin'],
    });
    render(<ChatPage />);

    fireEvent.click(await screen.findByRole('button', { name: 'Admin User account menu' }));
    const providerSettings = screen.getByRole('menuitem', { name: 'Provider settings' });
    fireEvent.click(providerSettings);

    expect(await screen.findByRole('dialog', { name: 'Provider settings' })).toBeInTheDocument();
    expect(mockGetCodexProviderStatus).toHaveBeenCalledOnce();
    expect(mockGetCopilotProviderStatus).toHaveBeenCalledOnce();
    expect(mockGetAnthropicProviderStatus).toHaveBeenCalledOnce();

    fireEvent.click(screen.getByRole('button', { name: 'Close provider settings' }));
    expect(screen.queryByRole('dialog', { name: 'Provider settings' })).not.toBeInTheDocument();
  });

  it('starts with the sidebar closed on mobile and closes it before opening a new chat', async () => {
    window.localStorage.setItem('kodelet.chat.sidebar.visible', 'true');
    vi.stubGlobal(
      'matchMedia',
      vi.fn().mockImplementation((query: string) => ({
        matches: query === '(max-width: 1023px)',
        media: query,
        onchange: null,
        addEventListener: vi.fn(),
        removeEventListener: vi.fn(),
        addListener: vi.fn(),
        removeListener: vi.fn(),
        dispatchEvent: vi.fn(),
      }))
    );

    render(<ChatPage />);

    await waitFor(() => expect(mockGetConversations).toHaveBeenCalled());
    expect(screen.queryByTestId('chat-sidebar-shell')).not.toBeInTheDocument();
    const mobileSidebarToggle = screen.getByTestId('sidebar-attached-toggle-mobile');
    expect(mobileSidebarToggle).toBeInTheDocument();
    expect(mobileSidebarToggle.querySelector('svg')).toHaveClass('lucide-panel-left');
    expect(window.localStorage.getItem('kodelet.chat.sidebar.visible')).toBe('true');

    await act(async () => {
      fireEvent.click(screen.getByTestId('sidebar-attached-toggle-mobile'));
    });
    expect(screen.getByRole('dialog', { name: 'Conversations' })).toBeInTheDocument();
    expect(screen.getByTestId('chat-sidebar-shell')).toHaveAttribute('tabindex', '-1');
    expect(screen.getByTestId('sidebar-hide-button')).toBeEnabled();
    fireEvent.click(screen.getByTestId('sidebar-hide-button'));
    await waitFor(() => expect(screen.getByTestId('sidebar-attached-toggle-mobile')).toHaveFocus());
    fireEvent.click(screen.getByTestId('sidebar-attached-toggle-mobile'));

    await act(async () => {
      fireEvent.click(screen.getByTestId('sidebar-new-chat-button'));
      await Promise.resolve();
    });
    expect(screen.queryByTestId('chat-sidebar-shell')).not.toBeInTheDocument();
    expect(screen.getByTestId('new-chat-dialog')).toBeInTheDocument();
  });

  it('isolates the mobile workspace sheet without stealing terminal keys', async () => {
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

    render(<ChatPage />);
    await waitFor(() => expect(mockGetConversations).toHaveBeenCalled());
    await waitForTerminalAccess();

    fireEvent.click(screen.getByTestId('workspace-tools-toggle'));
    await screen.findByTestId('terminal-panel');

    const workspaceShell = screen.getByTestId('workspace-tools-shell');
    const workspaceToggle = screen.getByTestId('workspace-tools-toggle');
    expect(workspaceToggle.querySelector('svg')).toHaveClass('lucide-panel-right');
    const terminalTab = screen.getByTestId('workspace-tools-terminal-tab');
    const diffTab = screen.getByTestId('workspace-tools-diff-tab');
    const terminalHost = screen.getByTestId('terminal-host');
    const chatMain = document.querySelector('main.chat-main-panel');
    expect(workspaceShell).toHaveAttribute('role', 'dialog');
    expect(workspaceShell).toHaveAttribute('aria-modal', 'true');
    expect(chatMain).toHaveAttribute('inert');
    expect(chatMain).toHaveAttribute('aria-hidden', 'true');

    workspaceToggle.focus();
    fireEvent.keyDown(window, { key: 'Tab' });
    expect(terminalTab).toHaveFocus();

    terminalTab.focus();
    fireEvent.keyDown(window, { key: 'Tab', shiftKey: true });
    expect(workspaceToggle).toHaveFocus();

    terminalHost.focus();
    expect(fireEvent.keyDown(terminalHost, { key: 'Tab' })).toBe(true);
    expect(terminalHost).toHaveFocus();

    fireEvent.keyDown(terminalHost, { key: 'F6' });
    expect(workspaceToggle).toHaveFocus();

    terminalHost.focus();
    expect(fireEvent.keyDown(terminalHost, { key: 'Tab', shiftKey: true })).toBe(true);
    expect(terminalHost).toHaveFocus();

    fireEvent.keyDown(terminalHost, { key: 'F6', shiftKey: true });
    expect(diffTab).toHaveFocus();
  });

  it('lets keyboard users leave the terminal on wide desktop', async () => {
    vi.stubGlobal(
      'matchMedia',
      vi.fn().mockImplementation((query: string) => ({
        matches: false,
        media: query,
        onchange: null,
        addEventListener: vi.fn(),
        removeEventListener: vi.fn(),
        addListener: vi.fn(),
        removeListener: vi.fn(),
        dispatchEvent: vi.fn(),
      }))
    );

    render(<ChatPage />);
    await waitFor(() => expect(mockGetConversations).toHaveBeenCalled());
    await waitForTerminalAccess();
    fireEvent.click(screen.getByTestId('workspace-tools-toggle'));
    await screen.findByTestId('terminal-panel');

    const terminalHost = screen.getByTestId('terminal-host');
    const workspaceToggle = screen.getByTestId('workspace-tools-toggle');
    const terminalTab = screen.getByTestId('workspace-tools-terminal-tab');
    const diffTab = screen.getByTestId('workspace-tools-diff-tab');
    expect(screen.getByTestId('workspace-tools-shell')).not.toHaveAttribute('role', 'dialog');

    terminalHost.focus();
    expect(fireEvent.keyDown(terminalHost, { key: 'Tab' })).toBe(true);
    expect(terminalHost).toHaveFocus();

    fireEvent.keyDown(terminalHost, { key: 'F6' });
    expect(workspaceToggle).toHaveFocus();

    terminalHost.focus();
    fireEvent.keyDown(terminalHost, { key: 'F6', shiftKey: true });
    expect(diffTab).toHaveFocus();

    fireEvent.click(diffTab);
    await waitFor(() => expect(screen.queryByTestId('terminal-host')).not.toBeInTheDocument());
    fireEvent.click(terminalTab);
    const reopenedTerminalHost = await screen.findByTestId('terminal-host');
    reopenedTerminalHost.focus();
    fireEvent.keyDown(reopenedTerminalHost, { key: 'F6' });
    expect(workspaceToggle).toHaveFocus();
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
    fireEvent.change(screen.getByLabelText('Environment'), {
      target: { value: 'runner-1' },
    });
    fireEvent.click(screen.getByRole('button', { name: 'Start' }));

    if (!terminal && !diff) {
      expect(screen.queryByTestId('workspace-tools-shell')).not.toBeInTheDocument();
      return;
    }

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
    routeParams = { id: 'conv-remote' };
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

  it('remounts the terminal after runner reconnection without changing its workspace target', async () => {
    vi.useFakeTimers();
    routeParams = { id: 'conv-remote' };
    const firstGeneration = makeRunner({
      generation: 1,
      workspaceGitDiff: true,
      workspaceTerminal: true,
    });
    const secondGeneration = makeRunner({
      generation: 2,
      workspaceGitDiff: true,
      workspaceTerminal: true,
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
      const firstTerminal = screen.getByTestId('terminal-panel');
      expect(firstTerminal).toHaveAttribute('data-runner-id', 'runner-1');
      expect(firstTerminal).toHaveAttribute('data-conversation-id', 'conv-remote');

      await act(async () => {
        vi.advanceTimersByTime(5000);
        await Promise.resolve();
        await Promise.resolve();
      });

      expect(mockGetRunners).toHaveBeenCalledTimes(2);
      const reconnectedTerminal = screen.getByTestId('terminal-panel');
      expect(reconnectedTerminal).not.toBe(firstTerminal);
      expect(reconnectedTerminal).toHaveAttribute('data-runner-id', 'runner-1');
      expect(reconnectedTerminal).toHaveAttribute('data-conversation-id', 'conv-remote');
      expect(reconnectedTerminal).toHaveAttribute('data-show-pop-out', 'true');
    } finally {
      vi.useRealTimers();
    }
  });

  it('removes a desktop sidebar from the accessibility tree when the workspace becomes modal', async () => {
    let overlayMatches = false;
    const overlayListeners = new Set<(event: MediaQueryListEvent) => void>();
    const overlayMediaQuery = {
      get matches() {
        return overlayMatches;
      },
      media: '(max-width: 1180px)',
      onchange: null,
      addEventListener: vi.fn((_type: string, listener: (event: MediaQueryListEvent) => void) => {
        overlayListeners.add(listener);
      }),
      removeEventListener: vi.fn(
        (_type: string, listener: (event: MediaQueryListEvent) => void) => {
          overlayListeners.delete(listener);
        }
      ),
      addListener: vi.fn(),
      removeListener: vi.fn(),
      dispatchEvent: vi.fn(),
    };
    vi.stubGlobal(
      'matchMedia',
      vi.fn().mockImplementation((query: string) =>
        query === '(max-width: 1180px)'
          ? overlayMediaQuery
          : {
              matches: false,
              media: query,
              onchange: null,
              addEventListener: vi.fn(),
              removeEventListener: vi.fn(),
              addListener: vi.fn(),
              removeListener: vi.fn(),
              dispatchEvent: vi.fn(),
            }
      )
    );

    render(<ChatPage />);
    await waitFor(() => expect(mockGetConversations).toHaveBeenCalled());
    await waitForTerminalAccess();
    expect(screen.getByTestId('chat-sidebar-shell')).not.toHaveAttribute('inert');

    fireEvent.click(screen.getByTestId('workspace-tools-toggle'));
    await screen.findByTestId('terminal-panel');
    expect(screen.getByTestId('chat-sidebar-shell')).not.toHaveAttribute('inert');

    overlayMatches = true;
    act(() => {
      for (const listener of overlayListeners) {
        listener({ matches: true } as MediaQueryListEvent);
      }
    });

    expect(screen.getByTestId('chat-sidebar-shell')).toHaveAttribute('inert');
    expect(screen.getByTestId('chat-sidebar-shell')).toHaveAttribute('aria-hidden', 'true');
  });

  it('resizes the sidebar width from the transparent edge handle', async () => {
    render(<ChatPage />);

    await waitFor(() => expect(mockGetConversations).toHaveBeenCalled());

    const sidebarShell = screen.getByTestId('chat-sidebar-shell');
    expect(sidebarShell.style.getPropertyValue('--sidebar-width')).toBe('320px');
    expect(screen.getByTestId('chat-sidebar-resizer')).toHaveClass('sidebar-resize-edge');

    fireEvent.mouseDown(screen.getByTestId('chat-sidebar-resizer'), {
      clientX: 320,
    });

    await waitFor(() => expect(document.body.style.cursor).toBe('col-resize'));

    fireEvent.mouseMove(window, { clientX: 420 });
    fireEvent.mouseUp(window);

    await waitFor(() =>
      expect(
        screen.getByTestId('chat-sidebar-shell').style.getPropertyValue('--sidebar-width')
      ).toBe('420px')
    );
  });

  it('includes pasted image attachments in the streamed chat request', async () => {
    mockStreamChat.mockResolvedValue(undefined);

    const fileReaderResult = 'data:image/png;base64,aGVsbG8=';
    const originalFileReader = window.FileReader;

    class MockFileReader {
      result: string | ArrayBuffer | null = null;
      error: DOMException | null = null;
      onload: null | (() => void) = null;
      onerror: null | (() => void) = null;

      readAsDataURL() {
        this.result = fileReaderResult;
        this.onload?.();
      }
    }

    // @ts-expect-error test shim
    window.FileReader = MockFileReader;

    render(<ChatPage />);

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

    window.FileReader = originalFileReader;
  });

  it('submits with Shift+Enter and keeps plain Enter for multiline editing', async () => {
    mockStreamChat.mockResolvedValue(undefined);

    render(<ChatPage />);

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
    render(<ChatPage />);

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

    render(<ChatPage />);

    await waitFor(() => expect(mockGetSlashCommands).toHaveBeenCalled());

    const textarea = screen.getByTestId('composer-textarea');
    fireEvent.change(textarea, { target: { value: '/' } });

    expect(await screen.findByTestId('slash-command-suggestions')).toBeInTheDocument();
    expect(screen.getByText('/review')).toBeInTheDocument();
  });

  it('uses the selected slash command placeholder for argument hints', async () => {
    render(<ChatPage />);

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
    render(<ChatPage />);

    await waitFor(() => expect(mockGetSlashCommands).toHaveBeenCalled());

    const textarea = screen.getByTestId('composer-textarea');
    fireEvent.change(textarea, { target: { value: '/intro ' } });

    expect(screen.getByTestId('composer-slash-usage-hint')).toHaveTextContent(
      '/intro [name=<value> occupation=<value>] additional instructions'
    );
  });

  it('switches the composer layout automatically for multiline drafts', async () => {
    render(<ChatPage />);

    await waitFor(() => expect(mockGetConversations).toHaveBeenCalled());

    const textarea = screen.getByTestId('composer-textarea');
    expect(screen.queryByTestId('composer-expand-toggle')).not.toBeInTheDocument();
    expect(textarea.parentElement).not.toHaveClass('is-multiline');

    fireEvent.change(textarea, { target: { value: 'a\nb\nc' } });

    await waitFor(() => expect(textarea.parentElement).toHaveClass('is-multiline'));
  });

  it('opens terminal in the workspace side panel from the right rail by default', async () => {
    render(<ChatPage />);

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

  it('switches to changes in the workspace side panel', async () => {
    render(<ChatPage />);

    await waitFor(() => expect(mockGetConversations).toHaveBeenCalled());
    await waitFor(() => expect(mockGetChatSettings).toHaveBeenCalled());
    await waitForTerminalAccess();

    fireEvent.click(screen.getByTestId('workspace-tools-toggle'));
    await waitFor(() => expect(screen.getByTestId('terminal-panel')).toBeInTheDocument());
    fireEvent.click(screen.getByTestId('workspace-tools-diff-tab'));

    await waitFor(() =>
      expect(mockGetGitDiff).toHaveBeenCalledWith({ kind: 'local', cwd: '/workspace/default' })
    );
    expect(screen.getByTestId('workspace-tools-dock')).toBeInTheDocument();
    await waitFor(() => expect(screen.getByTestId('git-diff-panel')).toBeInTheDocument());
    expect(screen.queryByRole('heading', { name: 'Changes' })).not.toBeInTheDocument();
    expect(screen.getByTestId('composer-textarea')).toBeInTheDocument();
    expect(screen.queryByTestId('git-diff-modal-backdrop')).not.toBeInTheDocument();
  });

  it('switches and closes the workspace side panel', async () => {
    render(<ChatPage />);

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

  it('omits reasoning effort while initial chat settings are loading', async () => {
    mockGetChatSettings.mockReturnValue(new Promise(() => {}));
    mockStreamChat.mockResolvedValue(undefined);

    render(<ChatPage />);

    fireEvent.change(screen.getByPlaceholderText('Ask kodelet anything...'), {
      target: { value: 'hello' },
    });
    fireEvent.click(screen.getByRole('button', { name: 'Send' }));

    await waitFor(() => expect(mockStreamChat).toHaveBeenCalled());
    expect(mockStreamChat).toHaveBeenCalledWith(
      expect.not.objectContaining({ reasoningEffort: expect.anything() }),
      expect.any(Object)
    );
  });

  it('omits reasoning effort when initial chat settings fail', async () => {
    const consoleError = vi.spyOn(console, 'error').mockImplementation(() => undefined);
    mockGetChatSettings.mockRejectedValue(new Error('settings unavailable'));
    mockStreamChat.mockResolvedValue(undefined);

    try {
      render(<ChatPage />);

      await waitFor(() =>
        expect(consoleError).toHaveBeenCalledWith('Failed to load chat settings', expect.any(Error))
      );
      fireEvent.change(screen.getByPlaceholderText('Ask kodelet anything...'), {
        target: { value: 'hello' },
      });
      fireEvent.click(screen.getByRole('button', { name: 'Send' }));

      await waitFor(() => expect(mockStreamChat).toHaveBeenCalled());
      expect(mockStreamChat).toHaveBeenCalledWith(
        expect.not.objectContaining({ reasoningEffort: expect.anything() }),
        expect.any(Object)
      );
    } finally {
      consoleError.mockRestore();
    }
  });

  it('allows selecting a profile for a new conversation', async () => {
    mockStreamChat.mockResolvedValue(undefined);

    render(<ChatPage />);

    await waitFor(() => expect(mockGetChatSettings).toHaveBeenCalled());
    expect(screen.getByRole('button', { name: 'New Chat' })).toBe(
      screen.getByTestId('sidebar-new-chat-button')
    );
    fireEvent.click(screen.getByTestId('sidebar-new-chat-button'));
    expect(screen.getByTestId('new-chat-dialog')).toBeInTheDocument();
    expect(screen.getByLabelText('Reasoning effort')).toHaveValue('medium');
    fireEvent.change(screen.getByLabelText('Reasoning effort'), {
      target: { value: 'high' },
    });

    fireEvent.change(screen.getByTestId('new-chat-profile-select'), {
      target: { value: 'anthropic' },
    });
    await waitFor(() => expect(mockGetChatSettings).toHaveBeenLastCalledWith('anthropic'));
    await waitFor(() => expect(screen.getByLabelText('Reasoning effort')).toHaveValue('high'));
    fireEvent.change(screen.getByLabelText('Working directory'), {
      target: { value: '/workspace/alt' },
    });

    await waitFor(() => expect(mockGetCWDHints).toHaveBeenCalledWith('/workspace/alt'));
    fireEvent.click(screen.getByRole('button', { name: 'Start' }));

    fireEvent.change(screen.getByPlaceholderText('Ask kodelet anything...'), {
      target: { value: 'hello' },
    });
    fireEvent.click(screen.getByRole('button', { name: 'Send' }));

    await waitFor(() => expect(mockStreamChat).toHaveBeenCalled());
    expect(mockStreamChat).toHaveBeenCalledWith(
      expect.objectContaining({
        profile: 'anthropic',
        reasoningEffort: 'high',
        cwd: '/workspace/alt',
      }),
      expect.any(Object)
    );
  });

  it('resets an unsupported explicit effort when the profile changes', async () => {
    render(<ChatPage />);

    await waitFor(() => expect(mockGetChatSettings).toHaveBeenCalled());
    fireEvent.click(screen.getByTestId('sidebar-new-chat-button'));
    fireEvent.change(screen.getByLabelText('Reasoning effort'), {
      target: { value: 'high' },
    });
    fireEvent.change(screen.getByTestId('new-chat-profile-select'), {
      target: { value: 'restricted' },
    });

    await waitFor(() => expect(screen.getByLabelText('Reasoning effort')).toHaveValue('low'));
    expect(screen.getByLabelText('Reasoning effort')).toBeDisabled();
  });

  it('reverts the profile when its reasoning settings fail to load', async () => {
    const consoleError = vi.spyOn(console, 'error').mockImplementation(() => undefined);
    mockStreamChat.mockResolvedValue(undefined);

    try {
      render(<ChatPage />);

      await waitFor(() => expect(mockGetChatSettings).toHaveBeenCalledTimes(1));
      fireEvent.click(screen.getByTestId('sidebar-new-chat-button'));
      mockGetChatSettings.mockRejectedValueOnce(new Error('profile settings unavailable'));
      fireEvent.change(screen.getByTestId('new-chat-profile-select'), {
        target: { value: 'restricted' },
      });

      await waitFor(() =>
        expect(screen.getByTestId('new-chat-profile-select')).toHaveValue('work')
      );
      expect(screen.getByLabelText('Reasoning effort')).toHaveValue('medium');
      expect(screen.getByRole('button', { name: 'Start' })).toBeEnabled();
      fireEvent.click(screen.getByRole('button', { name: 'Start' }));
      fireEvent.change(screen.getByPlaceholderText('Ask kodelet anything...'), {
        target: { value: 'hello' },
      });
      fireEvent.click(screen.getByRole('button', { name: 'Send' }));

      await waitFor(() => expect(mockStreamChat).toHaveBeenCalled());
      expect(mockStreamChat).toHaveBeenCalledWith(
        expect.objectContaining({
          profile: 'work',
          reasoningEffort: 'medium',
        }),
        expect.any(Object)
      );
    } finally {
      consoleError.mockRestore();
    }
  });

  it('shows cwd suggestions and applies a clicked suggestion', async () => {
    vi.useFakeTimers();

    mockGetCWDHints.mockImplementation((query: string) => {
      if (query === '/workspace/ko') {
        return Promise.resolve({
          hints: [{ path: '/workspace/kodelet' }, { path: '/workspace/koala' }],
        });
      }
      return Promise.resolve({
        hints: [{ path: '/workspace/default' }],
      });
    });
    mockStreamChat.mockResolvedValue(undefined);

    try {
      render(<ChatPage />);
      await flushAsyncUpdates();

      expect(mockGetChatSettings).toHaveBeenCalled();

      fireEvent.click(screen.getByTestId('sidebar-new-chat-button'));
      const cwdInput = screen.getByLabelText('Working directory');
      fireEvent.focus(cwdInput);
      expect(screen.queryByTestId('cwd-suggestions')).not.toBeInTheDocument();
      fireEvent.change(cwdInput, { target: { value: '/workspace/ko' } });
      await runCwdSuggestionDebounce();

      expect(mockGetCWDHints).toHaveBeenLastCalledWith('/workspace/ko');
      expect(screen.getByTestId('cwd-suggestions')).toBeInTheDocument();

      fireEvent.mouseDown(screen.getByTestId('cwd-suggestion-0'));
      fireEvent.click(screen.getByTestId('cwd-suggestion-0'));
      expect(screen.getByTestId('new-chat-dialog')).toBeInTheDocument();
      expect(screen.queryByTestId('cwd-suggestions')).not.toBeInTheDocument();
      expect(mockGetCWDHints).not.toHaveBeenLastCalledWith('/workspace/kodelet');
      fireEvent.click(screen.getByRole('button', { name: 'Start' }));
      expect(screen.queryByTestId('new-chat-dialog')).not.toBeInTheDocument();
      expect(screen.getByText(/workspace\/kodelet/)).toBeInTheDocument();
      await flushCwdBlurTimer();
    } finally {
      vi.useRealTimers();
    }
  });

  it('supports keyboard selection for cwd suggestions', async () => {
    vi.useFakeTimers();

    mockGetCWDHints.mockImplementation((query: string) => {
      if (query === '/workspace/ko') {
        return Promise.resolve({
          hints: [{ path: '/workspace/kodelet' }, { path: '/workspace/koala' }],
        });
      }
      return Promise.resolve({
        hints: [{ path: '/workspace/default' }],
      });
    });

    try {
      render(<ChatPage />);
      await flushAsyncUpdates();

      expect(mockGetChatSettings).toHaveBeenCalled();

      fireEvent.click(screen.getByTestId('sidebar-new-chat-button'));
      const cwdInput = screen.getByLabelText('Working directory');
      fireEvent.focus(cwdInput);
      expect(screen.queryByTestId('cwd-suggestions')).not.toBeInTheDocument();
      fireEvent.change(cwdInput, { target: { value: '/workspace/ko' } });
      await runCwdSuggestionDebounce();

      expect(screen.getByTestId('cwd-suggestions')).toBeInTheDocument();

      fireEvent.keyDown(cwdInput, { key: 'ArrowDown' });
      fireEvent.keyDown(cwdInput, { key: 'Enter' });
      fireEvent.click(screen.getByRole('button', { name: 'Start' }));

      expect(screen.getByText(/workspace\/kodelet/)).toBeInTheDocument();
      await flushCwdBlurTimer();
    } finally {
      vi.useRealTimers();
    }
  });

  it('supports tab completion for cwd suggestions', async () => {
    vi.useFakeTimers();

    mockGetCWDHints.mockImplementation((query: string) => {
      if (query === '/workspace/ko') {
        return Promise.resolve({
          hints: [{ path: '/workspace/kodelet' }, { path: '/workspace/koala' }],
        });
      }
      return Promise.resolve({
        hints: [{ path: '/workspace/default' }],
      });
    });

    try {
      render(<ChatPage />);
      await flushAsyncUpdates();

      expect(mockGetChatSettings).toHaveBeenCalled();

      fireEvent.click(screen.getByTestId('sidebar-new-chat-button'));
      const cwdInput = screen.getByLabelText('Working directory');
      fireEvent.focus(cwdInput);
      fireEvent.change(cwdInput, { target: { value: '/workspace/ko' } });
      await runCwdSuggestionDebounce();

      expect(screen.getByTestId('cwd-suggestions')).toBeInTheDocument();

      fireEvent.keyDown(cwdInput, { key: 'Tab' });
      expect(screen.queryByTestId('cwd-suggestions')).not.toBeInTheDocument();
      expect(cwdInput).toHaveValue('/workspace/kodelet');
      fireEvent.click(screen.getByRole('button', { name: 'Start' }));

      expect(screen.getByText(/workspace\/kodelet/)).toBeInTheDocument();
      await flushCwdBlurTimer();
    } finally {
      vi.useRealTimers();
    }
  });

  it('keeps the latest cwd suggestions when earlier requests resolve later', async () => {
    vi.useFakeTimers();

    const createDeferred = <T,>() => {
      let resolve!: (value: T) => void;
      const promise = new Promise<T>((resolvePromise) => {
        resolve = resolvePromise;
      });
      return { promise, resolve };
    };

    const initialRequest = createDeferred<{ hints: Array<{ path: string }> }>();
    const typedRequest = createDeferred<{ hints: Array<{ path: string }> }>();

    mockGetCWDHints.mockImplementation((query: string) => {
      if (query === '/workspace/default') {
        return initialRequest.promise;
      }
      if (query === '/workspace/ko') {
        return typedRequest.promise;
      }
      return Promise.resolve({ hints: [] });
    });

    try {
      render(<ChatPage />);

      await act(async () => {
        await Promise.resolve();
        await Promise.resolve();
      });
      expect(mockGetChatSettings).toHaveBeenCalled();
      expect(mockGetCWDHints).not.toHaveBeenCalledWith('/workspace/default');

      fireEvent.click(screen.getByTestId('sidebar-new-chat-button'));
      const cwdInput = screen.getByLabelText('Working directory');
      fireEvent.focus(cwdInput);
      fireEvent.change(cwdInput, { target: { value: '/workspace/ko' } });

      await act(async () => {
        vi.runOnlyPendingTimers();
      });

      expect(mockGetCWDHints).toHaveBeenLastCalledWith('/workspace/ko');

      await act(async () => {
        typedRequest.resolve({ hints: [{ path: '/workspace/kodelet' }] });
        await Promise.resolve();
        await Promise.resolve();
      });

      expect(screen.getByText('/workspace/kodelet')).toBeInTheDocument();

      await act(async () => {
        initialRequest.resolve({ hints: [{ path: '/workspace/default' }] });
        await Promise.resolve();
        await Promise.resolve();
      });

      expect(screen.queryByTestId('cwd-suggestion-1')).not.toBeInTheDocument();
      expect(screen.getByTestId('cwd-suggestion-0')).toHaveTextContent('/workspace/kodelet');
    } finally {
      vi.useRealTimers();
    }
  });

  it('submits a relative directory typed naturally', async () => {
    mockStreamChat.mockResolvedValue(undefined);

    render(<ChatPage />);

    await waitFor(() => expect(mockGetChatSettings).toHaveBeenCalled());

    fireEvent.click(screen.getByTestId('sidebar-new-chat-button'));
    fireEvent.change(screen.getByLabelText('Working directory'), {
      target: { value: 'kodelet-website' },
    });
    fireEvent.click(screen.getByRole('button', { name: 'Start' }));
    fireEvent.change(screen.getByPlaceholderText('Ask kodelet anything...'), {
      target: { value: 'hello' },
    });
    fireEvent.click(screen.getByRole('button', { name: 'Send' }));

    await waitFor(() => expect(mockStreamChat).toHaveBeenCalled());
    expect(mockStreamChat).toHaveBeenCalledWith(
      expect.objectContaining({ cwd: 'kodelet-website' }),
      expect.any(Object)
    );
  });

  it('opens and closes the new chat settings dialog', async () => {
    render(<ChatPage />);

    await waitFor(() => expect(mockGetChatSettings).toHaveBeenCalled());

    fireEvent.click(screen.getByTestId('sidebar-new-chat-button'));
    expect(screen.getByTestId('new-chat-dialog')).toBeInTheDocument();
    await waitFor(() => expect(screen.getByLabelText('Working directory')).toHaveFocus());
    const closeButton = screen.getByRole('button', {
      name: 'Close new chat dialog',
    });
    const startButton = screen.getByRole('button', { name: 'Start' });
    startButton.focus();
    fireEvent.keyDown(startButton, { key: 'Tab' });
    expect(closeButton).toHaveFocus();
    fireEvent.keyDown(closeButton, { key: 'Tab', shiftKey: true });
    expect(startButton).toHaveFocus();
    expect(screen.queryByTestId('cwd-suggestions')).not.toBeInTheDocument();
    expect(screen.queryByText('Type a full path or nearby project name.')).not.toBeInTheDocument();

    await new Promise((resolve) => window.setTimeout(resolve, 200));
    expect(mockGetCWDHints).not.toHaveBeenCalledWith('/workspace/default');

    fireEvent.click(screen.getByRole('button', { name: 'Cancel' }));
    expect(screen.queryByTestId('new-chat-dialog')).not.toBeInTheDocument();
  });

  it('selects a remote runner and omits control-plane cwd features', async () => {
    mockGetRunners.mockResolvedValue({
      runners: [makeRunner()],
    });
    mockStreamChat.mockResolvedValue(undefined);

    render(<ChatPage />);
    await waitFor(() => expect(mockGetRunners).toHaveBeenCalled());
    await waitForTerminalAccess();
    fireEvent.click(screen.getByTestId('sidebar-new-chat-button'));
    fireEvent.change(screen.getByLabelText('Environment'), {
      target: { value: 'runner-1' },
    });
    fireEvent.change(screen.getByLabelText('Runner profile'), {
      target: { value: 'gpu' },
    });
    expect(screen.getByText('/runner/kodelet')).toBeVisible();
    expect(screen.queryByLabelText('Working directory')).not.toBeInTheDocument();
    fireEvent.click(screen.getByRole('button', { name: 'Start' }));
    expect(screen.queryByTestId('workspace-tools-shell')).not.toBeInTheDocument();

    fireEvent.change(screen.getByPlaceholderText('Ask kodelet anything...'), {
      target: { value: 'hello remotely' },
    });
    fireEvent.click(screen.getByRole('button', { name: 'Send' }));

    await waitFor(() => expect(mockStreamChat).toHaveBeenCalled());
    expect(mockStreamChat).toHaveBeenCalledWith(
      expect.objectContaining({
        runnerId: 'runner-1',
        environmentProfile: 'gpu',
        clientCapabilities: {
          interactiveUI: true,
          persistentWidgets: true,
          persistentSurfaces: false,
        },
      }),
      expect.any(Object)
    );
    expect(mockStreamChat).toHaveBeenCalledWith(
      expect.not.objectContaining({ cwd: expect.anything() }),
      expect.any(Object)
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
    fireEvent.change(screen.getByLabelText('Environment'), {
      target: { value: 'runner-1' },
    });
    fireEvent.click(screen.getByRole('button', { name: 'Start' }));

    expect(screen.getByTestId('workspace-tools-shell')).toBeInTheDocument();
    fireEvent.click(screen.getByTestId('workspace-tools-toggle'));

    const terminal = await screen.findByTestId('terminal-panel');
    expect(terminal).toHaveAttribute('data-runner-id', 'runner-1');
    expect(terminal).toHaveAttribute('data-show-pop-out', 'false');

    fireEvent.click(screen.getByTestId('workspace-tools-diff-tab'));
    await waitFor(() =>
      expect(mockGetGitDiff).toHaveBeenCalledWith({
        kind: 'runner',
        runnerId: 'runner-1',
      })
    );
  });

  it('enables terminal pop-out for an established remote conversation', async () => {
    routeParams = { id: 'conv-remote' };
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
  });

  it('uses the runner-only workspace target while a new remote conversation is pending', async () => {
    mockGetRunners.mockResolvedValue({
      runners: [makeRunner({ workspaceGitDiff: true, workspaceTerminal: true })],
    });
    mockStreamChat.mockImplementation(async () => new Promise(() => undefined));

    const { rerender } = render(<ChatPage />);
    await waitFor(() => expect(mockGetRunners).toHaveBeenCalled());
    await waitForTerminalAccess();
    fireEvent.click(screen.getByTestId('sidebar-new-chat-button'));
    fireEvent.change(screen.getByLabelText('Environment'), {
      target: { value: 'runner-1' },
    });
    fireEvent.click(screen.getByRole('button', { name: 'Start' }));
    fireEvent.change(screen.getByPlaceholderText('Ask kodelet anything...'), {
      target: { value: 'hello remotely' },
    });
    fireEvent.click(screen.getByRole('button', { name: 'Send' }));

    await waitFor(() => expect(mockStreamChat).toHaveBeenCalled());
    const preallocatedId = mockStreamChat.mock.calls[0]?.[0]?.conversationId;
    expect(preallocatedId).toBeTruthy();
    routeParams = { id: preallocatedId };
    rerender(<ChatPage />);

    fireEvent.click(screen.getByTestId('workspace-tools-toggle'));
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
  });

  it('retains the remote runner binding when retrying a failed optimistic conversation', async () => {
    mockGetRunners.mockResolvedValue({
      runners: [makeRunner({ workspaceGitDiff: true, workspaceTerminal: true })],
    });
    mockStreamChat
      .mockRejectedValueOnce(new Error('runner unavailable'))
      .mockImplementationOnce(async () => new Promise(() => undefined));

    const { rerender } = render(<ChatPage />);
    await waitFor(() => expect(mockGetRunners).toHaveBeenCalled());
    fireEvent.click(screen.getByTestId('sidebar-new-chat-button'));
    fireEvent.change(screen.getByLabelText('Environment'), {
      target: { value: 'runner-1' },
    });
    fireEvent.click(screen.getByRole('button', { name: 'Start' }));
    fireEvent.change(screen.getByPlaceholderText('Ask kodelet anything...'), {
      target: { value: 'first attempt' },
    });
    fireEvent.click(screen.getByRole('button', { name: 'Send' }));

    await waitFor(() => expect(mockStreamChat).toHaveBeenCalledTimes(1));
    const preallocatedId = mockStreamChat.mock.calls[0]?.[0]?.conversationId;
    expect(preallocatedId).toBeTruthy();
    routeParams = { id: preallocatedId };
    rerender(<ChatPage />);
    await waitFor(() =>
      expect(screen.getAllByText('runner unavailable').length).toBeGreaterThan(0)
    );
    expect(mockGetConversation).not.toHaveBeenCalledWith(preallocatedId);

    fireEvent.change(screen.getByPlaceholderText('Ask kodelet anything...'), {
      target: { value: 'retry' },
    });
    fireEvent.click(screen.getByRole('button', { name: 'Send' }));

    await waitFor(() => expect(mockStreamChat).toHaveBeenCalledTimes(2));
    expect(mockStreamChat.mock.calls[1]?.[0]).toEqual(
      expect.objectContaining({
        conversationId: preallocatedId,
        runnerId: 'runner-1',
      })
    );
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
    fireEvent.change(screen.getByLabelText('Environment'), {
      target: { value: 'runner-1' },
    });
    fireEvent.click(screen.getByRole('button', { name: 'Start' }));
    fireEvent.change(screen.getByPlaceholderText('Ask kodelet anything...'), {
      target: { value: 'first message' },
    });
    fireEvent.click(screen.getByRole('button', { name: 'Send' }));

    await waitFor(() => expect(mockStreamChat).toHaveBeenCalledTimes(1));
    const conversationId = mockStreamChat.mock.calls[0]?.[0]?.conversationId;
    expect(conversationId).toBeTruthy();
    await waitFor(() => expect(mockGetConversation).toHaveBeenCalledWith(conversationId));

    routeParams = { id: conversationId };
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
    fireEvent.change(screen.getByLabelText('Environment'), {
      target: { value: 'runner-1' },
    });
    fireEvent.click(screen.getByRole('button', { name: 'Start' }));
    fireEvent.click(screen.getByTestId('workspace-tools-toggle'));

    expect(screen.queryByTestId('workspace-tools-terminal-tab')).not.toBeInTheDocument();
    expect(screen.getByTestId('workspace-tools-diff-tab')).toBeInTheDocument();
    await screen.findByTestId('git-diff-panel');
    await waitFor(() => expect(mockGetGitDiff).toHaveBeenCalled());
  });

  it('hides stale remote workspace tools while a different conversation is loading', async () => {
    routeParams = { id: 'conv-remote' };
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

    routeParams = { id: 'conv-local' };
    rerender(<ChatPage />);

    await waitFor(() => expect(mockGetConversation).toHaveBeenCalledWith('conv-local'));
    expect(screen.queryByTestId('workspace-tools-shell')).not.toBeInTheDocument();

    await act(async () => {
      resolveLocalConversation?.({
        id: 'conv-local',
        createdAt: '2026-08-19T00:00:00Z',
        updatedAt: '2026-08-19T00:00:00Z',
        messageCount: 1,
        cwd: '/workspace/local',
        messages: [{ role: 'user', content: 'local' }],
        toolResults: {},
      });
      await Promise.resolve();
    });
  });

  it('requires a workspace runner when the control-plane workspace is disabled', async () => {
    mockGetChatSettings.mockResolvedValue({
      currentProfile: 'work',
      controlPlaneWorkspaceEnabled: false,
      profiles: [
        { name: 'default', scope: 'built-in' },
        { name: 'work', scope: 'repo' },
      ],
      reasoningEffort: 'medium',
      reasoningEffortOptions: ['low', 'medium', 'high'],
    });
    mockGetRunners.mockResolvedValue({
      runners: [
        makeRunner({
          id: 'runner-required',
          displayName: 'required-runner',
          host: {
            instanceId: 'host-required',
            hostname: 'worker',
            os: 'linux',
            arch: 'amd64',
          },
          workspace: { path: '/runner/required', name: 'required' },
        }),
      ],
    });

    render(<ChatPage />);

    await waitFor(() => expect(mockGetChatSettings).toHaveBeenCalled());
    expect(
      screen.getByText(
        'The control-plane workspace is disabled. Select a workspace runner to start a chat.'
      )
    ).toBeVisible();
    expect(screen.getByTestId('composer-textarea')).toBeDisabled();
    expect(screen.queryByTestId('workspace-tools-shell')).not.toBeInTheDocument();

    fireEvent.click(screen.getByTestId('sidebar-new-chat-button'));
    expect(screen.getByRole('option', { name: 'Select a workspace runner' })).toBeDisabled();
    expect(screen.queryByLabelText('Working directory')).not.toBeInTheDocument();
    expect(screen.getByRole('button', { name: 'Start' })).toBeDisabled();

    fireEvent.change(screen.getByLabelText('Environment'), {
      target: { value: 'runner-required' },
    });
    expect(screen.getByRole('button', { name: 'Start' })).toBeEnabled();
    fireEvent.click(screen.getByRole('button', { name: 'Start' }));

    expect(screen.getByTestId('composer-textarea')).toBeEnabled();
    expect(
      screen.queryByText(
        'The control-plane workspace is disabled. Select a workspace runner to start a chat.'
      )
    ).not.toBeInTheDocument();
    expect(screen.queryByTestId('workspace-tools-shell')).not.toBeInTheDocument();
  });

  it('shows recent workspaces and applies a selected workspace', async () => {
    mockGetConversations.mockResolvedValue({
      conversations: [
        {
          id: 'conv-1',
          createdAt: '2023-01-01T00:00:00Z',
          updatedAt: '2023-01-06T00:00:00Z',
          messageCount: 1,
          cwd: '/workspace/a',
        },
        {
          id: 'conv-2',
          createdAt: '2023-01-01T00:00:00Z',
          updatedAt: '2023-01-05T00:00:00Z',
          messageCount: 1,
          cwd: '/workspace/b',
        },
        {
          id: 'conv-3',
          createdAt: '2023-01-01T00:00:00Z',
          updatedAt: '2023-01-04T00:00:00Z',
          messageCount: 1,
          cwd: '/workspace/c',
        },
        {
          id: 'conv-4',
          createdAt: '2023-01-01T00:00:00Z',
          updatedAt: '2023-01-03T00:00:00Z',
          messageCount: 1,
          cwd: '/workspace/d',
        },
        {
          id: 'conv-5',
          createdAt: '2023-01-01T00:00:00Z',
          updatedAt: '2023-01-02T00:00:00Z',
          messageCount: 1,
          cwd: '/workspace/e',
        },
        {
          id: 'conv-6',
          createdAt: '2023-01-01T00:00:00Z',
          updatedAt: '2023-01-01T00:00:00Z',
          messageCount: 1,
          cwd: '/workspace/f',
        },
      ],
      hasMore: false,
      total: 6,
      limit: 10,
      offset: 0,
    });

    render(<ChatPage />);

    await waitFor(() => {
      expect(mockGetConversations).toHaveBeenCalled();
      expect(mockGetChatSettings).toHaveBeenCalled();
    });
    fireEvent.click(screen.getByTestId('sidebar-new-chat-button'));

    expect(screen.getByTestId('recent-workspaces')).toBeInTheDocument();
    await waitFor(() => expect(screen.getByLabelText('Working directory')).toHaveFocus());
    expect(screen.getByRole('button', { name: '/workspace/a' })).toBeInTheDocument();
    expect(screen.getByRole('button', { name: '/workspace/e' })).toBeInTheDocument();
    expect(screen.queryByRole('button', { name: '/workspace/f' })).not.toBeInTheDocument();

    fireEvent.click(screen.getByRole('button', { name: '/workspace/b' }));
    await waitFor(() => {
      expect(screen.getByLabelText('Working directory')).toHaveValue('/workspace/b');
    });
  });

  it('cancels pending cwd suggestions when applying a recent workspace', async () => {
    vi.useFakeTimers();

    mockGetConversations.mockResolvedValue({
      conversations: [
        {
          id: 'conv-1',
          createdAt: '2023-01-01T00:00:00Z',
          updatedAt: '2023-01-06T00:00:00Z',
          messageCount: 1,
          cwd: '/workspace/recent',
        },
      ],
      hasMore: false,
      total: 1,
      limit: 10,
      offset: 0,
    });
    mockGetCWDHints.mockResolvedValue({
      hints: [{ path: '/workspace/kodelet' }],
    });

    try {
      render(<ChatPage />);

      await act(async () => {
        await Promise.resolve();
        await Promise.resolve();
      });

      fireEvent.click(screen.getByTestId('sidebar-new-chat-button'));
      const cwdInput = screen.getByLabelText('Working directory');
      fireEvent.focus(cwdInput);
      fireEvent.change(cwdInput, { target: { value: '/workspace/ko' } });
      fireEvent.click(screen.getByRole('button', { name: '/workspace/recent' }));

      await act(async () => {
        vi.advanceTimersByTime(150);
        await Promise.resolve();
        await Promise.resolve();
      });

      expect(mockGetCWDHints).not.toHaveBeenCalledWith('/workspace/ko');
      expect(cwdInput).toHaveValue('/workspace/recent');
      expect(screen.queryByTestId('cwd-suggestions')).not.toBeInTheDocument();
    } finally {
      vi.useRealTimers();
    }
  });

  it('shows the cwd inside the inline context for existing conversations', async () => {
    routeParams = { id: 'conv-123' };
    mockGetConversation.mockResolvedValue({
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
    });

    render(<ChatPage />);

    await waitFor(() => expect(mockGetConversation).toHaveBeenCalledWith('conv-123'));

    await waitFor(() =>
      expect(screen.getByTestId('composer-inline-context')).toHaveTextContent('/workspace/project')
    );
    expect(screen.queryByLabelText('Working directory')).not.toBeInTheDocument();
  });

  it('uses the live runner status instead of the conversation runner snapshot', async () => {
    routeParams = { id: 'conv-123' };
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

    await waitFor(() => expect(mockGetConversation).toHaveBeenCalledWith('conv-123'));
    await waitFor(() =>
      expect(screen.getByTestId('transcript-meta-strip')).toHaveTextContent(
        'runner:kodelet (2 active)'
      )
    );
    expect(screen.getByTestId('transcript-meta-strip')).toHaveTextContent('env:gpu');
    expect(screen.getByTestId('transcript-meta-strip')).not.toHaveTextContent(
      'runner:kodelet (idle)'
    );
    expect(screen.getByTestId('composer-inline-context')).not.toHaveTextContent('runner:kodelet');
    expect(screen.getByTestId('composer-inline-context')).not.toHaveTextContent('env:gpu');
  });

  it('shows the profile inside the inline context for existing conversations', async () => {
    routeParams = { id: 'conv-123' };
    mockGetConversation.mockResolvedValue({
      id: 'conv-123',
      createdAt: '2023-01-01T00:00:00Z',
      updatedAt: '2023-01-02T00:00:00Z',
      messageCount: 1,
      profile: 'anthropic',
      profileLocked: true,
      reasoningEffort: 'high',
      reasoningEffortLocked: true,
      messages: [
        {
          role: 'user',
          content: 'hello',
        },
      ],
      toolResults: {},
    });
    mockStreamChat.mockResolvedValue(undefined);

    render(<ChatPage />);

    await waitFor(() => expect(mockGetConversation).toHaveBeenCalledWith('conv-123'));

    expect(screen.getByTestId('composer-inline-context')).toBeInTheDocument();
    expect(screen.getByTestId('composer-inline-context')).toHaveTextContent('anthropic');
    expect(screen.getByTestId('composer-inline-context')).toHaveTextContent('effort:high');
    expect(screen.queryByLabelText('Profile')).not.toBeInTheDocument();
    expect(screen.queryByLabelText('Reasoning effort')).not.toBeInTheDocument();

    fireEvent.change(screen.getByPlaceholderText('Ask kodelet anything...'), {
      target: { value: 'continue' },
    });
    fireEvent.click(screen.getByRole('button', { name: 'Send' }));

    await waitFor(() => expect(mockStreamChat).toHaveBeenCalled());
    expect(mockStreamChat).toHaveBeenCalledWith(
      expect.not.objectContaining({ profile: expect.anything() }),
      expect.any(Object)
    );
    expect(mockStreamChat).toHaveBeenCalledWith(
      expect.not.objectContaining({ reasoningEffort: expect.anything() }),
      expect.any(Object)
    );
  });

  it('streams a future TUI turn into an already-open conversation', async () => {
    routeParams = { id: 'conv-123' };
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

  it('restores persistent extension widgets from the conversation stream', async () => {
    routeParams = { id: 'conv-123' };
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

    render(<ChatPage />);

    await waitFor(() => expect(streamListener).not.toBeNull());
    await act(async () => {
      streamListener?.({
        kind: 'ui-widgets',
        conversation_id: 'conv-123',
        ui_widgets: [
          {
            key: 'subagent-widget',
            extension_id: 'subagent',
            id: 'background-agents',
            placement: 'aboveComposer',
            frame: {
              sequence: 4,
              lines: [
                {
                  spans: [
                    { text: 'Background agents', style: { bold: true } },
                    { text: '  1 active', style: { dim: true } },
                  ],
                },
                '● Inspect authentication  running',
              ],
            },
          },
        ],
      });
    });

    expect(screen.getByTestId('extension-widgets-aboveComposer')).toBeInTheDocument();
    expect(screen.getByTestId('extension-widget-subagent-widget')).toHaveClass(
      'extension-widget-frame'
    );
    expect(screen.getByText(/Background agents/).closest('.extension-widget-line')).toHaveClass(
      'extension-widget-line-header'
    );
    expect(screen.getByText(/Inspect authentication/)).toBeInTheDocument();
    const widgetToggle = screen.getByRole('button', { name: /Background agents/ });
    expect(widgetToggle).toHaveAttribute('aria-expanded', 'true');
    fireEvent.click(widgetToggle);
    expect(widgetToggle).toHaveAttribute('aria-expanded', 'false');
    expect(screen.queryByText(/Inspect authentication/)).not.toBeInTheDocument();
    fireEvent.click(widgetToggle);
    expect(screen.getByText(/Inspect authentication/)).toBeInTheDocument();

    await act(async () => {
      streamListener?.({
        kind: 'ui-widget',
        conversation_id: 'conv-123',
        ui_widget: {
          key: 'subagent-widget',
          extension_id: 'subagent',
          generation: '0:2',
          id: 'background-agents',
          placement: 'aboveComposer',
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
          placement: 'aboveComposer',
          frame: { sequence: 5, lines: [] },
          removed: true,
        },
      });
    });

    expect(screen.getByText('Restarted agent generation')).toBeInTheDocument();

    await act(async () => {
      streamListener?.({
        kind: 'ui-widget',
        conversation_id: 'conv-123',
        ui_widget: {
          key: 'subagent-widget',
          extension_id: 'subagent',
          generation: '0:2',
          id: 'background-agents',
          placement: 'aboveComposer',
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
          placement: 'aboveComposer',
          frame: { sequence: 2, lines: ['Delayed stale update'] },
        },
      });
    });

    expect(screen.queryByText('Restarted agent generation')).not.toBeInTheDocument();
    expect(screen.queryByText('Delayed stale update')).not.toBeInTheDocument();
  });

  it('keeps a background-agent widget visible after the parent turn completes', async () => {
    routeParams = { id: 'conv-123' };
    const streamListeners: Array<(event: ChatStreamEvent) => void> = [];
    mockGetConversation.mockResolvedValue({
      id: 'conv-123',
      createdAt: '2026-08-29T00:00:00Z',
      updatedAt: '2026-08-29T00:00:00Z',
      messageCount: 1,
      messages: [],
      toolResults: {},
    });
    mockStreamConversation.mockImplementation(async (_id, options) => {
      streamListeners.push(
        (options as { onEvent: (event: ChatStreamEvent) => void }).onEvent
      );
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
    fireEvent.change(screen.getByPlaceholderText('Ask kodelet anything...'), {
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

  it('allows steering a TUI-started conversation while its remote runner is busy', async () => {
    routeParams = { id: 'conv-123' };
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

    await waitFor(() => expect(mockGetConversation).toHaveBeenCalledWith('conv-123'));
    await waitFor(() =>
      expect(screen.getByTestId('transcript-meta-strip')).toHaveTextContent(
        'runner:kodelet (1 active)'
      )
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
    routeParams = { id: 'conv-123' };
    mockGetConversation.mockResolvedValue({
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

    expect(await screen.findByTestId('pending-steer-list')).toBeInTheDocument();
    expect(screen.getByText('Focus on tests')).toBeInTheDocument();
    expect(screen.getByTestId('pending-steer-list')).not.toHaveTextContent('You');

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

  it('includes image attachments when queueing steering', async () => {
    routeParams = { id: 'conv-123' };
    mockGetConversation.mockResolvedValue({
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

    const fileReaderResult = 'data:image/png;base64,aGVsbG8=';
    const originalFileReader = window.FileReader;

    class MockFileReader {
      result: string | ArrayBuffer | null = null;
      error: DOMException | null = null;
      onload: null | (() => void) = null;
      onerror: null | (() => void) = null;

      readAsDataURL() {
        this.result = fileReaderResult;
        this.onload?.();
      }
    }

    // @ts-expect-error test shim
    window.FileReader = MockFileReader;

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

    window.FileReader = originalFileReader;
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

    render(<ChatPage />);

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

    const { rerender } = render(<ChatPage />);

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

    routeParams = { id: 'conv-456' };
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

    const { rerender } = render(<ChatPage />);

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

    routeParams = { id: 'conv-456' };
    rerender(<ChatPage />);

    await waitFor(() => expect(mockGetConversation).toHaveBeenCalledWith('conv-456'));

    expect(screen.getByTestId('conversation-running-indicator-conv-123')).toBeInTheDocument();
    expect(screen.getByTestId('conversation-row-conv-123')).toHaveClass('running');
    expect(screen.queryByTestId('conversation-running-indicator-conv-456')).not.toBeInTheDocument();
  });

  it('shows running indicators from the conversation list after refresh', async () => {
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
          summary: 'Idle task',
        },
      ],
      hasMore: false,
      total: 2,
      limit: 40,
      offset: 0,
    });
    mockStreamConversation.mockImplementation(async () => new Promise(() => undefined));

    render(<ChatPage />);

    await waitFor(() =>
      expect(screen.getByTestId('conversation-running-indicator-conv-123')).toBeInTheDocument()
    );
    expect(screen.queryByTestId('conversation-running-indicator-conv-456')).not.toBeInTheDocument();
    expect(mockStreamConversation).toHaveBeenCalledWith('conv-123', expect.any(Object));
  });

  it('clears a background running indicator when the stream finishes', async () => {
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

    await waitFor(() =>
      expect(screen.getByTestId('conversation-running-indicator-conv-123')).toBeInTheDocument()
    );

    await act(async () => {
      streamOptions?.onEvent({
        kind: 'done',
        conversation_id: 'conv-123',
      });
    });

    await waitFor(() =>
      expect(
        screen.queryByTestId('conversation-running-indicator-conv-123')
      ).not.toBeInTheDocument()
    );
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

    routeParams = { id: 'conv-456' };
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

    routeParams = { id: 'conv-123' };
    const { rerender } = render(<ChatPage />);

    await waitFor(() =>
      expect(mockStreamConversation).toHaveBeenCalledWith('conv-123', expect.any(Object))
    );

    fireEvent.click(screen.getByText('Other task'));
    expect(mockNavigate).toHaveBeenCalledWith('/c/conv-456');

    routeParams = { id: 'conv-456' };
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

  it('shows blocking UI prompts from a local stream after switching conversations', async () => {
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

    const { rerender } = render(<ChatPage />);

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

    routeParams = { id: 'conv-456' };
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

  it('clears selected conversation running state when stream attach is stale', async () => {
    routeParams = { id: 'conv-123' };
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

    const { rerender } = render(<ChatPage />);

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

    routeParams = { id: 'conv-456' };
    rerender(<ChatPage />);

    await waitFor(() => expect(mockGetConversation).toHaveBeenCalledWith('conv-456'));

    expect(screen.queryByRole('button', { name: 'Stop' })).not.toBeInTheDocument();
    expect(screen.getByRole('button', { name: 'Send' })).toBeInTheDocument();

    fireEvent.change(screen.getByPlaceholderText('Ask kodelet anything...'), {
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

    const { rerender } = render(<ChatPage />);

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

    routeParams = { id: 'conv-456' };
    rerender(<ChatPage />);

    await waitFor(() => expect(mockGetConversation).toHaveBeenCalledWith('conv-456'));
    expect(screen.getAllByRole('button', { name: /Brand new task/i })[0]).toBeInTheDocument();
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

    await waitFor(() => expect(mockGetConversations).toHaveBeenCalled());

    fireEvent.click(
      screen.getByRole('button', {
        name: /More actions for Enabled resumable webUI conversation/i,
      })
    );
    fireEvent.click(screen.getByRole('menuitem', { name: 'Copy' }));

    await waitFor(() => expect(mockForkConversation).toHaveBeenCalledWith('conv-123'));
    await waitFor(() => expect(mockNavigate).toHaveBeenCalledWith('/c/conv-copy-123'));
  });

  it('deletes the active conversation from the sidebar menu', async () => {
    routeParams = { id: 'conv-123' };
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

  it('queues steering immediately while a conversation is streaming', async () => {
    routeParams = { id: 'conv-123' };
    mockGetConversation.mockResolvedValue({
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

  it('preallocates a conversation id for a new conversation', async () => {
    mockStreamChat.mockImplementation(async () => new Promise(() => undefined));

    render(<ChatPage />);

    await waitFor(() => expect(mockGetConversations).toHaveBeenCalled());

    fireEvent.change(screen.getByPlaceholderText('Ask kodelet anything...'), {
      target: { value: 'hello' },
    });
    fireEvent.click(screen.getByRole('button', { name: 'Send' }));

    await waitFor(() => expect(mockStreamChat).toHaveBeenCalled());

    const preallocatedId = mockStreamChat.mock.calls[0]?.[0]?.conversationId;
    expect(preallocatedId).toMatch(/^\d{8}T\d{6}-[a-f0-9]{16}$/);
    expect(screen.getByRole('button', { name: 'Stop' })).toBeEnabled();
    expect(mockNavigate).toHaveBeenCalledWith(`/c/${preallocatedId}`, {
      replace: true,
    });
    await waitFor(() => expect(screen.getByTestId('sidebar-new-chat-button')).toBeEnabled());
    expect(mockStopConversation).not.toHaveBeenCalled();
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

    render(<ChatPage />);

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
    fireEvent.change(screen.getByLabelText('Working directory'), {
      target: { value: '/workspace/alt' },
    });
    await waitFor(() => expect(mockGetCWDHints).toHaveBeenCalledWith('/workspace/alt'));
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

    routeParams = { id: 'conv-123' };
    rerender(<ChatPage />);

    fireEvent.click(screen.getByTestId('workspace-tools-toggle'));
    fireEvent.click(screen.getByTestId('workspace-tools-diff-tab'));

    await waitFor(() =>
      expect(mockGetGitDiff).toHaveBeenCalledWith({ kind: 'local', cwd: '/workspace/alt' })
    );
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

      expect(mockGetConversations).toHaveBeenCalledWith({
        limit: 100,
        sortBy: 'updated',
        sortOrder: 'desc',
      });

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

  it('shows a compact home cwd label in recent chats and hides sidebar metadata', async () => {
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

    routeParams = { id: 'conv-123' };
    render(<ChatPage />);

    await waitFor(() => expect(mockGetConversations).toHaveBeenCalled());
    expect(screen.getByRole('button', { name: /~\/workspace\/kodelet 1/i })).toBeInTheDocument();
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

  it('shows compact new chat context text in the composer', async () => {
    mockGetChatSettings.mockResolvedValue({
      currentProfile: 'work',
      defaultCWD: '~/workspace/kodelet',
      profiles: [
        { name: 'default', scope: 'built-in' },
        { name: 'work', scope: 'repo' },
      ],
      reasoningEffort: 'medium',
      reasoningEffortOptions: ['low', 'medium', 'high'],
    });
    render(<ChatPage />);

    await waitFor(() => expect(mockGetChatSettings).toHaveBeenCalled());
    expect(screen.getByText(/work · effort:medium · ~\/workspace\/kodelet/)).toBeInTheDocument();
    expect(screen.queryByText('Shift+Enter to send')).not.toBeInTheDocument();
    expect(screen.getByRole('button', { name: 'Send' })).toHaveAttribute(
      'title',
      'Send (Shift+Enter)'
    );
    fireEvent.click(screen.getByText(/work · effort:medium · ~\/workspace\/kodelet/));
    const cwdInput = screen.getByLabelText('Working directory');
    await waitFor(() => {
      expect(cwdInput).toHaveValue('~/workspace/kodelet');
      expect(cwdInput).toHaveFocus();
    });
  });

  it('shows compact usage metadata below the transcript when available', async () => {
    routeParams = { id: 'conv-123' };
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
        inputCost: 0,
        outputCost: 0,
        cacheCreationCost: 0,
        cacheReadCost: 0,
      },
    });

    render(<ChatPage />);

    await waitFor(() => expect(mockGetConversation).toHaveBeenCalledWith('conv-123'));

    const meta = screen.getByTestId('transcript-meta-strip');
    expect(meta).toHaveTextContent('14.2K/272K (5%) context');
    expect(meta).toHaveTextContent('in 1.2K');
    expect(meta).toHaveTextContent('out 340');
    expect(meta).toHaveTextContent('cr 8K');
    expect(meta).toHaveTextContent('cw 2.2K');
    expect(meta).toHaveTextContent('$0.0000');
    expect(meta.textContent).toContain(', in 1.2K, out 340, cr 8K, cw 2.2K, $0.0000,');
    expect(meta.textContent).toMatch(/\d+m ago|just now/);
  });

  it('updates compact usage metadata when a streamed usage event arrives', async () => {
    routeParams = { id: 'conv-123' };
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
      expect(meta).toHaveTextContent('2.4K/272K (1%) context');
      expect(meta).toHaveTextContent('in 100');
      expect(meta).toHaveTextContent('out 140');
      expect(meta).toHaveTextContent('cr 50');
      expect(meta).toHaveTextContent('$0.0003');
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
    routeParams = { id: 'conv-123' };

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

  it('disables delete for the active conversation while it is streaming', async () => {
    routeParams = { id: 'conv-123' };

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

    fireEvent.change(screen.getByPlaceholderText('Ask kodelet anything...'), {
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

    render(<ChatPage />);

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
