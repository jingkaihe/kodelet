import { ArrowUp, ChevronDown, Paperclip, RotateCw, Settings, Square, X } from 'lucide-react';
import React from 'react';
import type { PendingImageAttachment, SlashCommandOption } from '../../types';
import { cn } from '../../utils';
import ListboxSelect, { type ListboxOption } from '../ListboxSelect';

export interface ComposerQuickPick {
  /** Selected `profile/model` option value; empty when unknown. */
  modelValue: string;
  /** Options labelled `profile/model`, ordered like the TUI picker. */
  modelOptions: ListboxOption[];
  modelOptionsLoading: boolean;
  reasoningEffort: string;
  reasoningEffortOptions: string[];
  onModelChange: (value: string) => void;
  onModelMenuOpen?: () => void;
  onReasoningEffortChange: (value: string) => void;
}

const composerSelectClassNames = {
  shell: 'composer-select-shell',
  trigger: 'composer-inline-context composer-select-trigger',
  chevron: 'composer-select-chevron',
  chevronIcon: 'h-3 w-3',
  menu: 'new-chat-select-menu composer-select-menu',
  option: 'new-chat-select-option composer-select-option',
};

interface ChatComposerProps {
  addImageDisabled: boolean;
  attachments: PendingImageAttachment[];
  canStop: boolean;
  contextDisabled: boolean;
  contextIsStatic: boolean;
  contextText: string;
  dragActive: boolean;
  draft: string;
  placeholder: string;
  quickPick?: ComposerQuickPick;
  showStop: boolean;
  slashCommandIndex: number;
  slashCommandSuggestions: SlashCommandOption[];
  slashCommandSuggestionsOpen: boolean;
  slashUsageHint: string;
  stopActionLabel: string;
  streamError: string | null;
  submitActionLabel: string;
  submitDisabled: boolean;
  textareaDisabled: boolean;
  onAttachImages: (files: File[]) => void | Promise<void>;
  onContextOpen: () => void;
  onDragLeave: (event: React.DragEvent<HTMLDivElement>) => void;
  onDragOver: (event: React.DragEvent<HTMLDivElement>) => void;
  onDrop: (event: React.DragEvent<HTMLDivElement>) => void;
  onDraftChange: (value: string) => void;
  onDraftKeyDown: (event: React.KeyboardEvent<HTMLTextAreaElement>) => void;
  onPaste: (event: React.ClipboardEvent<HTMLTextAreaElement>) => void;
  onReload?: () => void;
  onRemoveAttachment: (attachmentId: string) => void;
  onSelectSlashCommand: (commandName: string) => void;
  onStop: () => void;
  onSubmit: () => void | Promise<void>;
}

