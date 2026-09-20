import { act, fireEvent, render, screen, waitFor, within } from '@testing-library/react';
import { afterEach, assert, beforeEach, expect, vi } from 'vitest';
import type { BrowserTarget, Runner, WorkspaceTarget } from '../types';
import ChatPageComponent from './ChatPage';

// Import the page through this module so its dependencies are mocked before it loads.
export const ChatPage = ChatPageComponent;

// Shared integration-test fixtures. Each behavior suite registers its own lifecycle hooks.

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
        <textarea
          aria-label="Terminal input"
          className="workspace-terminal-host"
          data-testid="terminal-host"
          defaultValue="Terminal"
        />
      </div>
    ) : null,
}));

vi.mock('../components/workspace/BrowserPanel', () => ({
  default: ({ target }: { target: BrowserTarget }) => (
    <div
      data-testid="browser-panel"
      data-runner-id={target.runnerId}
      data-conversation-id={target.conversationId}
    />
  ),
}));

const apiMocks = vi.hoisted(() => ({
  getAuthPrincipal: vi.fn(),
  getConversations: vi.fn(),
  getConversation: vi.fn(),
  getChatSettings: vi.fn(),
  getCodexProviderStatus: vi.fn(),
  startCodexDeviceLogin: vi.fn(),
  getCodexDeviceLogin: vi.fn(),
  cancelCodexDeviceLogin: vi.fn(),
  getCopilotProviderStatus: vi.fn(),
  startCopilotDeviceLogin: vi.fn(),
  getCopilotDeviceLogin: vi.fn(),
  cancelCopilotDeviceLogin: vi.fn(),
  getAnthropicProviderStatus: vi.fn(),
  startAnthropicOAuthLogin: vi.fn(),
  completeAnthropicOAuthLogin: vi.fn(),
  cancelAnthropicOAuthLogin: vi.fn(),
  getRunners: vi.fn(),
  getSlashCommands: vi.fn(),
  streamChat: vi.fn(),
  streamConversation: vi.fn(),
  getCWDHints: vi.fn(),
  getGitDiff: vi.fn(),
  steerConversation: vi.fn(),
  stopConversation: vi.fn(),
  deleteConversation: vi.fn(),
  forkConversation: vi.fn(),
  respondToUIInput: vi.fn(),
}));

export const {
  getAuthPrincipal: mockGetAuthPrincipal,
  getConversations: mockGetConversations,
  getConversation: mockGetConversation,
  getChatSettings: mockGetChatSettings,
  getCodexProviderStatus: mockGetCodexProviderStatus,
  startCodexDeviceLogin: mockStartCodexDeviceLogin,
  getCodexDeviceLogin: mockGetCodexDeviceLogin,
  cancelCodexDeviceLogin: mockCancelCodexDeviceLogin,
  getCopilotProviderStatus: mockGetCopilotProviderStatus,
  startCopilotDeviceLogin: mockStartCopilotDeviceLogin,
  getCopilotDeviceLogin: mockGetCopilotDeviceLogin,
  cancelCopilotDeviceLogin: mockCancelCopilotDeviceLogin,
  getAnthropicProviderStatus: mockGetAnthropicProviderStatus,
  startAnthropicOAuthLogin: mockStartAnthropicOAuthLogin,
  completeAnthropicOAuthLogin: mockCompleteAnthropicOAuthLogin,
  cancelAnthropicOAuthLogin: mockCancelAnthropicOAuthLogin,
  getRunners: mockGetRunners,
  getSlashCommands: mockGetSlashCommands,
  streamChat: mockStreamChat,
  streamConversation: mockStreamConversation,
  getCWDHints: mockGetCWDHints,
  getGitDiff: mockGetGitDiff,
  steerConversation: mockSteerConversation,
  stopConversation: mockStopConversation,
  deleteConversation: mockDeleteConversation,
  forkConversation: mockForkConversation,
  respondToUIInput: mockRespondToUIInput,
} = apiMocks;

export const mockNavigate = vi.fn();

let routeParams: { id?: string } = {};

export const setRouteParams = (params: { id?: string }) => {
  routeParams = params;
};

