import { X } from 'lucide-react';
import type React from 'react';
import { useDialogFocus } from '../../features/chat/useDialogFocus';

interface KeyboardShortcutsDialogProps {
  onClose: () => void;
}

const shortcutGroups = [
  {
    title: 'Navigation',
    shortcuts: [
      { label: 'Toggle sidebar', keys: 'Ctrl/⌘+B' },
      { label: 'Toggle terminal', keys: 'Ctrl+`' },
      { label: 'Search conversations', keys: 'Ctrl/⌘+K' },
      { label: 'New chat', keys: 'Ctrl/⌘+Shift+O' },
      { label: 'Focus message', keys: 'Ctrl/⌘+Shift+L' },
      { label: 'Keyboard shortcuts', keys: 'Ctrl/⌘+/' },
    ],
  },
  {
    title: 'Message',
    shortcuts: [
      { label: 'Send message', keys: 'Shift+Enter' },
      { label: 'New line', keys: 'Enter' },
    ],
  },
];

const KeyboardShortcutsDialog: React.FC<KeyboardShortcutsDialogProps> = ({ onClose }) => {
  const { dialogRef, closeButtonRef } = useDialogFocus(
    onClose,
    '.sidebar-account-trigger, [data-testid="sidebar-keyboard-shortcuts"], [data-testid="sidebar-attached-toggle-mobile"]'
  );

  return (
    <div className="new-chat-dialog-backdrop keyboard-shortcuts-backdrop">
      <div
        aria-labelledby="keyboard-shortcuts-title"
        aria-modal="true"
        className="keyboard-shortcuts-dialog surface-panel"
        ref={dialogRef}
        role="dialog"
        tabIndex={-1}
      >
        <header className="new-chat-context-header keyboard-shortcuts-header">
          <h2 className="new-chat-context-title" id="keyboard-shortcuts-title">
            Keyboard shortcuts
          </h2>
          <button
            aria-label="Close keyboard shortcuts"
            className="new-chat-context-close"
            onClick={onClose}
            ref={closeButtonRef}
            type="button"
          >
            <X aria-hidden="true" className="h-4 w-4" strokeWidth={1.8} />
          </button>
        </header>

        <div className="keyboard-shortcuts-content">
          {shortcutGroups.map(({ title, shortcuts }) => (
            <section aria-label={title} key={title}>
              <h3>{title}</h3>
              <dl className="keyboard-shortcuts-list">
                {shortcuts.map(({ label, keys }) => (
                  <div key={label}>
                    <dt>{label}</dt>
                    <dd>
                      <kbd>{keys}</kbd>
                    </dd>
                  </div>
                ))}
              </dl>
            </section>
          ))}
        </div>
      </div>
    </div>
  );
};

export default KeyboardShortcutsDialog;