const ChatComposer: React.FC<ChatComposerProps> = ({
  addImageDisabled,
  attachments,
  canStop,
  contextDisabled,
  contextIsStatic,
  contextText,
  dragActive,
  draft,
  placeholder,
  quickPick,
  showStop,
  slashCommandIndex,
  slashCommandSuggestions,
  slashCommandSuggestionsOpen,
  slashUsageHint,
  stopActionLabel,
  streamError,
  submitActionLabel,
  submitDisabled,
  textareaDisabled,
  onAttachImages,
  onContextOpen,
  onDragLeave,
  onDragOver,
  onDrop,
  onDraftChange,
  onDraftKeyDown,
  onPaste,
  onReload,
  onRemoveAttachment,
  onSelectSlashCommand,
  onStop,
  onSubmit,
}) => {
  const fileInputRef = React.useRef<HTMLInputElement | null>(null);

  const handleFileInputChange = async (event: React.ChangeEvent<HTMLInputElement>) => {
    const files = Array.from(event.target.files || []);
    await onAttachImages(files);
    event.target.value = '';
  };

  return (
    <div className="composer-dock sticky bottom-0 z-10 shrink-0 py-2.5 pb-[calc(0.55rem+env(safe-area-inset-bottom))] md:py-3 lg:pb-[calc(1.25rem+env(safe-area-inset-bottom))]">
      <div className="mx-auto w-full max-w-5xl px-3 sm:px-4 md:px-8">
        {streamError ? (
          <div
            className="composer-error flex items-center justify-between gap-3"
            role={onReload ? 'alert' : undefined}
          >
            <span className="min-w-0">{streamError}</span>
            {onReload ? (
              <button
                className="panel-action-button panel-action-button-reload shrink-0"
                onClick={onReload}
                type="button"
              >
                <RotateCw aria-hidden="true" className="h-3.5 w-3.5" strokeWidth={1.9} />
                Reload
              </button>
            ) : null}
          </div>
        ) : null}

        {/* biome-ignore lint/a11y/noStaticElementInteractions: Drag and drop supplements the keyboard-accessible Add image button and file input. */}
        <div
          className={cn('composer-surface w-full p-2', dragActive && 'is-drag-active')}
          onDragLeave={onDragLeave}
          onDragOver={onDragOver}
          onDrop={onDrop}
        >
          <input
            accept="image/png,image/jpeg,image/gif,image/webp"
            className="hidden"
            data-testid="composer-image-input"
            multiple
            onChange={handleFileInputChange}
            ref={fileInputRef}
            type="file"
          />

          {attachments.length > 0 ? (
            <div className="mb-2.5 flex flex-wrap gap-2.5 px-2.5 pt-1.5">
              {attachments.map((attachment) => (
                <div
                  key={attachment.id}
                  className="relative overflow-hidden rounded-sm border border-kodelet-dark/10 bg-kodelet-light/80 p-2"
                >
                  <img
                    alt={attachment.name}
                    className="h-20 w-20 object-cover"
                    src={attachment.previewUrl}
                  />
                  <button
                    aria-label={`Remove ${attachment.name}`}
                    className="composer-attachment-remove"
                    onClick={() => onRemoveAttachment(attachment.id)}
                    type="button"
                  >
                    <X aria-hidden="true" className="h-3.5 w-3.5" strokeWidth={2} />
                  </button>
                </div>
              ))}
            </div>
          ) : null}

          {slashCommandSuggestionsOpen ? (
            <div className="composer-slash-suggestions" data-testid="slash-command-suggestions">
              {slashCommandSuggestions.map((command, index) => (
                <button
                  key={command.name}
                  className={cn(
                    'composer-slash-suggestion',
                    slashCommandIndex >= 0 && index === slashCommandIndex && 'is-active'
                  )}
                  onClick={() => onSelectSlashCommand(command.name)}
                  onMouseDown={(event) => event.preventDefault()}
                  type="button"
                >
                  <span className="composer-slash-suggestion-command">/{command.name}</span>
                  <span className="composer-slash-suggestion-description">
                    {command.description}
                  </span>
                  {command.hint ? (
                    <span className="composer-slash-suggestion-hint">{command.hint}</span>
                  ) : null}
                </button>
              ))}
            </div>
          ) : null}

          {slashUsageHint ? (
            <div className="composer-slash-usage-hint" data-testid="composer-slash-usage-hint">
              <span className="composer-slash-usage-label">hint</span>
              <code>{slashUsageHint}</code>
            </div>
          ) : null}

          <div className="composer-control-grid">
            {/* A fixed row count avoids forced layout measurements while typing. */}
            <textarea
              className="composer-editor"
              data-testid="composer-textarea"
              disabled={textareaDisabled}
              onChange={(event) => onDraftChange(event.target.value)}
              onKeyDown={onDraftKeyDown}
              onPaste={onPaste}
              placeholder={placeholder}
              rows={3}
              value={draft}
            />

            <div className="composer-leading-actions">
              <button
                aria-label="Add image"
                className="composer-icon-button"
                disabled={addImageDisabled}
                onClick={() => fileInputRef.current?.click()}
                title="Add image"
                type="button"
              >
                <Paperclip
                  aria-hidden="true"
                  className="composer-attachment-icon"
                  strokeWidth={1.9}
                />
              </button>
            </div>

            <div className="composer-context-cluster">
              {contextIsStatic ? (
                <div
                  className="composer-inline-context is-static"
                  data-testid="composer-inline-context"
                >
                  <span className="composer-inline-context-value" title={contextText}>
                    {contextText}
                  </span>
                </div>
              ) : quickPick ? (
                <div
                  className="composer-quick-pick"
                  data-testid="composer-quick-pick"
                  title={contextText}
                >
                  <ListboxSelect
                    busy={quickPick.modelOptionsLoading}
                    classNames={composerSelectClassNames}
                    disabled={contextDisabled}
                    label="Model quick pick"
                    onChange={quickPick.onModelChange}
                    onOpen={quickPick.onModelMenuOpen}
                    options={
                      quickPick.modelOptions.length > 0
                        ? quickPick.modelOptions
                        : [
                            {
                              value: '',
                              label: quickPick.modelOptionsLoading
                                ? 'Loading models…'
                                : 'No models available',
                              disabled: true,
                            },
                          ]
                    }
                    renderValue={(selected) => (
                      <span
                        className="composer-inline-context-value"
                        title={selected?.label ?? quickPick.modelValue}
                      >
                        {selected?.label ?? quickPick.modelValue}
                      </span>
                    )}
                    testId="composer-model-select"
                    value={quickPick.modelValue}
                  />
                  <span aria-hidden="true" className="composer-quick-pick-separator">
                    {' · '}
                  </span>
                  <ListboxSelect
                    classNames={composerSelectClassNames}
                    disabled={contextDisabled}
                    label="Reasoning effort quick pick"
                    onChange={quickPick.onReasoningEffortChange}
                    options={quickPick.reasoningEffortOptions.map((effort) => ({
                      value: effort,
                      label: effort,
                    }))}
                    renderValue={(selected) => (
                      <span className="composer-inline-context-value">
                        {selected?.label ?? quickPick.reasoningEffort}
                      </span>
                    )}
                    testId="composer-reasoning-effort-select"
                    value={quickPick.reasoningEffort}
                  />
                  <button
                    aria-haspopup="dialog"
                    aria-label="Model settings"
                    className="composer-inline-context composer-quick-pick-settings"
                    data-testid="composer-context-button"
                    disabled={contextDisabled}
                    onClick={onContextOpen}
                    title="Model settings"
                    type="button"
                  >
                    <Settings aria-hidden="true" className="h-3.5 w-3.5" strokeWidth={1.7} />
                  </button>
                </div>
              ) : (
                <button
                  aria-haspopup="dialog"
                  aria-label={`Model settings: ${contextText}`}
                  className="composer-inline-context"
                  data-testid="composer-context-button"
                  disabled={contextDisabled}
                  onClick={onContextOpen}
                  type="button"
                >
                  <span className="composer-inline-context-value" title={contextText}>
                    {contextText}
                  </span>
                  <ChevronDown
                    aria-hidden="true"
                    className="ml-1 h-3 w-3 shrink-0"
                    strokeWidth={1.6}
                  />
                </button>
              )}
            </div>

            <div className="composer-status-actions">
              {showStop ? (
                <button
                  aria-label={stopActionLabel}
                  className="composer-action-icon-button composer-action-icon-button-stop"
                  disabled={!canStop}
                  onClick={onStop}
                  title={stopActionLabel}
                  type="button"
                >
                  <Square
                    aria-hidden="true"
                    className="composer-action-stop-icon"
                    fill="currentColor"
                    strokeWidth={0}
                  />
                </button>
              ) : null}

              <button
                className={cn(
                  'composer-action-icon-button composer-action-icon-button-submit',
                  submitDisabled
                    ? 'composer-action-icon-button-disabled'
                    : 'composer-action-icon-button-ready'
                )}
                aria-label={submitActionLabel}
                disabled={submitDisabled}
                onClick={() => void onSubmit()}
                title={`${submitActionLabel} (Shift+Enter)`}
                type="button"
              >
                <ArrowUp
                  aria-hidden="true"
                  className="composer-action-submit-icon"
                  strokeWidth={3}
                />
              </button>
            </div>
          </div>
        </div>
      </div>
    </div>
  );
};

export default ChatComposer;
