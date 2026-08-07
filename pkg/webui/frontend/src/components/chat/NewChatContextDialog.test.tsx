import type React from 'react';
import { fireEvent, render, screen, within } from '@testing-library/react';
import { describe, expect, it, vi } from 'vitest';
import NewChatContextDialog from './NewChatContextDialog';
import { sampleCwdHints, sampleProfiles, sampleConversations } from '../../stories/fixtures';

const recentWorkspaces = Array.from(
  new Set(
    sampleConversations
      .map((conversation) => conversation.cwd)
      .filter((cwd): cwd is string => Boolean(cwd))
  )
);

const renderDialog = (
  overrides: Partial<React.ComponentProps<typeof NewChatContextDialog>> = {}
) => {
  const props: React.ComponentProps<typeof NewChatContextDialog> = {
    availableProfiles: sampleProfiles,
    cwdQuery: '/home/jingkaihe/workspace/kodelet',
    cwdSuggestionIndex: 0,
    cwdSuggestions: sampleCwdHints,
    cwdSuggestionsOpen: true,
    defaultCWD: '/home/jingkaihe/workspace/kodelet',
    profileDraft: 'default',
    reasoningEffortDraft: 'medium',
    reasoningEffortLoading: false,
    reasoningEffortOptions: ['low', 'medium', 'high'],
    recentWorkspaces,
    runners: [],
    runnerIdDraft: '',
    environmentProfileDraft: '',
    onCancel: vi.fn(),
    onCommit: vi.fn(),
    onCwdInputBlur: vi.fn(),
    onCwdInputChange: vi.fn(),
    onCwdInputFocus: vi.fn(),
    onCwdInputKeyDown: vi.fn(),
    onProfileDraftChange: vi.fn(),
    onReasoningEffortDraftChange: vi.fn(),
    onRecentWorkspaceSelect: vi.fn(),
    onRunnerDraftChange: vi.fn(),
    onEnvironmentProfileDraftChange: vi.fn(),
    onSelectCwdSuggestion: vi.fn(),
    ...overrides,
  };

  render(<NewChatContextDialog {...props} />);

  return props;
};

describe('NewChatContextDialog', () => {
  it('presents a labeled modal and highlights the current workspace', () => {
    const props = renderDialog({
      cwdQuery: '~/workspace/kodelet',
      recentWorkspaces: ['~/workspace/kodelet', '~/workspace/comet'],
    });

    expect(screen.getByRole('dialog', { name: 'New chat' })).toHaveAttribute('aria-modal', 'true');

    const selectedWorkspace = screen.getByRole('button', {
      name: '~/workspace/kodelet',
    });
    expect(selectedWorkspace).toHaveAttribute('aria-pressed', 'true');
    expect(within(selectedWorkspace).getByText('~/workspace')).toBeVisible();
    expect(screen.getByRole('button', { name: '~/workspace/comet' })).toHaveAttribute(
      'aria-pressed',
      'false'
    );

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
    fireEvent.click(screen.getByLabelText('/home/jingkaihe/workspace/plugins'));

    expect(props.onProfileDraftChange).toHaveBeenCalledWith('code-review');
    expect(props.onReasoningEffortDraftChange).toHaveBeenCalledWith('high');
    expect(props.onCwdInputChange).toHaveBeenCalledWith('/tmp/project');
    expect(props.onSelectCwdSuggestion).toHaveBeenCalledWith(
      '/home/jingkaihe/workspace/kodelet/pkg/webui/frontend'
    );
    expect(props.onRecentWorkspaceSelect).toHaveBeenCalledWith('/home/jingkaihe/workspace/plugins');
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

  it('selects an available runner and replaces local cwd controls', () => {
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
          generation: 2,
        },
      ],
      runnerIdDraft: 'runner-1',
    });

    expect(screen.getByText('/workspace/kodelet')).toBeVisible();
    expect(screen.getByLabelText('Runner profile')).toBeVisible();
    expect(screen.queryByTestId('cwd-input')).not.toBeInTheDocument();
    fireEvent.change(screen.getByLabelText('Runner profile'), {
      target: { value: 'gpu' },
    });
    expect(props.onEnvironmentProfileDraftChange).toHaveBeenCalledWith('gpu');
    fireEvent.change(screen.getByTestId('new-chat-runner-select'), {
      target: { value: '' },
    });
    expect(props.onRunnerDraftChange).toHaveBeenCalledWith('');
  });

  it('disables busy and offline runners and prevents stale runner submission', () => {
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
          generation: 1,
        },
      ],
      runnerIdDraft: 'runner-busy',
    });

    expect(screen.getByRole('option', { name: /busy — worker — busy/ })).toBeDisabled();
    expect(screen.getByRole('option', { name: /offline — worker — offline/ })).toBeDisabled();
    expect(screen.getByRole('button', { name: 'Start' })).toBeDisabled();
  });
});
