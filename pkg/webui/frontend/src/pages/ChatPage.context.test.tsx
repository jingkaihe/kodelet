import { act, fireEvent, render, screen, waitFor, within } from '@testing-library/react';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import type { ChatSettings, Runner } from '../types';
import {
  ChatPage,
  flushAsyncUpdates,
  flushCwdBlurTimer,
  makeRunner,
  mockGetChatSettings,
  mockGetConversations,
  mockGetCWDHints,
  mockGetRunners,
  mockGetSlashCommands,
  mockStreamChat,
  runCwdSuggestionDebounce,
  selectNewChatOption,
  selectWorkspaceRunner,
  setupChatPageTests,
  waitForTerminalAccess,
} from './ChatPage.testSupport';

describe('ChatPage working directories and runner selection', () => {
  setupChatPageTests();

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
      selectWorkspaceRunner();
      const cwdInput = screen.getByLabelText('Working directory');
      fireEvent.focus(cwdInput);
      expect(screen.queryByTestId('cwd-suggestions')).not.toBeInTheDocument();
      fireEvent.change(cwdInput, { target: { value: '/workspace/ko' } });
      await runCwdSuggestionDebounce();

      expect(mockGetCWDHints).toHaveBeenLastCalledWith('/workspace/ko', {
        runnerId: 'runner-1',
        environmentProfile: '',
        profile: 'work',
      });
      expect(screen.getByTestId('cwd-suggestions')).toBeInTheDocument();

      fireEvent.mouseDown(screen.getByTestId('cwd-suggestion-0'));
      fireEvent.click(screen.getByTestId('cwd-suggestion-0'));
      expect(screen.getByTestId('new-chat-dialog')).toBeInTheDocument();
      expect(screen.queryByTestId('cwd-suggestions')).not.toBeInTheDocument();
      expect(mockGetCWDHints).not.toHaveBeenLastCalledWith('/workspace/kodelet', expect.anything());
      fireEvent.click(screen.getByRole('button', { name: 'Start' }));
      expect(screen.queryByTestId('new-chat-dialog')).not.toBeInTheDocument();
      expect(screen.getByTestId('chat-workspace-header')).toHaveTextContent('/workspace/kodelet');
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
      selectWorkspaceRunner();
      const cwdInput = screen.getByLabelText('Working directory');
      fireEvent.focus(cwdInput);
      expect(screen.queryByTestId('cwd-suggestions')).not.toBeInTheDocument();
      fireEvent.change(cwdInput, { target: { value: '/workspace/ko' } });
      await runCwdSuggestionDebounce();

      expect(screen.getByTestId('cwd-suggestions')).toBeInTheDocument();

      fireEvent.keyDown(cwdInput, { key: 'ArrowDown' });
      fireEvent.keyDown(cwdInput, { key: 'Enter' });
      fireEvent.click(screen.getByRole('button', { name: 'Start' }));

      expect(screen.getByTestId('chat-workspace-header')).toHaveTextContent('/workspace/kodelet');
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
      selectWorkspaceRunner();
      const cwdInput = screen.getByLabelText('Working directory');
      fireEvent.focus(cwdInput);
      fireEvent.change(cwdInput, { target: { value: '/workspace/ko' } });
      await runCwdSuggestionDebounce();

      expect(screen.getByTestId('cwd-suggestions')).toBeInTheDocument();

      fireEvent.keyDown(cwdInput, { key: 'Tab' });
      expect(screen.queryByTestId('cwd-suggestions')).not.toBeInTheDocument();
      expect(cwdInput).toHaveValue('/workspace/kodelet');
      fireEvent.click(screen.getByRole('button', { name: 'Start' }));

      expect(screen.getByTestId('chat-workspace-header')).toHaveTextContent('/workspace/kodelet');
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
      expect(mockGetCWDHints).not.toHaveBeenCalled();

      fireEvent.click(screen.getByTestId('sidebar-new-chat-button'));
      selectWorkspaceRunner();
      const cwdInput = screen.getByLabelText('Working directory');
      fireEvent.focus(cwdInput);
      fireEvent.change(cwdInput, { target: { value: '/workspace/default' } });
      await runCwdSuggestionDebounce();
      fireEvent.change(cwdInput, { target: { value: '/workspace/ko' } });

      await act(async () => {
        vi.runOnlyPendingTimers();
      });

      expect(mockGetCWDHints).toHaveBeenLastCalledWith('/workspace/ko', {
        runnerId: 'runner-1',
        environmentProfile: '',
        profile: 'work',
      });

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
    selectWorkspaceRunner();
    fireEvent.change(screen.getByLabelText('Working directory'), {
      target: { value: 'kodelet-website' },
    });
    await flushAsyncUpdates();
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

  it('selects a remote runner and sends an explicit runner-host cwd', async () => {
    mockGetRunners.mockResolvedValue({
      runners: [makeRunner()],
    });
    mockStreamChat.mockResolvedValue(undefined);

    render(<ChatPage />);
    await waitFor(() => expect(mockGetRunners).toHaveBeenCalled());
    await waitForTerminalAccess();
    fireEvent.click(screen.getByTestId('sidebar-new-chat-button'));
    selectWorkspaceRunner();
    fireEvent.change(screen.getByLabelText('Runner profile'), {
      target: { value: 'gpu' },
    });
    const cwdInput = screen.getByLabelText('Working directory');
    expect(cwdInput).toHaveAttribute('placeholder', '/runner/kodelet');
    fireEvent.change(cwdInput, { target: { value: '/runner/other-project' } });
    await flushAsyncUpdates();
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
        cwd: '/runner/other-project',
        clientCapabilities: {
          interactiveUI: true,
          persistentWidgets: true,
          persistentSurfaces: false,
        },
      }),
      expect.any(Object)
    );
  });

  it('refreshes slash discovery when only the model profile changes', async () => {
    const runnerId = 'runner-1';
    vi.useFakeTimers();
    mockGetRunners.mockResolvedValue({ runners: [makeRunner({ workspaceDiscovery: true })] });
    mockGetSlashCommands.mockImplementation((_cwd: string, target?: { profile?: string }) =>
      Promise.resolve({
        commands: [{ name: `${target?.profile}-command`, description: 'Profile command' }],
      })
    );
    try {
      render(<ChatPage />);
      await flushAsyncUpdates();
      fireEvent.click(screen.getByTestId('sidebar-new-chat-button'));
      selectNewChatOption('Environment', runnerId);
      await flushAsyncUpdates();
      fireEvent.click(screen.getByRole('button', { name: 'Start' }));
      await flushAsyncUpdates();
      fireEvent.change(screen.getByTestId('composer-textarea'), { target: { value: '/' } });
      expect(screen.getByText('/work-command')).toBeInTheDocument();

      const cwd = '/runner/kodelet';
      for (const profile of ['anthropic', 'default']) {
        fireEvent.click(screen.getByTestId('composer-context-button'));
        mockGetSlashCommands.mockClear();
        selectNewChatOption('Profile', profile);
        await flushAsyncUpdates();
        expect(mockGetSlashCommands).not.toHaveBeenCalled();
        fireEvent.click(screen.getByRole('button', { name: 'Start' }));
        await flushAsyncUpdates();

        expect(mockGetSlashCommands).toHaveBeenCalledTimes(1);
        expect(mockGetSlashCommands).toHaveBeenLastCalledWith(cwd, {
          runnerId,
          conversationId: undefined,
          environmentProfile: '',
          profile,
        });
        expect(screen.getByText(`/${profile}-command`)).toBeInTheDocument();
        expect(screen.queryByText('/work-command')).not.toBeInTheDocument();
      }
    } finally {
      vi.useRealTimers();
    }
  });

  it.each([
    'anthropic',
    'default',
  ])('discovers runner commands and directory hints with model profile %s', async (profile) => {
    vi.useFakeTimers();
    mockGetRunners.mockResolvedValue({ runners: [makeRunner({ workspaceDiscovery: true })] });
    mockGetCWDHints.mockResolvedValue({ hints: [{ path: '/runner/selected-project' }] });
    try {
      render(<ChatPage />);
      await flushAsyncUpdates();
      fireEvent.click(screen.getByTestId('sidebar-new-chat-button'));
      selectWorkspaceRunner();
      fireEvent.change(screen.getByLabelText('Runner profile'), { target: { value: 'review' } });
      selectNewChatOption('Profile', profile);
      const cwdInput = screen.getByLabelText('Working directory');
      fireEvent.focus(cwdInput);
      fireEvent.change(cwdInput, { target: { value: '~/proj' } });
      await runCwdSuggestionDebounce();
      expect(mockGetCWDHints).toHaveBeenLastCalledWith('~/proj', {
        runnerId: 'runner-1',
        environmentProfile: 'review',
        profile,
      });
      expect(screen.getByTestId('cwd-suggestions')).toHaveTextContent('/runner/selected-project');
      fireEvent.click(screen.getByText('/runner/selected-project'));
      fireEvent.click(screen.getByRole('button', { name: 'Start' }));
      await flushAsyncUpdates();
      expect(mockGetSlashCommands).toHaveBeenLastCalledWith('/runner/selected-project', {
        runnerId: 'runner-1',
        conversationId: undefined,
        environmentProfile: 'review',
        profile,
      });
    } finally {
      vi.useRealTimers();
    }
  });

  it.each([
    {
      runnerId: 'runner-1',
      label: 'Runner profile',
      value: 'new',
      profile: 'work',
      environmentProfile: 'new',
    },
    {
      runnerId: 'runner-1',
      label: 'Profile',
      value: 'default',
      profile: 'default',
      environmentProfile: '',
    },
  ])('discards pending directory hints after $label changes ($runnerId)', async ({
    runnerId,
    label,
    value,
    profile,
    environmentProfile,
  }) => {
    vi.useFakeTimers();
    mockGetRunners.mockResolvedValue({ runners: [makeRunner({ workspaceDiscovery: true })] });
    let resolveOldHints: ((result: { hints: { path: string }[] }) => void) | undefined;
    mockGetCWDHints
      .mockImplementationOnce(
        () =>
          new Promise((resolve) => {
            resolveOldHints = resolve;
          })
      )
      .mockResolvedValue({ hints: [{ path: '/runner/new-profile' }] });
    try {
      render(<ChatPage />);
      await flushAsyncUpdates();
      fireEvent.click(screen.getByTestId('sidebar-new-chat-button'));
      selectNewChatOption('Environment', runnerId);
      const cwdInput = screen.getByLabelText('Working directory');
      fireEvent.focus(cwdInput);
      fireEvent.change(cwdInput, { target: { value: 'project' } });
      await runCwdSuggestionDebounce();
      expect(resolveOldHints).toBeDefined();
      if (label === 'Profile') {
        selectNewChatOption('Profile', value);
      } else {
        fireEvent.change(screen.getByLabelText(label), { target: { value } });
      }
      await act(async () => {
        resolveOldHints?.({ hints: [{ path: '/runner/old-profile' }] });
      });
      expect(screen.queryByText('/runner/old-profile')).not.toBeInTheDocument();
      fireEvent.focus(cwdInput);
      await runCwdSuggestionDebounce();
      expect(mockGetCWDHints).toHaveBeenLastCalledWith('project', {
        runnerId,
        environmentProfile,
        profile,
      });
      expect(screen.getByTestId('cwd-suggestions')).toHaveTextContent('/runner/new-profile');
    } finally {
      vi.useRealTimers();
    }
  });

  describe('embedded runner defaults', () => {
    const embeddedRunner = makeRunner({ id: 'runner-embedded', displayName: 'embedded-runner' });
    const settings: ChatSettings = {
      currentProfile: 'work',
      profiles: [{ name: 'work', scope: 'global' }],
      reasoningEffort: 'medium',
      reasoningEffortOptions: ['medium'],
      defaultRunnerId: embeddedRunner.id,
      defaultRunnerReady: true,
    };

    beforeEach(() => {
      mockGetChatSettings.mockResolvedValue(settings);
      mockGetRunners.mockResolvedValue({ runners: [makeRunner(), embeddedRunner] });
      mockStreamChat.mockResolvedValue(undefined);
    });

    it('allows prompting without setup once the embedded runner loads', async () => {
      let resolveRunners!: (value: { runners: Runner[] }) => void;
      mockGetRunners.mockReturnValueOnce(
        new Promise((resolve) => {
          resolveRunners = resolve;
        })
      );
      render(<ChatPage />);
      await flushAsyncUpdates();
      expect(screen.getByTestId('composer-textarea')).toBeDisabled();

      await act(async () => resolveRunners({ runners: [makeRunner(), embeddedRunner] }));
      expect(screen.getByTestId('composer-textarea')).toBeEnabled();
      expect(screen.queryByTestId('new-chat-dialog')).not.toBeInTheDocument();
      fireEvent.change(screen.getByPlaceholderText('Ask kodelet anything...'), {
        target: { value: 'hello immediately' },
      });
      fireEvent.click(screen.getByRole('button', { name: 'Send' }));

      await waitFor(() =>
        expect(mockStreamChat).toHaveBeenCalledWith(
          expect.objectContaining({ message: 'hello immediately', runnerId: embeddedRunner.id }),
          expect.any(Object)
        )
      );
    });

    it('does not preselect an embedded runner that is not ready', async () => {
      mockGetChatSettings.mockResolvedValue({ ...settings, defaultRunnerReady: false });
      render(<ChatPage />);
      await flushAsyncUpdates();

      expect(screen.getByTestId('composer-textarea')).toBeDisabled();
      fireEvent.click(screen.getByTestId('sidebar-new-chat-button'));
      expect(screen.getByLabelText('Environment')).toHaveTextContent('Select a workspace runner');
      expect(screen.getByRole('button', { name: 'Start' })).toBeDisabled();
    });

    it('preserves a manual runner choice until starting a new chat', async () => {
      vi.useFakeTimers();
      try {
        mockGetRunners.mockResolvedValueOnce({ runners: [makeRunner()] });
        render(<ChatPage />);
        await flushAsyncUpdates();

        fireEvent.click(screen.getByTestId('sidebar-new-chat-button'));
        selectWorkspaceRunner();
        await flushAsyncUpdates();
        await act(async () => vi.advanceTimersByTimeAsync(5000));
        expect(screen.getByLabelText('Environment')).toHaveTextContent(
          'kodelet-gpu — worker — idle'
        );

        fireEvent.click(screen.getByRole('button', { name: 'Start' }));
        await act(async () => vi.advanceTimersByTimeAsync(5000));
        fireEvent.click(screen.getByRole('button', { name: /\/runner\/kodelet/ }));
        expect(screen.getByLabelText('Environment')).toHaveTextContent(
          'kodelet-gpu — worker — idle'
        );
        fireEvent.click(screen.getByRole('button', { name: 'Cancel' }));

        fireEvent.click(screen.getByTestId('sidebar-new-chat-button'));
        await flushAsyncUpdates();
        expect(screen.getByLabelText('Environment')).toHaveTextContent(
          'embedded-runner — worker — idle'
        );
        expect(screen.getByRole('button', { name: 'Start' })).toBeEnabled();
        fireEvent.click(screen.getByRole('button', { name: 'Cancel' }));
        expect(screen.getByTestId('composer-textarea')).toBeEnabled();
      } finally {
        vi.useRealTimers();
      }
    });
  });

  it('requires a workspace runner without a workspace-enabled setting', async () => {
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
    expect(mockGetSlashCommands).not.toHaveBeenCalled();
    expect(mockGetCWDHints).not.toHaveBeenCalled();

    fireEvent.click(screen.getByTestId('sidebar-new-chat-button'));
    fireEvent.click(screen.getByRole('combobox', { name: 'Environment' }));
    expect(
      within(screen.getByRole('listbox', { name: 'Environment' })).getByRole('option', {
        name: 'Select a workspace runner',
      })
    ).toBeDisabled();
    fireEvent.click(screen.getByRole('combobox', { name: 'Environment' }));
    expect(screen.queryByLabelText('Working directory')).not.toBeInTheDocument();
    expect(screen.getByRole('button', { name: 'Start' })).toBeDisabled();

    selectNewChatOption('Environment', 'runner-required');
    expect(screen.getByLabelText('Working directory')).toHaveValue('');
    expect(screen.getByLabelText('Working directory')).toHaveAttribute(
      'placeholder',
      '/runner/required'
    );
    await flushAsyncUpdates();
    expect(screen.getByRole('button', { name: 'Start' })).toBeEnabled();
    fireEvent.click(screen.getByRole('button', { name: 'Start' }));

    expect(screen.getByTestId('composer-textarea')).toBeEnabled();
    expect(
      screen.queryByText(
        'The control-plane workspace is disabled. Select a workspace runner to start a chat.'
      )
    ).not.toBeInTheDocument();
    expect(screen.queryByTestId('workspace-tools-shell')).not.toBeInTheDocument();
    expect(mockGetSlashCommands).not.toHaveBeenCalled();
    fireEvent.click(screen.getByTestId('sidebar-new-chat-button'));
    expect(screen.getByTestId('composer-textarea')).toBeDisabled();
    expect(screen.getByLabelText('Environment')).toHaveTextContent('Select a workspace runner');
    expect(screen.queryByLabelText('Working directory')).not.toBeInTheDocument();
  });

  it('does not offer recent local workspaces in the new chat dialog', async () => {
    mockGetConversations.mockResolvedValue({
      conversations: [
        {
          id: 'conv-1',
          createdAt: '2023-01-01T00:00:00Z',
          updatedAt: '2023-01-06T00:00:00Z',
          messageCount: 1,
          cwd: '/workspace/a',
        },
      ],
      hasMore: false,
      total: 1,
      limit: 10,
      offset: 0,
    });

    render(<ChatPage />);

    await waitFor(() => {
      expect(mockGetConversations).toHaveBeenCalled();
      expect(mockGetChatSettings).toHaveBeenCalled();
    });
    fireEvent.click(screen.getByTestId('sidebar-new-chat-button'));

    expect(screen.queryByTestId('recent-workspaces')).not.toBeInTheDocument();
    expect(screen.queryByLabelText('Working directory')).not.toBeInTheDocument();
    selectWorkspaceRunner();
    await flushAsyncUpdates();
    expect(screen.getByLabelText('Working directory')).toHaveValue('');
    expect(screen.queryByTestId('recent-workspaces')).not.toBeInTheDocument();
    expect(mockGetCWDHints).not.toHaveBeenCalled();
  });
});
