import { ArrowRight, Check, ChevronDown, FolderOpen, X } from 'lucide-react';
import React from 'react';
import type { ChatProfileOption, CWDHint, Runner } from '../../types';
import { cn, formatRunnerStatus } from '../../utils';

interface NewChatSelectProps {
  label: string;
  testId: string;
  value: string;
  options: { value: string; label: string; disabled?: boolean }[];
  placeholder?: string;
  disabled?: boolean;
  busy?: boolean;
  wide?: boolean;
  onChange: (value: string) => void;
}

const NewChatSelect = ({
  label,
  testId,
  value,
  options,
  placeholder,
  disabled,
  busy,
  wide,
  onChange,
}: NewChatSelectProps) => {
  const id = React.useId();
  const rootRef = React.useRef<HTMLDivElement>(null);
  const buttonRef = React.useRef<HTMLButtonElement>(null);
  const searchRef = React.useRef({ text: '', time: 0 });
  const [expanded, setExpanded] = React.useState(false);
  const [activeValue, setActiveValue] = React.useState(value);
  const [placement, setPlacement] = React.useState({ above: false, maxHeight: 240 });
  const open = expanded && !disabled;
  const enabledOptions = options.filter((option) => !option.disabled);
  const selectedOption = options.find((option) => option.value === value);
  const activeIndex = options.findIndex(
    (option) => option.value === activeValue && !option.disabled
  );

  const openMenu = () => {
    // Button clicks do not focus the trigger in every browser.
    buttonRef.current?.focus();
    searchRef.current = { text: '', time: 0 };
    setActiveValue(
      enabledOptions.find((option) => option.value === value)?.value ??
        enabledOptions[0]?.value ??
        ''
    );
    setExpanded(true);
  };

  const chooseOption = (nextValue: string) => {
    if (enabledOptions.some((option) => option.value === nextValue) && nextValue !== value) {
      onChange(nextValue);
    }
    setExpanded(false);
  };

  React.useEffect(() => {
    if (disabled) setExpanded(false);
  }, [disabled]);

  React.useLayoutEffect(() => {
    if (!open) return;
    // Keep the menu inside the dialog's scrolling panel, including on small screens.
    const positionMenu = () => {
      const button = buttonRef.current?.getBoundingClientRect();
      const panel = rootRef.current?.closest('.new-chat-context-panel')?.getBoundingClientRect();
      if (!button || !panel) return;
      const below = Math.min(panel.bottom, window.innerHeight) - button.bottom - 8;
      const above = button.top - Math.max(panel.top, 0) - 8;
      const placeAbove = below < 160 && above > below;
      setPlacement({
        above: placeAbove,
        maxHeight: Math.max(0, Math.min(240, placeAbove ? above : below)),
      });
    };
    const onPointerDown = (event: PointerEvent) => {
      if (!rootRef.current?.contains(event.target as Node)) setExpanded(false);
    };
    positionMenu();
    window.addEventListener('resize', positionMenu);
    window.addEventListener('scroll', positionMenu, true);
    document.addEventListener('pointerdown', onPointerDown);
    return () => {
      window.removeEventListener('resize', positionMenu);
      window.removeEventListener('scroll', positionMenu, true);
      document.removeEventListener('pointerdown', onPointerDown);
    };
  }, [open]);

  React.useEffect(() => {
    if (open) {
      document
        .getElementById(`${id}-option-${activeIndex}`)
        ?.scrollIntoView?.({ block: 'nearest' });
    }
  }, [open, id, activeIndex]);

  const onKeyDown = (event: React.KeyboardEvent<HTMLButtonElement>) => {
    if (event.key === 'Escape' && open) {
      event.preventDefault();
      event.stopPropagation();
      setExpanded(false);
      return;
    }
    if (event.key === 'Tab') {
      if (open) chooseOption(activeValue);
      return;
    }
    if (event.key === 'Enter' || event.key === ' ') {
      event.preventDefault();
      if (open) chooseOption(activeValue);
      else openMenu();
      return;
    }
    const index = enabledOptions.findIndex((option) => option.value === activeValue);
    if (['ArrowDown', 'ArrowUp', 'Home', 'End', 'PageDown', 'PageUp'].includes(event.key)) {
      event.preventDefault();
      if (open && event.altKey && event.key === 'ArrowUp') {
        chooseOption(activeValue);
        return;
      }
      if (!open) openMenu();
      let nextIndex = index;
      if (event.key === 'Home') nextIndex = 0;
      else if (event.key === 'End') nextIndex = enabledOptions.length - 1;
      else if (open) {
        const step = event.key.startsWith('Page') ? 10 : 1;
        nextIndex += event.key.endsWith('Down') ? step : -step;
      } else return;
      const next = enabledOptions[Math.max(0, Math.min(enabledOptions.length - 1, nextIndex))];
      if (next) setActiveValue(next.value);
      return;
    }
    if (event.key.length !== 1 || event.ctrlKey || event.metaKey || event.altKey) return;
    event.preventDefault();
    if (!open) openMenu();
    const now = Date.now();
    const text =
      (now - searchRef.current.time < 500 ? searchRef.current.text : '') + event.key.toLowerCase();
    searchRef.current = { text, time: now };
    const repeated = [...text].every((character) => character === text[0]);
    const prefix = repeated ? text[0] : text;
    const start = open ? Math.max(0, index + (repeated ? 1 : 0)) : 0;
    const ordered = [...enabledOptions.slice(start), ...enabledOptions.slice(0, start)];
    const match = ordered.find((option) => option.label.toLowerCase().startsWith(prefix));
    if (match) setActiveValue(match.value);
  };

  return (
    <div className={cn('new-chat-field new-chat-choice-card', wide && 'new-chat-field-wide')}>
      <label className="new-chat-field-label" htmlFor={id}>
        {label}
      </label>
      <div className={cn('new-chat-select-shell', open && 'is-open')} ref={rootRef}>
        <button
          aria-activedescendant={
            open && activeIndex >= 0 ? `${id}-option-${activeIndex}` : undefined
          }
          aria-busy={busy}
          aria-controls={open ? `${id}-listbox` : undefined}
          aria-expanded={open}
          aria-haspopup="listbox"
          aria-label={label}
          className="new-chat-field-control new-chat-field-control-select new-chat-select-trigger"
          data-testid={testId}
          disabled={disabled}
          id={id}
          onBlur={(event) => {
            if (!rootRef.current?.contains(event.relatedTarget as Node | null)) setExpanded(false);
          }}
          onClick={() => {
            if (open) setExpanded(false);
            else openMenu();
          }}
          onKeyDown={onKeyDown}
          ref={buttonRef}
          role="combobox"
          type="button"
        >
          {selectedOption?.label ?? placeholder ?? options[0]?.label}
          <span className="new-chat-select-chevron" aria-hidden="true">
            <ChevronDown className="h-4 w-4" strokeWidth={1.8} />
          </span>
        </button>
        {open && (
          <div
            aria-label={label}
            className={cn('new-chat-select-menu', placement.above && 'is-above')}
            id={`${id}-listbox`}
            role="listbox"
            style={{ maxHeight: placement.maxHeight }}
          >
            {options.map((option, index) => (
              <button
                aria-selected={option.value === value}
                className={cn(
                  'new-chat-select-option',
                  option.value === activeValue && 'is-active'
                )}
                data-value={option.value}
                disabled={option.disabled}
                id={`${id}-option-${index}`}
                key={option.value}
                onClick={() => {
                  chooseOption(option.value);
                  buttonRef.current?.focus();
                }}
                onMouseDown={(event) => event.preventDefault()}
                role="option"
                tabIndex={-1}
                type="button"
              >
                <span>{option.label}</span>
                {option.value === value && (
                  <Check aria-hidden="true" className="h-4 w-4 shrink-0" />
                )}
              </button>
            ))}
          </div>
        )}
      </div>
    </div>
  );
};

