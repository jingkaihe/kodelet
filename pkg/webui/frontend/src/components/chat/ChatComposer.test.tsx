import type React from 'react';
import { fireEvent, render, screen, waitFor } from '@testing-library/react';
import { describe, expect, it, vi } from 'vitest';
import ChatComposer from './ChatComposer';
import { sampleAttachment, sampleSlashCommands } from '../../stories/fixtures';

const renderComposer = (
  overrides: Partial<React.ComponentProps<typeof ChatComposer>> = {}
) => {
  const props: React.ComponentProps<typeof ChatComposer> = {
    addImageDisabled: false,
    attachments: [],
    canStop: false,
    contextDisabled: false,
    contextIsStatic: false,
    contextText: 'default · kodelet',
    dragActive: false,
    draft: 'hello',
    expanded: false,
    placeholder: 'Ask kodelet anything...',
    showStop: false,
    slashCommandIndex: -1,
    slashCommandSuggestions: [],
    slashCommandSuggestionsOpen: false,
    slashUsageHint: '',
    stopActionLabel: 'Stop',
    streamError: null,
    submitActionLabel: 'Send',
    submitDisabled: false,
    textareaDisabled: false,
    onAttachImages: vi.fn(),
    onContextOpen: vi.fn(),
    onDragLeave: vi.fn(),
    onDragOver: vi.fn(),
    onDrop: vi.fn(),
    onDraftChange: vi.fn(),
    onDraftKeyDown: vi.fn(),
    onGitDiffOpen: vi.fn(),
    onPaste: vi.fn(),
    onRemoveAttachment: vi.fn(),
    onSelectSlashCommand: vi.fn(),
    onStop: vi.fn(),
    onSubmit: vi.fn(),
    onTerminalOpen: vi.fn(),
    onToggleExpanded: vi.fn(),
    ...overrides,
  };

  render(<ChatComposer {...props} />);

  return props;
};

describe('ChatComposer', () => {
  it('emits composer actions through props', () => {
    const props = renderComposer();

    fireEvent.change(screen.getByTestId('composer-textarea'), {
      target: { value: 'next draft' },
    });
    fireEvent.click(screen.getByLabelText('Send'));
    fireEvent.click(screen.getByTestId('composer-git-diff-toggle'));
    fireEvent.click(screen.getByTestId('composer-terminal-toggle'));
    fireEvent.click(screen.getByTestId('composer-expand-toggle'));
    fireEvent.click(screen.getByText('default · kodelet'));

    expect(props.onDraftChange).toHaveBeenCalledWith('next draft');
    expect(props.onSubmit).toHaveBeenCalledTimes(1);
    expect(props.onGitDiffOpen).toHaveBeenCalledTimes(1);
    expect(props.onTerminalOpen).toHaveBeenCalledTimes(1);
    expect(props.onToggleExpanded).toHaveBeenCalledTimes(1);
    expect(props.onContextOpen).toHaveBeenCalledTimes(1);
  });

  it('renders attachment previews and slash command suggestions', () => {
    const props = renderComposer({
      attachments: [sampleAttachment],
      slashCommandIndex: 0,
      slashCommandSuggestions: sampleSlashCommands,
      slashCommandSuggestionsOpen: true,
      slashUsageHint: '/review frontend extraction',
    });

    fireEvent.click(screen.getByLabelText(`Remove ${sampleAttachment.name}`));
    fireEvent.click(screen.getByText('/review'));

    expect(screen.getByAltText(sampleAttachment.name)).toBeInTheDocument();
    expect(screen.getByTestId('composer-slash-usage-hint')).toHaveTextContent(
      '/review frontend extraction'
    );
    expect(props.onRemoveAttachment).toHaveBeenCalledWith(sampleAttachment.id);
    expect(props.onSelectSlashCommand).toHaveBeenCalledWith('review');
  });

  it('passes files selected by the hidden image input to the page', async () => {
    const onAttachImages = vi.fn();
    renderComposer({ onAttachImages });

    const file = new File(['image-data'], 'capture.png', { type: 'image/png' });
    fireEvent.change(screen.getByTestId('composer-image-input'), {
      target: { files: [file] },
    });

    await waitFor(() => {
      expect(onAttachImages).toHaveBeenCalledWith([file]);
    });
  });
});
