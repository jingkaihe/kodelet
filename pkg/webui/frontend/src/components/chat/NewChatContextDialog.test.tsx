import type React from 'react';
import { fireEvent, render, screen } from '@testing-library/react';
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
    recentWorkspaces,
    onCancel: vi.fn(),
    onCommit: vi.fn(),
    onCwdInputBlur: vi.fn(),
    onCwdInputChange: vi.fn(),
    onCwdInputFocus: vi.fn(),
    onCwdInputKeyDown: vi.fn(),
    onProfileDraftChange: vi.fn(),
    onRecentWorkspaceSelect: vi.fn(),
    onSelectCwdSuggestion: vi.fn(),
    ...overrides,
  };

  render(<NewChatContextDialog {...props} />);

  return props;
};

describe('NewChatContextDialog', () => {
  it('emits profile and directory changes without owning page state', () => {
    const props = renderDialog();

    fireEvent.change(screen.getByTestId('new-chat-profile-select'), {
      target: { value: 'code-review' },
    });
    fireEvent.change(screen.getByTestId('cwd-input'), {
      target: { value: '/tmp/project' },
    });
    fireEvent.click(screen.getByTestId('cwd-suggestion-1'));
    fireEvent.click(screen.getByLabelText('/home/jingkaihe/workspace/plugins'));

    expect(props.onProfileDraftChange).toHaveBeenCalledWith('code-review');
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
});