export const makeRunner = (overrides: Partial<Runner> = {}): Runner => ({
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

export const selectNewChatOption = (
  label: 'Profile' | 'Model' | 'Reasoning effort' | 'Environment',
  value: string
) => {
  fireEvent.click(screen.getByRole('combobox', { name: label }));
  const option = within(screen.getByRole('listbox', { name: label }))
    .getAllByRole('option')
    .find((item) => item.dataset.value === value);
  assert.isDefined(option, `Expected ${label} option ${value}`);
  fireEvent.click(option);
};

export const selectWorkspaceRunner = () => selectNewChatOption('Environment', 'runner-1');

export const renderChatWithRunner = async () => {
  const result = render(<ChatPage />);
  await flushAsyncUpdates();
  fireEvent.click(screen.getByRole('button', { name: /^Change workspace:/ }));
  selectWorkspaceRunner();
  await flushAsyncUpdates();
  fireEvent.click(screen.getByRole('button', { name: 'Start' }));
  await flushAsyncUpdates();
  return result;
};

export const flushAsyncUpdates = async () => {
  await act(async () => {
    await Promise.resolve();
    await Promise.resolve();
  });
};

export const runCwdSuggestionDebounce = async () => {
  await act(async () => {
    vi.advanceTimersByTime(150);
    await Promise.resolve();
    await Promise.resolve();
  });
};

export const flushCwdBlurTimer = async () => {
  await act(async () => {
    vi.advanceTimersByTime(120);
    await Promise.resolve();
  });
};

export const stubImageFileReader = (dataUrl: string) => {
  vi.stubGlobal(
    'FileReader',
    class {
      result: string | ArrayBuffer | null = null;
      error: DOMException | null = null;
      onload: null | (() => void) = null;
      onerror: null | (() => void) = null;

      readAsDataURL() {
        this.result = dataUrl;
        this.onload?.();
      }
    }
  );
};

vi.mock('react-router', async () => {
  const actual = await vi.importActual<typeof import('react-router')>('react-router');

  return {
    ...actual,
    useNavigate: () => mockNavigate,
    useParams: () => routeParams,
  };
});

vi.mock('../services/api', () => ({ default: apiMocks }));

export const waitForTerminalAccess = async () => {
  await waitFor(() => expect(mockGetAuthPrincipal).toHaveBeenCalled());
  await act(async () => {
    await Promise.resolve();
  });
};

export const startOptimisticRemoteConversation = async ({
  runner = makeRunner(),
  message = 'first attempt',
}: {
  runner?: Runner;
  message?: string;
} = {}) => {
  mockGetRunners.mockResolvedValue({ runners: [runner] });
  const { rerender } = render(<ChatPage />);
  await waitFor(() => expect(mockGetRunners).toHaveBeenCalled());
  fireEvent.click(screen.getByTestId('sidebar-new-chat-button'));
  selectNewChatOption('Environment', runner.id);
  fireEvent.change(screen.getByLabelText('Working directory'), {
    target: { value: '../other-project' },
  });
  await flushAsyncUpdates();
  fireEvent.click(screen.getByRole('button', { name: 'Start' }));
  fireEvent.change(screen.getByPlaceholderText('Ask kodelet anything...'), {
    target: { value: message },
  });
  fireEvent.click(screen.getByRole('button', { name: 'Send' }));

  await waitFor(() => expect(mockStreamChat).toHaveBeenCalledTimes(1));
  const conversationId = mockStreamChat.mock.calls[0]?.[0]?.conversationId as string | undefined;
  if (!conversationId) {
    throw new Error('expected a preallocated conversation id');
  }
  setRouteParams({ id: conversationId });
  rerender(<ChatPage />);
  await flushAsyncUpdates();
  return conversationId;
};

export const setupChatPageTests = () => {
  afterEach(() => {
    // Notifications are mounted outside the React root and own their dismissal timers.
    for (const button of screen.queryAllByRole('button', { name: 'Dismiss notification' })) {
      button.click();
    }
    vi.unstubAllGlobals();
  });

  beforeEach(() => {
    vi.clearAllMocks();
    // Clear implementations and queued responses, not just call history, between scenarios.
    for (const mock of Object.values(apiMocks)) mock.mockReset();
    mockNavigate.mockReset();
    setRouteParams({});
    window.localStorage.clear();
    window.HTMLElement.prototype.scrollIntoView = vi.fn();
    vi.stubGlobal(
      'ResizeObserver',
      class {
        observe = vi.fn();
        disconnect = vi.fn();
      }
    );
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
    mockGetRunners.mockResolvedValue({
      runners: [
        makeRunner({
          workspaceDiscovery: true,
          workspaceTerminal: true,
          workspaceGitDiff: true,
        }),
      ],
    });
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
          { name: 'default', scope: 'global' },
          { name: 'work', scope: 'global', active: true },
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
};
