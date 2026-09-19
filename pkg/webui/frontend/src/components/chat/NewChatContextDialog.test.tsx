import { fireEvent, render, screen, within } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import type React from 'react';
import { describe, expect, it, vi } from 'vitest';
import { sampleCwdHints, sampleProfiles } from '../../stories/fixtures';
import NewChatContextDialog from './NewChatContextDialog';

const renderDialog = (
  overrides: Partial<React.ComponentProps<typeof NewChatContextDialog>> = {}
) => {
  const props: React.ComponentProps<typeof NewChatContextDialog> = {
    availableProfiles: sampleProfiles,
    cwdQuery: '/home/jingkaihe/workspace/kodelet',
    cwdSuggestionIndex: 0,
    cwdSuggestions: sampleCwdHints,
    cwdSuggestionsOpen: true,
    profileDraft: 'flair',
    reasoningEffortDraft: 'medium',
    reasoningEffortLoading: false,
    reasoningEffortOptions: ['low', 'medium', 'high'],
    runners: [
      {
        id: 'runner-1',
        host: { instanceId: 'host-1', hostname: 'worker', os: 'linux', arch: 'amd64' },
        workspace: { path: '/workspace/kodelet', name: 'kodelet' },
        manifestChanged: false,
        status: 'idle',
        connected: true,
        generation: 1,
      },
    ],
    runnerIdDraft: 'runner-1',
    environmentProfileDraft: '',
    onCancel: vi.fn(),
    onCommit: vi.fn(),
    onCwdInputBlur: vi.fn(),
    onCwdInputChange: vi.fn(),
    onCwdInputFocus: vi.fn(),
    onCwdInputKeyDown: vi.fn(),
    onProfileDraftChange: vi.fn(),
    onReasoningEffortDraftChange: vi.fn(),
    onRunnerDraftChange: vi.fn(),
    onEnvironmentProfileDraftChange: vi.fn(),
    onSelectCwdSuggestion: vi.fn(),
    ...overrides,
  };

  render(<NewChatContextDialog {...props} />);

  return props;
};

