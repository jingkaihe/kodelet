import type React from 'react';
import { fireEvent, render, screen, within } from '@testing-library/react';
import { describe, expect, it, vi } from 'vitest';
import NewChatContextDialog from './NewChatContextDialog';
import {
  sampleCwdHints,
  sampleProfiles,
  sampleConversations,
} from '../../stories/fixtures';

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
    onCancel: vi.fn(),
    onCommit: vi.fn(),
    onCwdInputBlur: vi.fn(),
    onCwdInputChange: vi.fn(),
    onCwdInputFocus: vi.fn(),
    onCwdInputKeyDown: vi.fn(),
    onProfileDraftChange: vi.fn(),
    onReasoningEffortDraftChange: vi.fn(),
    onRecentWorkspaceSelect: vi.fn(),
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

    expect(screen.getByRole('dialog', { name: 'New chat' })).toHaveAttribute(
      'aria-modal',
      'true'
    );

    const selectedWorkspace = screen.getByRole('button', {
      name: '~/workspace/kodelet',
    });
    expect(selectedWorkspace).toHaveAttribute('aria-pressed', 'true');
    expect(within(selectedWorkspace).getByText('~/workspace')).toBeVisible();
    expect(
      screen.getByRole('button', { name: '~/workspace/comet' })
    ).toHaveAttribute('aria-pressed', 'false');

    fireEvent.click(
      screen.getByRole('button', { name: 'Close new chat dialog' })
    );
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
    expect(props.onRecentWorkspaceSelect).toHaveBeenCalledWith(
      '/home/jingkaihe/workspace/plugins'
    );
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
});
