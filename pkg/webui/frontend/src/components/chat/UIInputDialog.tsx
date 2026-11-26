import { ChevronDown, X } from 'lucide-react';
import type React from 'react';
import { useEffect, useId, useRef, useState } from 'react';
import type { UIConfirmRequestEvent, UIInputRequestEvent, UISelectRequestEvent } from '../../types';

type UIInputDialogMode = 'input' | 'confirm' | 'select';

const EMPTY_SELECT_OPTIONS: string[] = [];
const DIALOG_FOCUSABLE_SELECTOR = [
  'button:not([disabled])',
  'input:not([disabled])',
  'select:not([disabled])',
  'textarea:not([disabled])',
  '[href]',
  "[tabindex]:not([tabindex='-1'])",
].join(',');

interface UIInputDialogProps {
  mode?: UIInputDialogMode;
  request: UIInputRequestEvent | UIConfirmRequestEvent | UISelectRequestEvent;
  submitting?: boolean;
  onCancel: () => void;
  onSubmit: (value: string) => void;
}

const UIInputDialog: React.FC<UIInputDialogProps> = ({
  mode = 'input',
  request,
  submitting = false,
  onCancel,
  onSubmit,
}) => {
  const inputRequest = request as UIInputRequestEvent;
  const selectRequest = request as UISelectRequestEvent;
  const selectOptions = mode === 'select' ? selectRequest.options : EMPTY_SELECT_OPTIONS;
  const [value, setValue] = useState(mode === 'input' ? inputRequest.defaultValue || '' : '');
  const titleId = useId();
  const messageId = useId();
  const inputRef = useRef<HTMLInputElement | null>(null);
  const selectRef = useRef<HTMLSelectElement | null>(null);
  const submitRef = useRef<HTMLButtonElement | null>(null);
  const dialogRef = useRef<HTMLFormElement | null>(null);
  const onCancelRef = useRef(onCancel);
  const submittingRef = useRef(submitting);
  onCancelRef.current = onCancel;
  submittingRef.current = submitting;

  // biome-ignore lint/correctness/useExhaustiveDependencies(request.id): Reset the value and restart focus when a new input request arrives, even with the same defaults.
  useEffect(() => {
    const previousFocus =
      document.activeElement instanceof HTMLElement ? document.activeElement : null;
    setValue(
      mode === 'input'
        ? inputRequest.defaultValue || ''
        : mode === 'select'
          ? selectOptions[0] || ''
          : ''
    );
    const focusControl = window.setTimeout(() => {
      if (mode === 'select') {
        selectRef.current?.focus();
        return;
      }
      if (mode === 'input') {
        inputRef.current?.focus();
        return;
      }
      submitRef.current?.focus();
    }, 0);
    const handleKeyDown = (event: KeyboardEvent) => {
      const dialog = dialogRef.current;
      if (!dialog) {
        return;
      }
      if (event.key === 'Escape') {
        event.preventDefault();
        event.stopPropagation();
        if (submittingRef.current) {
          return;
        }
        onCancelRef.current();
        return;
      }
      if (event.key !== 'Tab') {
        return;
      }

      const focusableElements = Array.from(
        dialog.querySelectorAll<HTMLElement>(DIALOG_FOCUSABLE_SELECTOR)
      ).filter((element) => !element.hasAttribute('disabled'));
      if (focusableElements.length === 0) {
        event.preventDefault();
        dialog.focus();
        return;
      }

      const firstElement = focusableElements[0];
      const lastElement = focusableElements[focusableElements.length - 1];
      if (event.shiftKey && document.activeElement === firstElement) {
        event.preventDefault();
        lastElement.focus();
        return;
      }
      if (!event.shiftKey && document.activeElement === lastElement) {
        event.preventDefault();
        firstElement.focus();
      }
    };

    window.addEventListener('keydown', handleKeyDown, true);
    return () => {
      window.clearTimeout(focusControl);
      window.removeEventListener('keydown', handleKeyDown, true);
      window.setTimeout(() => {
        if (previousFocus?.isConnected) {
          previousFocus.focus();
        }
      }, 0);
    };
  }, [mode, request.id, inputRequest.defaultValue, selectOptions]);

  const fallbackTitle =
    mode === 'confirm'
      ? 'Extension requested confirmation'
      : mode === 'select'
        ? 'Extension requested selection'
        : 'Extension requested input';
  const trimmedTitle = request.title?.trim() || fallbackTitle;
  const message = 'message' in request ? request.message?.trim() : '';
  const helpText = mode === 'input' ? inputRequest.helpText?.trim() : '';
  const canSubmit =
    mode === 'select'
      ? selectOptions.length > 0
      : mode !== 'input' || !inputRequest.required || value.trim().length > 0;
  const submitLabel =
    mode === 'confirm'
      ? (request as UIConfirmRequestEvent).confirmButtonText || 'Confirm'
      : mode === 'select'
        ? selectRequest.submitButtonText || 'Select'
        : inputRequest.submitButtonText || 'Submit';

  return (
    <div className="new-chat-dialog-backdrop ui-input-dialog-backdrop">
      <form
        aria-describedby={message ? messageId : undefined}
        aria-labelledby={titleId}
        aria-modal="true"
        className="ui-input-dialog"
        data-testid="ui-input-dialog"
        onSubmit={(event) => {
          event.preventDefault();
          if (canSubmit && !submitting) {
            onSubmit(value);
          }
        }}
        ref={dialogRef}
        role="dialog"
        tabIndex={-1}
      >
        <header className="new-chat-context-header ui-input-dialog-header">
          <h2 className="new-chat-context-title ui-input-dialog-title" id={titleId}>
            {trimmedTitle}
          </h2>
          <button
            aria-label="Dismiss dialog"
            className="new-chat-context-close"
            disabled={submitting}
            onClick={onCancel}
            type="button"
          >
            <X aria-hidden="true" className="h-4 w-4" strokeWidth={1.8} />
          </button>
        </header>

        {message || helpText || mode !== 'confirm' ? (
          <div className="ui-input-dialog-body">
            {message ? (
              <p className="ui-input-dialog-message" id={messageId}>
                {message}
              </p>
            ) : null}

            {mode === 'input' ? (
              <label className="new-chat-field">
                <input
                  aria-label="Response"
                  className="new-chat-field-control"
                  data-testid="ui-input-response"
                  disabled={submitting}
                  onChange={(event) => setValue(event.target.value)}
                  placeholder={inputRequest.placeholder}
                  ref={inputRef}
                  required={inputRequest.required}
                  type={inputRequest.secret ? 'password' : 'text'}
                  value={value}
                />
              </label>
            ) : null}

            {mode === 'select' ? (
              <div className="new-chat-select-shell">
                <select
                  aria-label="Choice"
                  className="new-chat-field-control new-chat-field-control-select"
                  data-testid="ui-select-response"
                  disabled={submitting}
                  onChange={(event) => setValue(event.target.value)}
                  ref={selectRef}
                  value={value}
                >
                  {selectOptions.map((option, index) => (
                    <option key={`${index}-${option}`} value={option}>
                      {option}
                    </option>
                  ))}
                </select>
                <span aria-hidden="true" className="new-chat-select-chevron">
                  <ChevronDown className="h-4 w-4" strokeWidth={1.8} />
                </span>
              </div>
            ) : null}

            {helpText ? <p className="ui-input-dialog-help">{helpText}</p> : null}
          </div>
        ) : null}

        <div className="new-chat-dialog-actions new-chat-context-actions">
          <div className="new-chat-context-action-buttons">
            <button
              className="new-chat-secondary-button"
              disabled={submitting}
              onClick={onCancel}
              type="button"
            >
              {request.cancelButtonText || 'Cancel'}
            </button>
            <button
              className="new-chat-primary-button"
              disabled={submitting || !canSubmit}
              ref={submitRef}
              type="submit"
            >
              {submitting ? 'Sending…' : submitLabel}
            </button>
          </div>
        </div>
      </form>
    </div>
  );
};

export default UIInputDialog;