describe('NewChatContextDialog', () => {
  it('presents a labeled modal with the selected runner workspace', () => {
    const props = renderDialog();

    expect(screen.getByRole('dialog', { name: 'New chat' })).toHaveAttribute('aria-modal', 'true');
    expect(screen.getByText('/workspace/kodelet')).toBeVisible();
    expect(screen.queryByTestId('recent-workspaces')).not.toBeInTheDocument();

    fireEvent.click(screen.getByRole('button', { name: 'Close new chat dialog' }));
    expect(props.onCancel).toHaveBeenCalledTimes(1);
  });

  it.each([
    'code-review',
    'default',
  ])('emits %s and directory changes without owning page state', (name) => {
    const props = renderDialog({
      availableProfiles: [...sampleProfiles, { name: 'default', scope: 'global' }],
    });

    const profile = screen.getByRole('combobox', { name: 'Profile' });
    // fireEvent does not provide the browser's implicit click-to-focus behavior.
    fireEvent.click(profile);
    expect(profile).toHaveFocus();
    const options = screen.getByRole('listbox', { name: 'Profile' });
    expect(within(options).getAllByRole('option')).toHaveLength(4);
    expect(within(options).getByRole('option', { name: 'flair (Default)' })).toHaveAttribute(
      'aria-selected',
      'true'
    );
    fireEvent.click(within(options).getByRole('option', { name }));
    fireEvent.click(screen.getByRole('combobox', { name: 'Reasoning effort' }));
    fireEvent.click(screen.getByRole('option', { name: 'high' }));
    fireEvent.change(screen.getByTestId('cwd-input'), {
      target: { value: '/tmp/project' },
    });
    fireEvent.click(screen.getByTestId('cwd-suggestion-1'));

    expect(props.onProfileDraftChange).toHaveBeenCalledWith(name);
    expect(props.onReasoningEffortDraftChange).toHaveBeenCalledWith('high');
    expect(props.onCwdInputChange).toHaveBeenCalledWith('/tmp/project');
    expect(props.onSelectCwdSuggestion).toHaveBeenCalledWith(
      '/home/jingkaihe/workspace/kodelet/pkg/webui/frontend'
    );
  });

  it.each([
    { name: 'empty', availableProfiles: [] },
    { name: 'configured', availableProfiles: sampleProfiles },
  ])('requires a resolved profile with $name options', ({ availableProfiles }) => {
    renderDialog({ profileDraft: '', availableProfiles });

    const profile = screen.getByRole('combobox', { name: 'Profile' });
    expect(profile).toHaveTextContent('Select a profile');
    expect(screen.getByRole('button', { name: 'Start' })).toBeDisabled();
    if (availableProfiles.length === 0) {
      expect(profile).toBeDisabled();
      return;
    }
    fireEvent.click(profile);
    const options = screen.getByRole('listbox', { name: 'Profile' });
    expect(within(options).queryByRole('option', { selected: true })).not.toBeInTheDocument();
    expect(within(options).queryByRole('option', { name: 'default' })).not.toBeInTheDocument();
  });

  it('exposes the directory autocomplete and its active suggestion to assistive technology', () => {
    const props = renderDialog();
    const input = screen.getByRole('combobox', { name: 'Working directory' });
    const suggestions = screen.getByRole('listbox', { name: 'Working directory suggestions' });
    const activeSuggestion = screen.getByTestId('cwd-suggestion-0');

    expect(input).toHaveAttribute('aria-expanded', 'true');
    expect(input).toHaveAttribute('aria-controls', suggestions.id);
    expect(input).toHaveAttribute('aria-activedescendant', activeSuggestion.id);
    expect(activeSuggestion).toHaveAttribute('aria-selected', 'true');
    fireEvent.keyDown(input, { key: 'ArrowDown' });
    expect(props.onCwdInputKeyDown).toHaveBeenCalledTimes(1);
  });

  it('does not reference hidden directory suggestions', () => {
    renderDialog({ cwdSuggestionsOpen: false });
    const input = screen.getByRole('combobox', { name: 'Working directory' });

    expect(input).toHaveAttribute('aria-expanded', 'false');
    expect(input).not.toHaveAttribute('aria-controls');
    expect(input).not.toHaveAttribute('aria-activedescendant');
    expect(screen.queryByRole('listbox')).not.toBeInTheDocument();
  });

  it('keeps dialog actions external', () => {
    const props = renderDialog();

    fireEvent.click(screen.getByRole('button', { name: 'Cancel' }));
    fireEvent.click(screen.getByRole('button', { name: 'Start' }));

    expect(props.onCancel).toHaveBeenCalledTimes(1);
    expect(props.onCommit).toHaveBeenCalledTimes(1);
  });

  it('prevents starting while reasoning settings are loading', () => {
    renderDialog({ reasoningEffortLoading: true });

    expect(screen.getByLabelText('Reasoning effort')).toBeDisabled();
    expect(screen.getByRole('button', { name: 'Start' })).toBeDisabled();
  });

  it('requires a workspace runner', () => {
    const props = renderDialog({ runnerIdDraft: '' });

    fireEvent.click(screen.getByRole('combobox', { name: 'Environment' }));
    expect(screen.getByRole('option', { name: 'Select a workspace runner' })).toBeDisabled();
    expect(
      screen.queryByRole('option', { name: 'Local control-plane workspace' })
    ).not.toBeInTheDocument();
    expect(screen.queryByTestId('cwd-input')).not.toBeInTheDocument();
    expect(screen.getByText('Workspace runner required')).toBeVisible();
    expect(screen.getByRole('button', { name: 'Start' })).toBeDisabled();
    fireEvent.click(screen.getByRole('option', { name: 'kodelet — worker — idle' }));
    expect(props.onRunnerDraftChange).toHaveBeenCalledWith('runner-1');
  });

  it('selects an available runner and accepts a runner-host cwd', () => {
    const props = renderDialog({
      runners: [
        {
          id: 'runner-1',
          displayName: 'kodelet-gpu',
          host: {
            instanceId: 'host-1',
            hostname: 'worker',
            os: 'linux',
            arch: 'amd64',
          },
          workspace: { path: '/workspace/kodelet', name: 'kodelet' },
          manifestChanged: true,
          status: 'idle',
          connected: true,
          concurrentRuns: true,
          generation: 2,
        },
      ],
      runnerIdDraft: 'runner-1',
      cwdQuery: '',
    });

    expect(screen.getByText('/workspace/kodelet')).toBeVisible();
    expect(screen.getByLabelText('Runner profile')).toBeVisible();
    const cwdInput = screen.getByTestId('cwd-input');
    expect(cwdInput).toHaveAttribute('placeholder', '/workspace/kodelet');
    fireEvent.change(cwdInput, { target: { value: '/workspace/other-project' } });
    expect(props.onCwdInputChange).toHaveBeenCalledWith('/workspace/other-project');
    fireEvent.change(screen.getByLabelText('Runner profile'), {
      target: { value: 'gpu' },
    });
    expect(props.onEnvironmentProfileDraftChange).toHaveBeenCalledWith('gpu');
  });

  it('allows concurrent busy runners but disables legacy busy and offline runners', () => {
    const props = renderDialog({
      runners: [
        {
          id: 'runner-busy',
          host: {
            instanceId: 'host-1',
            hostname: 'worker',
            os: 'linux',
            arch: 'amd64',
          },
          workspace: { path: '/workspace/busy', name: 'busy' },
          manifestChanged: false,
          status: 'busy',
          connected: true,
          concurrentRuns: true,
          activeRunId: 'run-1',
          activeRunIds: ['run-1', 'run-2'],
          generation: 1,
        },
        {
          id: 'runner-legacy-busy',
          host: {
            instanceId: 'host-legacy',
            hostname: 'worker',
            os: 'linux',
            arch: 'amd64',
          },
          workspace: { path: '/workspace/legacy', name: 'legacy' },
          manifestChanged: false,
          status: 'busy',
          connected: true,
          concurrentRuns: false,
          generation: 1,
        },
        {
          id: 'runner-offline',
          host: {
            instanceId: 'host-2',
            hostname: 'worker',
            os: 'linux',
            arch: 'amd64',
          },
          workspace: { path: '/workspace/offline', name: 'offline' },
          manifestChanged: false,
          status: 'offline',
          connected: false,
          concurrentRuns: false,
          generation: 1,
        },
      ],
      runnerIdDraft: 'runner-busy',
    });

    const environment = screen.getByRole('combobox', { name: 'Environment' });
    fireEvent.click(environment);
    expect(screen.getByRole('option', { name: /busy — worker — 2 active/ })).toBeEnabled();
    expect(screen.getByRole('option', { name: /legacy — worker — 1 active/ })).toBeDisabled();
    expect(screen.getByRole('option', { name: /offline — worker — offline/ })).toBeDisabled();
    expect(screen.getByRole('button', { name: 'Start' })).toBeEnabled();
    fireEvent.click(screen.getByRole('option', { name: /offline — worker — offline/ }));
    expect(props.onRunnerDraftChange).not.toHaveBeenCalled();
    fireEvent.keyDown(environment, { key: 'End' });
    expect(environment).toHaveAttribute(
      'aria-activedescendant',
      screen.getByRole('option', { name: /busy — worker — 2 active/ }).id
    );
  });

  it('supports keyboard navigation, typeahead, selection, and Escape without changing the draft', async () => {
    const user = userEvent.setup();
    const props = renderDialog({ cwdSuggestionsOpen: false });
    const profile = screen.getByRole('combobox', { name: 'Profile' });
    profile.focus();
    await user.keyboard('{ArrowDown}');
    const menu = screen.getByRole('listbox', { name: 'Profile' });
    const selected = within(menu).getByRole('option', { name: 'flair (Default)' });
    expect(profile).toHaveFocus();
    expect(profile).toHaveAttribute('aria-controls', menu.id);
    expect(profile).toHaveAttribute('aria-activedescendant', selected.id);
    expect(selected).toHaveAttribute('aria-selected', 'true');
    await user.keyboard('{End}');
    const options = within(menu).getAllByRole('option');
    expect(profile).toHaveAttribute('aria-activedescendant', options[options.length - 1].id);
    await user.keyboard('{Home}code{Enter}');
    expect(props.onProfileDraftChange).toHaveBeenCalledWith('code-review');
    expect(profile).toHaveFocus();
    expect(profile).toHaveAttribute('aria-expanded', 'false');
    expect(profile).not.toHaveAttribute('aria-activedescendant');
    expect(profile).not.toHaveAttribute('aria-controls');
    await user.keyboard('{ArrowDown}{ArrowDown}{Escape}');
    expect(props.onProfileDraftChange).toHaveBeenCalledTimes(1);
    expect(props.onCancel).not.toHaveBeenCalled();
    expect(profile).toHaveFocus();
    expect(screen.queryByRole('listbox')).not.toBeInTheDocument();
  });

  it('dismisses on outside clicks and blur, and commits keyboard selection on Tab', async () => {
    const user = userEvent.setup();
    const props = renderDialog({ cwdSuggestionsOpen: false });
    const profile = screen.getByRole('combobox', { name: 'Profile' });
    await user.click(profile);
    await user.click(screen.getByRole('heading', { name: 'New chat' }));
    expect(profile).toHaveAttribute('aria-expanded', 'false');
    await user.click(profile);
    await user.click(screen.getByRole('combobox', { name: 'Reasoning effort' }));
    expect(screen.queryByRole('listbox', { name: 'Profile' })).not.toBeInTheDocument();
    await user.keyboard('{End}{Tab}');
    expect(props.onReasoningEffortDraftChange).toHaveBeenCalledWith('high');
    expect(screen.queryByRole('listbox')).not.toBeInTheDocument();
    expect(screen.getByRole('combobox', { name: 'Environment' })).toHaveFocus();
  });

  it('keeps a single reasoning option disabled and handles an empty runner list', async () => {
    const user = userEvent.setup();
    const props = renderDialog({
      reasoningEffortOptions: ['none'],
      reasoningEffortDraft: 'none',
      runners: [],
      runnerIdDraft: '',
      cwdSuggestionsOpen: false,
    });
    await user.click(screen.getByRole('combobox', { name: 'Reasoning effort' }));
    expect(screen.queryByRole('listbox')).not.toBeInTheDocument();
    const environment = screen.getByRole('combobox', { name: 'Environment' });
    await user.click(environment);
    await user.keyboard('{ArrowDown}{End}');
    expect(environment).not.toHaveAttribute('aria-activedescendant');
    await user.keyboard('{Enter}');
    expect(props.onRunnerDraftChange).not.toHaveBeenCalled();
  });
});