interface NewChatContextDialogProps {
  availableProfiles: ChatProfileOption[];
  cwdInputRef?: React.Ref<HTMLInputElement>;
  cwdQuery: string;
  cwdSuggestionIndex: number;
  cwdSuggestions: CWDHint[];
  cwdSuggestionsOpen: boolean;
  profileDraft: string;
  modelDraft: string;
  modelOptions: string[];
  reasoningEffortDraft: string;
  reasoningEffortLoading: boolean;
  reasoningEffortOptions: string[];
  runners: Runner[];
  runnerIdDraft: string;
  environmentProfileDraft: string;
  onCancel: () => void;
  onCommit: () => void;
  onCwdInputBlur: () => void;
  onCwdInputChange: (value: string) => void;
  onCwdInputFocus: () => void;
  onCwdInputKeyDown: (event: React.KeyboardEvent<HTMLInputElement>) => void;
  onProfileDraftChange: (profileName: string) => void;
  onModelDraftChange: (model: string) => void;
  onReasoningEffortDraftChange: (reasoningEffort: string) => void;
  onRunnerDraftChange: (runnerId: string) => void;
  onEnvironmentProfileDraftChange: (profileName: string) => void;
  onSelectCwdSuggestion: (path: string) => void;
}

const NewChatContextDialog = React.forwardRef<HTMLDivElement, NewChatContextDialogProps>(
  (
    {
      availableProfiles,
      cwdInputRef,
      cwdQuery,
      cwdSuggestionIndex,
      cwdSuggestions,
      cwdSuggestionsOpen,
      profileDraft,
      modelDraft,
      modelOptions,
      reasoningEffortDraft,
      reasoningEffortLoading,
      reasoningEffortOptions,
      runners,
      runnerIdDraft,
      environmentProfileDraft,
      onCancel,
      onCommit,
      onCwdInputBlur,
      onCwdInputChange,
      onCwdInputFocus,
      onCwdInputKeyDown,
      onProfileDraftChange,
      onModelDraftChange,
      onReasoningEffortDraftChange,
      onRunnerDraftChange,
      onEnvironmentProfileDraftChange,
      onSelectCwdSuggestion,
    },
    ref
  ) => {
    const selectedRunner = runners.find((runner) => runner.id === runnerIdDraft);
    const selectedRunnerAvailable = Boolean(
      runnerIdDraft &&
        selectedRunner?.connected &&
        (selectedRunner.status === 'idle' ||
          (selectedRunner.status === 'busy' && selectedRunner.concurrentRuns))
    );
    const directorySuggestions =
      cwdSuggestionsOpen && cwdSuggestions.length > 0 ? (
        <div
          aria-label="Working directory suggestions"
          className="composer-cwd-suggestions composer-cwd-suggestions-inline"
          data-testid="cwd-suggestions"
          id="new-chat-cwd-suggestions"
          role="listbox"
        >
          {cwdSuggestions.map((suggestion, index) => (
            <button
              aria-selected={index === cwdSuggestionIndex}
              className={cn('composer-cwd-suggestion', index === cwdSuggestionIndex && 'is-active')}
              data-testid={`cwd-suggestion-${index}`}
              id={`new-chat-cwd-suggestion-${index}`}
              key={suggestion.path}
              onClick={() => onSelectCwdSuggestion(suggestion.path)}
              onMouseDown={(event) => event.preventDefault()}
              role="option"
              type="button"
            >
              <span className="composer-cwd-suggestion-path">{suggestion.path}</span>
            </button>
          ))}
        </div>
      ) : null;
    return (
      <div className="new-chat-dialog-backdrop new-chat-context-backdrop">
        <div
          aria-labelledby="new-chat-dialog-title"
          aria-modal="true"
          className="new-chat-dialog new-chat-context-dialog surface-panel"
          data-testid="new-chat-dialog"
          ref={ref}
          role="dialog"
        >
          <header className="new-chat-context-header">
            <h2 className="new-chat-context-title" id="new-chat-dialog-title">
              New chat
            </h2>
            <button
              aria-label="Close new chat dialog"
              className="new-chat-context-close"
              onClick={onCancel}
              type="button"
            >
              <X className="h-4 w-4" strokeWidth={1.8} />
            </button>
          </header>

          <div className="new-chat-context-panel" data-testid="new-chat-context-panel">
            <div className="new-chat-dialog-grid">
              <NewChatSelect
                label="Profile"
                testId="new-chat-profile-select"
                placeholder="Select a profile"
                disabled={availableProfiles.length === 0}
                onChange={onProfileDraftChange}
                value={profileDraft}
                options={availableProfiles.map((profile) => ({
                  value: profile.name,
                  label: profile.name,
                }))}
              />
              <NewChatSelect
                label="Reasoning effort"
                testId="new-chat-reasoning-effort-select"
                busy={reasoningEffortLoading}
                disabled={reasoningEffortLoading || reasoningEffortOptions.length <= 1}
                onChange={onReasoningEffortDraftChange}
                value={reasoningEffortDraft}
                options={reasoningEffortOptions.map((effort) => ({ value: effort, label: effort }))}
              />
              <NewChatSelect
                label="Model"
                testId="new-chat-model-select"
                placeholder="Profile default"
                busy={reasoningEffortLoading}
                disabled={reasoningEffortLoading || modelOptions.length <= 1}
                onChange={onModelDraftChange}
                value={modelDraft}
                wide
                options={[...modelOptions]
                  .sort((a, b) => {
                    if (a === b) return 0;
                    if (a === modelDraft) return -1;
                    if (b === modelDraft) return 1;
                    return b.localeCompare(a, 'en', { numeric: true });
                  })
                  .map((model) => ({ value: model, label: model }))}
              />
              <NewChatSelect
                label="Environment"
                testId="new-chat-runner-select"
                onChange={onRunnerDraftChange}
                value={runnerIdDraft}
                wide
                options={[
                  { value: '', label: 'Select a workspace runner', disabled: true },
                  ...runners.map((runner) => ({
                    value: runner.id,
                    label: `${runner.displayName || runner.workspace.name || runner.id} — ${runner.host.hostname} — ${formatRunnerStatus(runner)}`,
                    disabled: !(
                      runner.connected &&
                      (runner.status === 'idle' ||
                        (runner.status === 'busy' && runner.concurrentRuns))
                    ),
                  })),
                ]}
              />

              {selectedRunner ? (
                <>
                  <label className="new-chat-field new-chat-field-wide new-chat-choice-card">
                    <span className="new-chat-field-label">Runner profile</span>
                    <input
                      aria-label="Runner profile"
                      autoCapitalize="off"
                      autoComplete="off"
                      autoCorrect="off"
                      className="new-chat-field-control new-chat-field-control-mono"
                      data-testid="new-chat-environment-profile-input"
                      onChange={(event) => onEnvironmentProfileDraftChange(event.target.value)}
                      placeholder="default"
                      spellCheck={false}
                      type="text"
                      value={environmentProfileDraft}
                    />
                    <span className="new-chat-recent-workspace-parent">
                      Optional runner-local environment profile; locked when the chat starts.
                    </span>
                  </label>
                  <div className="new-chat-field new-chat-field-wide new-chat-workspace-card">
                    <label className="new-chat-field-label" htmlFor="new-chat-cwd">
                      Working directory
                    </label>
                    <div className="new-chat-field-autocomplete">
                      <div className="new-chat-directory-shell">
                        <FolderOpen
                          aria-hidden="true"
                          className="new-chat-directory-icon"
                          strokeWidth={1.6}
                        />
                        <input
                          aria-activedescendant={
                            directorySuggestions && cwdSuggestions[cwdSuggestionIndex]
                              ? `new-chat-cwd-suggestion-${cwdSuggestionIndex}`
                              : undefined
                          }
                          aria-autocomplete="list"
                          aria-controls={
                            directorySuggestions ? 'new-chat-cwd-suggestions' : undefined
                          }
                          aria-expanded={cwdSuggestionsOpen && cwdSuggestions.length > 0}
                          aria-label="Working directory"
                          autoCapitalize="off"
                          autoComplete="off"
                          autoCorrect="off"
                          className="new-chat-field-control new-chat-field-control-mono new-chat-directory-control"
                          data-testid="cwd-input"
                          id="new-chat-cwd"
                          onBlur={onCwdInputBlur}
                          onChange={(event) => onCwdInputChange(event.target.value)}
                          onFocus={onCwdInputFocus}
                          onKeyDown={onCwdInputKeyDown}
                          placeholder={selectedRunner.workspace.path}
                          ref={cwdInputRef}
                          role="combobox"
                          spellCheck={false}
                          type="text"
                          value={cwdQuery}
                        />
                      </div>
                      {directorySuggestions}
                    </div>
                    <span className="new-chat-recent-workspace-parent">
                      Optional. Relative paths and ~ are resolved on the runner host.
                    </span>
                  </div>
                  <div className="new-chat-field new-chat-field-wide new-chat-workspace-card">
                    <span className="new-chat-field-label">Default runner workspace</span>
                    <div className="new-chat-directory-shell">
                      <FolderOpen
                        aria-hidden="true"
                        className="new-chat-directory-icon"
                        strokeWidth={1.6}
                      />
                      <div className="new-chat-field-control new-chat-field-control-mono new-chat-directory-control">
                        {selectedRunner.workspace.path}
                      </div>
                    </div>
                    <span className="new-chat-recent-workspace-parent">
                      {selectedRunner.host.hostname} · {selectedRunner.status}
                      {selectedRunner.manifestChanged ? ' · manifest changed' : ''}
                    </span>
                  </div>
                </>
              ) : (
                <output className="new-chat-field new-chat-field-wide new-chat-workspace-card">
                  <span className="new-chat-field-label">Workspace runner required</span>
                  <span className="new-chat-recent-workspace-parent">
                    The control-plane workspace is disabled. Select an available workspace runner to
                    start this chat.
                  </span>
                </output>
              )}
            </div>
          </div>

          <div className="new-chat-dialog-actions new-chat-context-actions">
            <div className="new-chat-context-action-buttons">
              <button className="new-chat-secondary-button" onClick={onCancel} type="button">
                Cancel
              </button>
              <button
                className="new-chat-primary-button"
                disabled={
                  reasoningEffortLoading || !selectedRunnerAvailable || !profileDraft.trim()
                }
                onClick={onCommit}
                type="button"
              >
                <span>Start</span>
                <ArrowRight aria-hidden="true" className="h-3.5 w-3.5" strokeWidth={1.9} />
              </button>
            </div>
          </div>
        </div>
      </div>
    );
  }
);

NewChatContextDialog.displayName = 'NewChatContextDialog';

export default NewChatContextDialog;
