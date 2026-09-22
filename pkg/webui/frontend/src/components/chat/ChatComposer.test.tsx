import { fireEvent, render, screen, waitFor } from '@testing-library/react';
import type React from 'react';
import { describe, expect, it, vi } from 'vitest';
import { sampleAttachment, sampleSlashCommands } from '../../stories/fixtures';
import ChatComposer from './ChatComposer';

const renderComposer = (overrides: Partial<React.ComponentProps<typeof ChatComposer>> = {}) => {
  const props: React.ComponentProps<typeof ChatComposer> = {
    addImageDisabled: false,
    attachments: [],
    canStop: false,
    contextDisabled: false,
    contextIsStatic: false,
    contextText: 'gpt-5 · medium',
    dragActive: false,
    draft: 'hello',
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
    onPaste: vi.fn(),
    onRemoveAttachment: vi.fn(),
    onSelectSlashCommand: vi.fn(),
    onStop: vi.fn(),
    onSubmit: vi.fn(),
    ...overrides,
  };

  const view = render(<ChatComposer {...props} />);

  return {
    ...props,
    rerenderComposer: (nextOverrides: Partial<React.ComponentProps<typeof ChatComposer>>) =>
      view.rerender(<ChatComposer {...props} {...nextOverrides} />),
  };
};

