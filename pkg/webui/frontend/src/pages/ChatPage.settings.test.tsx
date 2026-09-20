import { act, fireEvent, render, screen, waitFor, within } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { describe, expect, it, vi } from 'vitest';
import type { ChatSettings } from '../types';
import {
  ChatPage,
  flushAsyncUpdates,
  makeRunner,
  mockGetChatSettings,
  mockGetConversation,
  mockGetCWDHints,
  mockGetRunners,
  mockGetSlashCommands,
  mockStreamChat,
  renderChatWithRunner,
  runCwdSuggestionDebounce,
  selectNewChatOption,
  selectWorkspaceRunner,
  setRouteParams,
  setupChatPageTests,
} from './ChatPage.testSupport';

describe('ChatPage model and reasoning settings', () => {
  setupChatPageTests();

  it('prevents starting a chat while initial settings are loading', async () => {
    mockGetChatSettings.mockReturnValue(new Promise(() => {}));
    mockStreamChat.mockResolvedValue(undefined);

    render(<ChatPage />);

    await flushAsyncUpdates();
    expect(screen.getByTestId('composer-textarea')).toBeDisabled();
    expect(screen.getByRole('button', { name: 'Send' })).toBeDisabled();
    fireEvent.click(screen.getByTestId('sidebar-new-chat-button'));
    selectWorkspaceRunner();
    expect(screen.getByRole('button', { name: 'Start' })).toBeDisabled();
    expect(mockStreamChat).not.toHaveBeenCalled();
    expect(mockGetSlashCommands).not.toHaveBeenCalled();
  });

  it.each([
    { name: 'request failure', error: 'settings unavailable' },
    { name: 'missing profile', profile: undefined },
    { name: 'empty profile', profile: '' },
    { name: 'blank profile', profile: '  ' },
  ])('prevents starting a chat with invalid initial settings: $name', async (test) => {
    const consoleError = vi.spyOn(console, 'error').mockImplementation(() => undefined);
    if (test.error) {
      mockGetChatSettings.mockRejectedValue(new Error(test.error));
    } else {
      mockGetChatSettings.mockResolvedValue({
        currentProfile: test.profile,
        profiles: [{ name: 'flair', scope: 'global', active: true }],
        reasoningEffort: 'medium',
        reasoningEffortOptions: ['medium'],
      });
    }
    mockStreamChat.mockResolvedValue(undefined);

    try {
      render(<ChatPage />);

      await waitFor(() =>
        expect(consoleError).toHaveBeenCalledWith(
          'Failed to load chat settings',
          expect.objectContaining({
            message: test.error || 'Chat settings did not return a model profile',
          })
        )
      );
      expect(screen.getByTestId('composer-textarea')).toBeDisabled();
      expect(screen.getByRole('button', { name: 'Send' })).toBeDisabled();
      fireEvent.click(screen.getByTestId('sidebar-new-chat-button'));
      const profile = screen.getByRole('combobox', { name: 'Profile' });
      expect(profile).toHaveTextContent('Select a profile');
      expect(profile).toBeDisabled();
      selectWorkspaceRunner();
      expect(screen.getByRole('button', { name: 'Start' })).toBeDisabled();
      expect(mockStreamChat).not.toHaveBeenCalled();
      expect(mockGetSlashCommands).not.toHaveBeenCalled();
    } finally {
      consoleError.mockRestore();
    }
  });

  it.each([
    { selected: 'deep', hidden: false },
    { selected: 'hidden-search', hidden: true },
  ])('shows the resolved $selected profile independently of the configured default', async ({
    selected,
    hidden,
  }) => {
    mockGetChatSettings.mockImplementation((profile?: string) =>
      Promise.resolve({
        currentProfile: profile || (hidden ? selected : 'flair'),
        profiles: [
          { name: 'flair', scope: 'global', active: true },
          ...(!hidden ? [{ name: selected, scope: 'global' }] : []),
        ],
        reasoningEffort: 'medium',
        reasoningEffortOptions: ['medium'],
      })
    );

    render(<ChatPage />);
    await flushAsyncUpdates();
    fireEvent.click(screen.getByTestId('sidebar-new-chat-button'));
    if (!hidden) selectNewChatOption('Profile', selected);
    await waitFor(() => expect(screen.getByLabelText('Profile')).toHaveTextContent(selected));
    await flushAsyncUpdates();
    fireEvent.click(screen.getByRole('combobox', { name: 'Profile' }));
    const options = screen.getByRole('listbox', { name: 'Profile' });
    expect(within(options).getAllByRole('option')).toHaveLength(2);
    expect(within(options).getByRole('option', { name: 'flair' })).toHaveAttribute(
      'aria-selected',
      'false'
    );
    expect(within(options).getByRole('option', { name: selected })).toHaveAttribute(
      'aria-selected',
      'true'
    );
    expect(within(options).queryByRole('option', { name: 'default' })).not.toBeInTheDocument();
  });

  it('allows selecting a profile for a new conversation', async () => {
    mockStreamChat.mockResolvedValue(undefined);

    render(<ChatPage />);

    await waitFor(() => expect(mockGetChatSettings).toHaveBeenCalled());
    expect(screen.getByRole('button', { name: 'New chat' })).toBe(
      screen.getByTestId('sidebar-new-chat-button')
    );
    fireEvent.click(screen.getByTestId('sidebar-new-chat-button'));
    selectWorkspaceRunner();
    expect(screen.getByTestId('new-chat-dialog')).toBeInTheDocument();
    await waitFor(() => expect(screen.getByLabelText('Reasoning effort')).toBeEnabled());
    expect(screen.getByLabelText('Reasoning effort')).toHaveTextContent('medium');
    selectNewChatOption('Reasoning effort', 'high');

    selectNewChatOption('Profile', 'anthropic');
    await waitFor(() =>
      expect(mockGetChatSettings).toHaveBeenLastCalledWith('anthropic', 'runner-1')
    );
    await waitFor(() =>
      expect(screen.getByLabelText('Reasoning effort')).toHaveTextContent('high')
    );
    fireEvent.change(screen.getByLabelText('Working directory'), {
      target: { value: '/workspace/alt' },
    });

    await waitFor(() =>
      expect(mockGetCWDHints).toHaveBeenCalledWith('/workspace/alt', {
        runnerId: 'runner-1',
        environmentProfile: '',
        profile: 'anthropic',
      })
    );
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
    selectNewChatOption('Reasoning effort', 'high');
    selectNewChatOption('Profile', 'restricted');

    await waitFor(() => expect(screen.getByLabelText('Reasoning effort')).toHaveTextContent('low'));
    expect(screen.getByLabelText('Reasoning effort')).toBeDisabled();
  });

  it('sends the chosen model only for the first turn of a new conversation', async () => {
    const defaults: ChatSettings = await mockGetChatSettings();
    mockGetChatSettings.mockResolvedValue({
      ...defaults,
      model: 'gpt-5',
      modelOptions: ['gpt-5', 'gpt-5-mini'],
    });
    mockStreamChat.mockImplementation(async (request, { onEvent }) => {
      onEvent({
        kind: 'conversation',
        conversation_id: request.conversationId,
        cwd: '/runner/kodelet',
      });
    });
    mockGetConversation.mockImplementation(async (id: string) => ({
      id,
      runnerId: 'runner-1',
      cwd: '/runner/kodelet',
      profile: 'work',
      model: 'gpt-5-mini',
      reasoningEffort: 'medium',
      messages: [{ role: 'user', content: 'hello' }],
      toolResults: {},
    }));

    const { rerender } = await renderChatWithRunner();
    expect(screen.getByTestId('composer-quick-pick')).toHaveTextContent(/^work\/gpt-5 · medium$/);
    fireEvent.click(screen.getByTestId('composer-context-button'));
    await flushAsyncUpdates();
    selectNewChatOption('Model', 'gpt-5-mini');
    fireEvent.click(screen.getByRole('button', { name: 'Start' }));
    expect(screen.getByTestId('composer-quick-pick')).toHaveTextContent(
      /^work\/gpt-5-mini · medium$/
    );
    fireEvent.change(screen.getByPlaceholderText('Ask kodelet anything...'), {
      target: { value: 'hello' },
    });
    fireEvent.click(screen.getByRole('button', { name: 'Send' }));
    await waitFor(() => expect(mockStreamChat).toHaveBeenCalledTimes(1));
    expect(mockStreamChat.mock.calls[0][0]).toMatchObject({
      profile: 'work',
      options: { model: 'gpt-5-mini' },
    });

    setRouteParams({ id: mockStreamChat.mock.calls[0][0].conversationId });
    rerender(<ChatPage />);
    await flushAsyncUpdates();
    expect(screen.queryByLabelText('Model')).not.toBeInTheDocument();
    expect(screen.getByTestId('composer-inline-context')).toHaveTextContent(
      /^work\/gpt-5-mini · medium$/
    );
    expect(screen.queryByTestId('composer-context-button')).not.toBeInTheDocument();
    fireEvent.change(screen.getByPlaceholderText('Ask kodelet anything...'), {
      target: { value: 'continue' },
    });
    fireEvent.click(screen.getByRole('button', { name: 'Send' }));
    await waitFor(() => expect(mockStreamChat).toHaveBeenCalledTimes(2));
    expect(mockStreamChat.mock.calls[1][0].options).toBeUndefined();
  });

  it('quick picks a profile/model and reasoning effort from the composer', async () => {
    const defaults: ChatSettings = await mockGetChatSettings();
    mockGetChatSettings.mockImplementation((profile?: string, runnerId?: string) => {
      const selectedProfile = profile || 'work';
      const models =
        selectedProfile === 'anthropic'
          ? { model: 'claude-opus-4-6', modelOptions: ['claude-sonnet-4-6', 'claude-opus-4-6'] }
          : selectedProfile === 'work'
            ? { model: 'gpt-5', modelOptions: ['gpt-5', 'gpt-5-mini'] }
            : { model: `${selectedProfile}-model`, modelOptions: [] };
      const reasoning =
        selectedProfile === 'anthropic'
          ? { reasoningEffort: 'max', reasoningEffortOptions: ['medium', 'high', 'max'] }
          : { reasoningEffort: 'medium', reasoningEffortOptions: ['low', 'medium', 'high'] };
      return Promise.resolve({
        ...defaults,
        currentProfile: selectedProfile,
        profiles: [
          { name: 'work', scope: 'global', active: true },
          { name: 'anthropic', scope: 'global' },
          { name: 'secret', scope: 'global', hidden: true },
        ],
        ...models,
        ...reasoning,
        runnerId,
      });
    });

    await renderChatWithRunner();
    const quickPick = screen.getByTestId('composer-quick-pick');
    expect(quickPick).toHaveTextContent(/^work\/gpt-5 · medium$/);
    expect(quickPick.querySelector('svg.lucide-settings')).not.toBeNull();
    mockGetChatSettings.mockClear();

    // Opening the model picker loads every visible profile for the selected runner.
    fireEvent.click(screen.getByRole('combobox', { name: 'Model quick pick' }));
    await flushAsyncUpdates();
    expect(mockGetChatSettings.mock.calls).toEqual([
      ['work', 'runner-1'],
      ['anthropic', 'runner-1'],
    ]);
    const modelList = screen.getByRole('listbox', { name: 'Model quick pick' });
    expect(
      within(modelList)
        .getAllByRole('option')
        .map((option) => option.textContent)
    ).toEqual([
      'work/gpt-5',
      'work/gpt-5-mini',
      'anthropic/claude-sonnet-4-6',
      'anthropic/claude-opus-4-6',
    ]);
    fireEvent.click(within(modelList).getByRole('option', { name: 'anthropic/claude-opus-4-6' }));

    // Switching profile adopts that profile's reasoning defaults.
    expect(quickPick).toHaveTextContent(/^anthropic\/claude-opus-4-6 · max$/);
    expect(screen.queryByRole('listbox')).not.toBeInTheDocument();

    fireEvent.click(screen.getByRole('combobox', { name: 'Reasoning effort quick pick' }));
    const effortList = screen.getByRole('listbox', { name: 'Reasoning effort quick pick' });
    expect(
      within(effortList)
        .getAllByRole('option')
        .map((option) => option.textContent)
    ).toEqual(['medium', 'high', 'max']);
    fireEvent.click(within(effortList).getByRole('option', { name: 'high' }));
    expect(quickPick).toHaveTextContent(/^anthropic\/claude-opus-4-6 · high$/);

    // The gear still opens the full dialog, seeded from the quick pick.
    fireEvent.click(screen.getByRole('button', { name: 'Model settings' }));
    await flushAsyncUpdates();
    expect(screen.getByLabelText('Profile')).toHaveTextContent('anthropic');
    expect(screen.getByLabelText('Model')).toHaveTextContent('claude-opus-4-6');
    expect(screen.getByLabelText('Reasoning effort')).toHaveTextContent('high');
    fireEvent.click(screen.getByRole('button', { name: 'Close new chat dialog' }));

    fireEvent.change(screen.getByPlaceholderText('Ask kodelet anything...'), {
      target: { value: 'hello' },
    });
    fireEvent.click(screen.getByRole('button', { name: 'Send' }));
    await waitFor(() => expect(mockStreamChat).toHaveBeenCalledTimes(1));
    expect(mockStreamChat.mock.calls[0][0]).toMatchObject({
      profile: 'anthropic',
      options: { model: 'claude-opus-4-6' },
      reasoningEffort: 'high',
    });
  });

  it('replaces model options and resets to the profile default, including custom configured models', async () => {
    const defaultSettings = mockGetChatSettings.getMockImplementation();
    mockGetChatSettings.mockImplementation(async (profile?: string) => ({
      ...(await defaultSettings?.(profile)),
      ...(profile === 'anthropic'
        ? {
            model: 'custom-claude',
            modelOptions: ['claude-sonnet-4-6'],
          }
        : { model: 'gpt-5', modelOptions: ['gpt-5', 'gpt-5-mini'] }),
    }));
    mockStreamChat.mockResolvedValue(undefined);

    await renderChatWithRunner();
    fireEvent.click(screen.getByTestId('composer-context-button'));
    await flushAsyncUpdates();
    selectNewChatOption('Model', 'gpt-5-mini');
    selectNewChatOption('Profile', 'anthropic');
    expect(screen.getByLabelText('Model')).toBeDisabled();
    expect(screen.getByRole('button', { name: 'Start' })).toBeDisabled();
    await flushAsyncUpdates();
    expect(screen.getByLabelText('Model')).toHaveTextContent('custom-claude');
    fireEvent.click(screen.getByLabelText('Model'));
    const models = within(screen.getByRole('listbox', { name: 'Model' }));
    expect(models.getAllByRole('option')).toHaveLength(2);
    expect(models.getByRole('option', { name: 'custom-claude' })).toHaveAttribute(
      'aria-selected',
      'true'
    );
    expect(models.getByRole('option', { name: 'claude-sonnet-4-6' })).toBeInTheDocument();
    expect(models.queryByRole('option', { name: 'gpt-5-mini' })).not.toBeInTheDocument();
    fireEvent.click(screen.getByRole('combobox', { name: 'Model' }));
    selectNewChatOption('Profile', 'work');
    await flushAsyncUpdates();
    expect(screen.getByLabelText('Model')).toHaveTextContent('gpt-5');
    selectNewChatOption('Profile', 'anthropic');
    await flushAsyncUpdates();
    fireEvent.click(screen.getByRole('button', { name: 'Start' }));
    expect(screen.getByTestId('composer-quick-pick')).toHaveTextContent(
      /^anthropic\/custom-claude · max$/
    );
    fireEvent.change(screen.getByPlaceholderText('Ask kodelet anything...'), {
      target: { value: 'hello' },
    });
    fireEvent.click(screen.getByRole('button', { name: 'Send' }));
    await waitFor(() => expect(mockStreamChat).toHaveBeenCalledTimes(1));
    expect(mockStreamChat.mock.calls[0][0]).toMatchObject({
      profile: 'anthropic',
      options: { model: 'custom-claude' },
    });
  });

  it('resets the model on every runner change even when both runners offer the same models', async () => {
    const defaults: ChatSettings = await mockGetChatSettings();
    mockGetRunners.mockResolvedValue({ runners: [makeRunner(), makeRunner({ id: 'runner-2' })] });
    mockGetChatSettings.mockImplementation(async (_profile?: string, runnerId?: string) => ({
      ...defaults,
      model: runnerId === 'runner-2' ? 'gpt-5-mini' : 'gpt-5',
      modelOptions: ['gpt-5', 'gpt-5-mini'],
    }));

    await renderChatWithRunner();
    fireEvent.click(screen.getByTestId('composer-context-button'));
    await flushAsyncUpdates();
    selectNewChatOption('Environment', 'runner-2');
    await flushAsyncUpdates();
    expect(screen.getByLabelText('Model')).toHaveTextContent('gpt-5-mini');
    selectNewChatOption('Environment', 'runner-1');
    await flushAsyncUpdates();
    expect(screen.getByLabelText('Model')).toHaveTextContent('gpt-5');
    expect(screen.getByLabelText('Model')).not.toHaveTextContent('gpt-5-mini');
    selectNewChatOption('Model', 'gpt-5-mini');
    fireEvent.click(screen.getByRole('button', { name: 'Cancel' }));
    fireEvent.click(screen.getByTestId('composer-context-button'));
    await flushAsyncUpdates();
    expect(screen.getByLabelText('Model')).not.toHaveTextContent('gpt-5-mini');
  });

  it('keeps dialog focus and discovery stable on profile changes and cancels pending suggestions', async () => {
    vi.useFakeTimers();

    try {
      await renderChatWithRunner();
      fireEvent.click(screen.getByTestId('composer-context-button'));
      await flushAsyncUpdates();
      act(() => vi.advanceTimersByTime(0));
      const profileSelect = screen.getByLabelText('Profile');
      profileSelect.focus();
      const settingsRequests = mockGetChatSettings.mock.calls.length;

      selectNewChatOption('Profile', 'anthropic');
      await flushAsyncUpdates();
      act(() => vi.advanceTimersByTime(0));

      expect(profileSelect).toHaveFocus();
      expect(profileSelect).toHaveTextContent('anthropic');
      expect(mockGetChatSettings).toHaveBeenCalledTimes(settingsRequests + 1);
      expect(mockGetChatSettings).toHaveBeenLastCalledWith('anthropic', 'runner-1');

      fireEvent.change(screen.getByLabelText('Working directory'), {
        target: { value: '/workspace/cancelled' },
      });
      fireEvent.keyDown(window, { key: 'Escape' });
      await runCwdSuggestionDebounce();

      expect(screen.queryByTestId('new-chat-dialog')).not.toBeInTheDocument();
      expect(screen.getByTestId('composer-context-button')).toHaveTextContent(/^medium$/);
      expect(mockGetCWDHints).not.toHaveBeenCalled();
    } finally {
      vi.useRealTimers();
    }
  });

  it.each([
    'success',
    'failure',
  ])('ignores a stale runner profile discovery %s', async (outcome) => {
    const defaults: ChatSettings = await mockGetChatSettings();
    mockGetChatSettings.mockClear();
    mockGetRunners.mockResolvedValue({
      runners: [makeRunner(), makeRunner({ id: 'runner-2', displayName: 'second-runner' })],
    });
    let resolveOld: (settings: ChatSettings) => void = () => {};
    let rejectOld: (error: Error) => void = () => {};
    const oldRequest = new Promise<ChatSettings>((resolve, reject) => {
      resolveOld = resolve;
      rejectOld = reject;
    });
    mockGetChatSettings.mockImplementation((_profile?: string, runnerId?: string) => {
      if (runnerId === 'runner-1') return oldRequest;
      return Promise.resolve({
        ...defaults,
        model: 'new-model',
        modelOptions: ['new-model', 'new-model-mini'],
        profiles: [
          ...defaults.profiles,
          ...(runnerId ? [{ name: 'new-runner/search', scope: 'extension' }] : []),
        ],
      });
    });

    render(<ChatPage />);
    await flushAsyncUpdates();
    fireEvent.click(screen.getByTestId('sidebar-new-chat-button'));
    selectWorkspaceRunner();
    expect(mockGetChatSettings).toHaveBeenLastCalledWith(undefined, 'runner-1');
    expect(screen.getByRole('button', { name: 'Start' })).toBeDisabled();
    expect(screen.getByLabelText('Model')).toBeDisabled();
    selectNewChatOption('Environment', 'runner-2');
    await flushAsyncUpdates();
    expect(mockGetChatSettings).toHaveBeenLastCalledWith(undefined, 'runner-2');
    fireEvent.click(screen.getByRole('combobox', { name: 'Profile' }));
    expect(
      within(screen.getByRole('listbox', { name: 'Profile' })).getByRole('option', {
        name: 'new-runner/search',
      })
    ).toBeInTheDocument();
    fireEvent.click(screen.getByRole('combobox', { name: 'Profile' }));
    expect(screen.getByRole('button', { name: 'Start' })).toBeEnabled();

    await act(async () => {
      if (outcome === 'failure') {
        rejectOld(new Error('old runner unavailable'));
      } else {
        resolveOld({
          ...defaults,
          model: 'old-model',
          modelOptions: ['old-model'],
          profiles: [{ name: 'old-runner/search', scope: 'extension' }],
          reasoningEffort: 'none',
          reasoningEffortOptions: ['none'],
        });
      }
    });
    fireEvent.click(screen.getByRole('combobox', { name: 'Profile' }));
    const profiles = within(screen.getByRole('listbox', { name: 'Profile' }));
    expect(profiles.queryByRole('option', { name: 'old-runner/search' })).not.toBeInTheDocument();
    expect(profiles.getByRole('option', { name: 'new-runner/search' })).toBeInTheDocument();
    fireEvent.click(screen.getByRole('combobox', { name: 'Profile' }));
    expect(screen.getByLabelText('Reasoning effort')).toHaveTextContent('medium');
    expect(screen.getByLabelText('Environment')).toHaveTextContent('second-runner — worker — idle');
    expect(screen.getByLabelText('Model')).toHaveTextContent('new-model');
    fireEvent.click(screen.getByLabelText('Model'));
    const models = within(screen.getByRole('listbox', { name: 'Model' }));
    expect(models.queryByRole('option', { name: 'old-model' })).not.toBeInTheDocument();
    expect(models.getByRole('option', { name: 'new-model-mini' })).toBeInTheDocument();
    expect(mockGetChatSettings).toHaveBeenCalledTimes(3);
  });

  it('discards a pending profile response when its runner changes', async () => {
    const defaults: ChatSettings = await mockGetChatSettings();
    mockGetRunners.mockResolvedValue({ runners: [makeRunner(), makeRunner({ id: 'runner-2' })] });
    let resolveOld: (settings: ChatSettings) => void = () => {};
    const oldRequest = new Promise<ChatSettings>((resolve) => {
      resolveOld = resolve;
    });
    mockGetChatSettings.mockImplementation((profile?: string, runnerId?: string) => {
      if (profile === 'code-search') return oldRequest;
      return Promise.resolve({
        ...defaults,
        profiles: [
          ...defaults.profiles,
          ...(runnerId === 'runner-1' ? [{ name: 'code-search', scope: 'extension' }] : []),
        ],
      });
    });

    render(<ChatPage />);
    await flushAsyncUpdates();
    fireEvent.click(screen.getByTestId('sidebar-new-chat-button'));
    selectWorkspaceRunner();
    await flushAsyncUpdates();
    selectNewChatOption('Profile', 'code-search');
    expect(mockGetChatSettings).toHaveBeenLastCalledWith('code-search', 'runner-1');
    selectNewChatOption('Environment', 'runner-2');
    await flushAsyncUpdates();

    expect(screen.getByLabelText('Profile')).toHaveTextContent('work');
    fireEvent.click(screen.getByRole('combobox', { name: 'Profile' }));
    expect(
      within(screen.getByRole('listbox', { name: 'Profile' })).queryByRole('option', {
        name: 'code-search',
      })
    ).not.toBeInTheDocument();
    fireEvent.click(screen.getByRole('combobox', { name: 'Profile' }));
    await act(async () => {
      resolveOld({
        ...defaults,
        currentProfile: 'code-search',
        profiles: [{ name: 'code-search', scope: 'extension' }],
        reasoningEffort: 'none',
        reasoningEffortOptions: ['none'],
      });
    });
    expect(screen.getByLabelText('Profile')).toHaveTextContent('work');
    expect(screen.getByLabelText('Reasoning effort')).toHaveTextContent('medium');
    fireEvent.click(screen.getByRole('combobox', { name: 'Profile' }));
    expect(
      within(screen.getByRole('listbox', { name: 'Profile' })).queryByRole('option', {
        name: 'code-search',
      })
    ).not.toBeInTheDocument();
    fireEvent.click(screen.getByRole('combobox', { name: 'Profile' }));
    expect(screen.getByRole('button', { name: 'Start' })).toBeEnabled();
  });

  it.each([
    { profile: 'anthropic', expectedProfile: 'anthropic' },
    { profile: 'code-search', expectedProfile: 'work' },
  ])('revalidates the selected $profile on another runner', async ({
    profile,
    expectedProfile,
  }) => {
    const defaultSettings = mockGetChatSettings.getMockImplementation();
    mockGetChatSettings.mockImplementation(async (profile?: string, runnerId?: string) => {
      const settings = await defaultSettings?.(profile);
      return {
        ...settings,
        profiles: [
          ...settings.profiles,
          ...(runnerId === 'runner-1' ? [{ name: 'code-search', scope: 'extension' }] : []),
        ],
      };
    });
    mockGetRunners.mockResolvedValue({ runners: [makeRunner(), makeRunner({ id: 'runner-2' })] });
    render(<ChatPage />);
    await flushAsyncUpdates();
    fireEvent.click(screen.getByTestId('sidebar-new-chat-button'));
    selectWorkspaceRunner();
    await flushAsyncUpdates();
    selectNewChatOption('Profile', profile);
    await flushAsyncUpdates();
    selectNewChatOption('Reasoning effort', 'high');
    fireEvent.click(screen.getByRole('button', { name: 'Start' }));
    await flushAsyncUpdates();
    fireEvent.click(screen.getByRole('button', { name: 'Model settings: high' }));
    await flushAsyncUpdates();
    selectNewChatOption('Environment', 'runner-2');
    await flushAsyncUpdates();

    expect(mockGetChatSettings).toHaveBeenLastCalledWith(
      expectedProfile === 'work' ? undefined : expectedProfile,
      'runner-2'
    );
    expect(screen.getByLabelText('Profile')).toHaveTextContent(expectedProfile);
    fireEvent.click(screen.getByRole('combobox', { name: 'Profile' }));
    expect(
      within(screen.getByRole('listbox', { name: 'Profile' })).queryByRole('option', {
        name: 'code-search',
      })
    ).not.toBeInTheDocument();
    fireEvent.click(screen.getByRole('combobox', { name: 'Profile' }));
    expect(screen.getByLabelText('Reasoning effort')).toHaveTextContent('high');
    expect(screen.getByRole('button', { name: 'Start' })).toBeEnabled();
  });

  it('reverts the profile when its reasoning settings fail to load', async () => {
    const consoleError = vi.spyOn(console, 'error').mockImplementation(() => undefined);
    mockStreamChat.mockResolvedValue(undefined);

    try {
      render(<ChatPage />);

      await flushAsyncUpdates();
      await waitFor(() => expect(mockGetChatSettings).toHaveBeenCalledTimes(1));
      fireEvent.click(screen.getByTestId('sidebar-new-chat-button'));
      await waitFor(() => expect(screen.getByLabelText('Profile')).toHaveTextContent('work'));
      selectWorkspaceRunner();
      await waitFor(() => expect(screen.getByRole('button', { name: 'Start' })).toBeEnabled());
      mockGetChatSettings.mockRejectedValueOnce(new Error('profile settings unavailable'));
      selectNewChatOption('Profile', 'restricted');

      await waitFor(() =>
        expect(screen.getByTestId('new-chat-profile-select')).toHaveTextContent('work')
      );
      expect(screen.getByLabelText('Reasoning effort')).toHaveTextContent('medium');
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

  it('opens and closes the new chat settings dialog', async () => {
    render(<ChatPage />);

    await waitFor(() => expect(mockGetChatSettings).toHaveBeenCalled());

    fireEvent.click(screen.getByTestId('sidebar-new-chat-button'));
    expect(screen.getByTestId('new-chat-dialog')).toBeInTheDocument();
    selectWorkspaceRunner();
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
    expect(mockGetCWDHints).not.toHaveBeenCalled();

    fireEvent.click(screen.getByRole('button', { name: 'Cancel' }));
    expect(screen.queryByTestId('new-chat-dialog')).not.toBeInTheDocument();
  });

  it('closes the expanded Profile on Escape before dismissing the new chat dialog', async () => {
    render(<ChatPage />);
    await flushAsyncUpdates();
    fireEvent.click(screen.getByTestId('sidebar-new-chat-button'));
    const profile = screen.getByRole('combobox', { name: 'Profile' });
    profile.focus();
    fireEvent.click(profile);

    expect(screen.getByRole('listbox', { name: 'Profile' })).toBeInTheDocument();
    expect(profile).toHaveAttribute('aria-expanded', 'true');
    fireEvent.keyDown(profile, { key: 'Escape' });

    expect(screen.queryByRole('listbox', { name: 'Profile' })).not.toBeInTheDocument();
    expect(profile).toHaveAttribute('aria-expanded', 'false');
    expect(profile).toHaveFocus();
    expect(screen.getByRole('dialog', { name: 'New chat' })).toBeInTheDocument();
    fireEvent.keyDown(profile, { key: 'Escape' });

    expect(screen.queryByRole('dialog', { name: 'New chat' })).not.toBeInTheDocument();
  });

  it.each([
    { profile: 'anthropic', model: 'saved-claude', label: 'anthropic/saved-claude · high' },
    { profile: ' deep ', model: ' provider/model ', label: 'deep/provider/model · high' },
    { profile: undefined, model: 'saved-model', label: 'saved-model · high' },
    { profile: 'legacy', model: undefined, label: 'high' },
  ])('shows saved settings as $label in the read-only composer', async ({
    profile,
    model,
    label,
  }) => {
    const defaults: ChatSettings = await mockGetChatSettings();
    mockGetChatSettings.mockResolvedValue({
      ...defaults,
      model: 'gpt-5',
      modelOptions: ['gpt-5', 'gpt-5-mini'],
    });
    setRouteParams({ id: 'conv-123' });
    mockGetConversation.mockResolvedValue({
      runnerId: 'runner-1',
      id: 'conv-123',
      createdAt: '2023-01-01T00:00:00Z',
      updatedAt: '2023-01-02T00:00:00Z',
      messageCount: 1,
      profile,
      model,
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

    expect(screen.getByTestId('composer-inline-context').textContent).toBe(label);
    expect(screen.queryByTestId('composer-context-button')).not.toBeInTheDocument();
    expect(screen.queryByRole('button', { name: /^Change workspace:/ })).not.toBeInTheDocument();
    expect(screen.queryByLabelText('Profile')).not.toBeInTheDocument();
    expect(screen.queryByLabelText('Model')).not.toBeInTheDocument();
    expect(screen.queryByLabelText('Reasoning effort')).not.toBeInTheDocument();
    await waitFor(() =>
      expect(mockGetSlashCommands).toHaveBeenLastCalledWith(undefined, {
        runnerId: 'runner-1',
        environmentProfile: '',
        conversationId: 'conv-123',
        profile: undefined,
      })
    );

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
    expect(mockStreamChat.mock.calls[0][0].options).toBeUndefined();
  });

  it('shows the full workspace path title and reopens settings without losing the draft or selection', async () => {
    const user = userEvent.setup();
    const cwd = '/runner/a-very-long-workspace-name/another-long-directory/kodelet';
    const defaults: ChatSettings = await mockGetChatSettings();
    mockGetChatSettings.mockResolvedValue({
      ...defaults,
      model: 'gpt-5',
      modelOptions: ['gpt-5', 'gpt-5-mini'],
    });

    await renderChatWithRunner();
    fireEvent.change(screen.getByTestId('composer-textarea'), {
      target: { value: 'Keep this unsent message' },
    });
    await user.click(screen.getByTestId('composer-context-button'));
    await waitFor(() => expect(screen.getByLabelText('Model')).toBeEnabled());
    selectNewChatOption('Model', 'gpt-5-mini');
    selectNewChatOption('Reasoning effort', 'high');
    fireEvent.change(screen.getByLabelText('Working directory'), { target: { value: cwd } });
    await user.click(screen.getByRole('button', { name: 'Start' }));

    const header = screen.getByTestId('chat-workspace-header');
    expect(within(header).getByTitle(cwd)).toHaveTextContent(cwd);
    const workspaceButton = within(header).getByRole('button', {
      name: `Change workspace: ${cwd}`,
    });
    const contextButton = screen.getByTestId('composer-quick-pick');
    expect(contextButton).toHaveTextContent(/^work\/gpt-5-mini · high$/);
    expect(contextButton).not.toHaveTextContent(cwd);

    await user.click(workspaceButton);
    await waitFor(() => expect(screen.getByLabelText('Model')).toBeEnabled());
    expect(screen.getByLabelText('Working directory')).toHaveValue(cwd);
    expect(screen.getByLabelText('Profile')).toHaveTextContent('work');
    expect(screen.getByLabelText('Model')).toHaveTextContent('gpt-5-mini');
    expect(screen.getByLabelText('Reasoning effort')).toHaveTextContent('high');
    expect(screen.getByTestId('composer-textarea')).toHaveValue('Keep this unsent message');
    selectNewChatOption('Model', 'gpt-5');
    fireEvent.change(screen.getByLabelText('Working directory'), {
      target: { value: '/cancelled' },
    });
    await user.click(screen.getByRole('button', { name: 'Cancel' }));

    await waitFor(() => expect(workspaceButton).toHaveFocus());
    expect(screen.queryByTestId('new-chat-dialog')).not.toBeInTheDocument();
    expect(within(header).getByTitle(cwd)).toHaveTextContent(cwd);
    expect(screen.getByTestId('composer-quick-pick')).toHaveTextContent(
      /^work\/gpt-5-mini · high$/
    );
    expect(screen.getByTestId('composer-textarea')).toHaveValue('Keep this unsent message');
  });
});
