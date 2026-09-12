import { fireEvent, render, screen } from '@testing-library/react';
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
    profileDraft: 'default',
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

  it('emits profile and directory changes without owning page state', () => {
    const props = renderDialog();

    fireEvent.change(screen.getByTestId('new-chat-profile-select'), {
      target: { value: 'code-review' },
    });
    fireEvent.change(screen.getByTestId('new-chat-reasoning-effort-select'), {
      target: { value: 'high' },
    });
    fireEvent.change(screen.getByTestId('cwd-input'), {
      target: { value: '/tmp/project' },
    });
    fireEvent.click(screen.getByTestId('cwd-suggestion-1'));

    expect(props.onProfileDraftChange).toHaveBeenCalledWith('code-review');
    expect(props.onReasoningEffortDraftChange).toHaveBeenCalledWith('high');
    expect(props.onCwdInputChange).toHaveBeenCalledWith('/tmp/project');
    expect(props.onSelectCwdSuggestion).toHaveBeenCalledWith(
      '/home/jingkaihe/workspace/kodelet/pkg/webui/frontend'
    );
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
    renderDialog({ runnerIdDraft: '' });

    expect(screen.getByRole('option', { name: 'Select a workspace runner' })).toBeDisabled();
    expect(
      screen.queryByRole('option', { name: 'Local control-plane workspace' })
    ).not.toBeInTheDocument();
    expect(screen.queryByTestId('cwd-input')).not.toBeInTheDocument();
    expect(screen.getByText('Workspace runner required')).toBeVisible();
    expect(screen.getByRole('button', { name: 'Start' })).toBeDisabled();
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
    fireEvent.change(screen.getByTestId('new-chat-runner-select'), {
      target: { value: '' },
    });
    expect(props.onRunnerDraftChange).toHaveBeenCalledWith('');
  });

  it('allows concurrent busy runners but disables legacy busy and offline runners', () => {
    renderDialog({
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

    expect(screen.getByRole('option', { name: /busy — worker — 2 active/ })).toBeEnabled();
    expect(screen.getByRole('option', { name: /legacy — worker — 1 active/ })).toBeDisabled();
    expect(screen.getByRole('option', { name: /offline — worker — offline/ })).toBeDisabled();
    expect(screen.getByRole('button', { name: 'Start' })).toBeEnabled();
  });
});