describe('ChatComposer', () => {
  it('emits composer actions through props', () => {
    const props = renderComposer();
    const addImageButton = screen.getByLabelText('Add image');

    fireEvent.change(screen.getByTestId('composer-textarea'), {
      target: { value: 'next draft' },
    });
    fireEvent.click(screen.getByLabelText('Send'));
    const contextButton = screen.getByRole('button', { name: 'Model settings: gpt-5 · medium' });
    fireEvent.click(contextButton);

    expect(contextButton).toBe(screen.getByTestId('composer-context-button'));
    expect(contextButton).toHaveTextContent(/^gpt-5 · medium$/);
    expect(screen.getByLabelText('Send')).toHaveAttribute('title', 'Send (Shift+Enter)');
    expect(addImageButton.querySelector('svg')).toHaveClass('lucide-paperclip');
    expect(screen.getByTestId('composer-textarea').parentElement).toHaveClass(
      'composer-control-grid'
    );
    expect(screen.getByTestId('composer-textarea')).toHaveAttribute('rows', '3');
    expect(screen.queryByTestId('composer-expand-toggle')).not.toBeInTheDocument();
    expect(props.onDraftChange).toHaveBeenCalledWith('next draft');
    expect(props.onSubmit).toHaveBeenCalledTimes(1);
    expect(props.onContextOpen).toHaveBeenCalledTimes(1);
  });

  it('renders saved model settings without an editable context button', () => {
    renderComposer({ contextIsStatic: true, contextText: 'saved-claude · high' });

    expect(screen.getByTestId('composer-inline-context')).toHaveTextContent(
      /^saved-claude · high$/
    );
    expect(screen.queryByTestId('composer-context-button')).not.toBeInTheDocument();
    expect(screen.queryByRole('button', { name: /^Model settings:/ })).not.toBeInTheDocument();
  });

  it('offers profile/model and reasoning effort quick picks with a gear for full settings', () => {
    const quickPick = {
      modelValue: 'deep\u0000gpt-6-astra',
      modelOptions: [
        { value: 'deep\u0000gpt-6-astra', label: 'deep/gpt-6-astra' },
        { value: 'flair\u0000claude-opus-4-6', label: 'flair/claude-opus-4-6' },
      ],
      modelOptionsLoading: false,
      reasoningEffort: 'xhigh',
      reasoningEffortOptions: ['medium', 'xhigh'],
      onModelChange: vi.fn(),
      onModelMenuOpen: vi.fn(),
      onReasoningEffortChange: vi.fn(),
    };
    const props = renderComposer({ contextText: 'deep/gpt-6-astra · xhigh', quickPick });

    expect(screen.getByTestId('composer-quick-pick')).toHaveTextContent(
      /^deep\/gpt-6-astra · xhigh$/
    );
    expect(screen.queryByRole('listbox')).not.toBeInTheDocument();

    const modelTrigger = screen.getByRole('combobox', { name: 'Model quick pick' });
    fireEvent.click(modelTrigger);
    expect(quickPick.onModelMenuOpen).toHaveBeenCalledTimes(1);
    fireEvent.click(screen.getByRole('option', { name: 'flair/claude-opus-4-6' }));
    expect(quickPick.onModelChange).toHaveBeenCalledWith('flair\u0000claude-opus-4-6');
    expect(screen.queryByRole('listbox')).not.toBeInTheDocument();

    fireEvent.click(screen.getByRole('combobox', { name: 'Reasoning effort quick pick' }));
    fireEvent.click(screen.getByRole('option', { name: 'medium' }));
    expect(quickPick.onReasoningEffortChange).toHaveBeenCalledWith('medium');

    const gear = screen.getByRole('button', { name: 'Model settings' });
    expect(gear).toBe(screen.getByTestId('composer-context-button'));
    expect(gear.querySelector('svg')).toHaveClass('lucide-settings');
    fireEvent.click(gear);
    expect(props.onContextOpen).toHaveBeenCalledTimes(1);
  });

  it('shows a loading placeholder while profile models are being fetched', () => {
    renderComposer({
      quickPick: {
        modelValue: 'work\u0000gpt-5',
        modelOptions: [],
        modelOptionsLoading: true,
        reasoningEffort: 'medium',
        reasoningEffortOptions: ['medium'],
        onModelChange: vi.fn(),
        onReasoningEffortChange: vi.fn(),
      },
    });

    fireEvent.click(screen.getByRole('combobox', { name: 'Model quick pick' }));
    expect(screen.getByRole('option', { name: 'Loading models…' })).toBeDisabled();
  });

  it('offers an explicit reload action alongside a stream error', () => {
    const onReload = vi.fn();
    const composer = renderComposer({ streamError: 'Failed to fetch', onReload });

    expect(screen.getByRole('alert')).toHaveTextContent('Failed to fetch');
    const reloadButton = screen.getByRole('button', { name: 'Reload' });
    expect(reloadButton).toHaveClass('panel-action-button', 'panel-action-button-reload');
    expect(reloadButton.querySelector('svg')).toHaveClass('lucide-rotate-cw');
    expect(onReload).not.toHaveBeenCalled();

    fireEvent.click(reloadButton);

    expect(onReload).toHaveBeenCalledTimes(1);
    expect(screen.getByTestId('composer-textarea')).toHaveValue('hello');

    composer.rerenderComposer({ streamError: null });
    expect(screen.queryByRole('button', { name: 'Reload' })).not.toBeInTheDocument();
  });

  it('does not offer reload for a notice without a recovery action', () => {
    renderComposer({ streamError: 'Select a workspace runner to start a chat.' });

    expect(screen.getByText('Select a workspace runner to start a chat.')).toBeInTheDocument();
    expect(screen.queryByRole('button', { name: 'Reload' })).not.toBeInTheDocument();
  });

  it('keeps editor sizing stable without measuring layout on mount, edits, or resize', () => {
    const measurements = [
      vi.spyOn(window, 'getComputedStyle'),
      vi.spyOn(Element.prototype, 'getBoundingClientRect'),
      vi.spyOn(Element.prototype, 'clientHeight', 'get'),
      vi.spyOn(Element.prototype, 'scrollHeight', 'get'),
    ];
    try {
      const composer = renderComposer({ draft: '' });
      const textarea = screen.getByTestId('composer-textarea');

      for (const overrides of [
        { draft: 'hello' },
        { draft: 'A long wrapped draft. '.repeat(100) },
        { draft: 'a\nb\nc\nd\ne' },
        { draft: '' },
        { contextText: 'claude-sonnet-4-6 · high' },
        { showStop: true },
        { placeholder: 'Steer the active conversation…' },
      ]) {
        composer.rerenderComposer(overrides);
        fireEvent(window, new Event('resize'));

        expect(textarea).toHaveAttribute('rows', '3');
        expect(textarea).not.toHaveAttribute('style');
        expect(textarea.parentElement).toHaveAttribute('class', 'composer-control-grid');
      }

      for (const measurement of measurements) {
        expect(measurement).not.toHaveBeenCalled();
      }
    } finally {
      for (const measurement of measurements) {
        measurement.mockRestore();
      }
    }
  });

  it('renders and emits the compact stop action', () => {
    const props = renderComposer({ canStop: true, showStop: true });
    const stopButton = screen.getByLabelText('Stop');

    expect(stopButton).toHaveClass('composer-action-icon-button-stop');
    expect(stopButton.querySelector('svg')).toHaveClass('composer-action-stop-icon');

    fireEvent.click(stopButton);
    expect(props.onStop).toHaveBeenCalledTimes(1);
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
