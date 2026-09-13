import { ExternalLink } from 'lucide-react';
import type React from 'react';
import { useId, useState } from 'react';
import { cn } from '../../utils';

export type TerminalStatusVariant = 'live' | 'connecting' | 'idle' | 'error';
export type TerminalArrowDirection = 'A' | 'B' | 'C' | 'D';

type TerminalKey = {
  label: string;
  name?: string;
  title: string;
} & ({ data: string } | { direction: TerminalArrowDirection });

const MAIN_TERMINAL_KEYS: TerminalKey[] = [
  { label: 'Ctrl+C', title: 'Ctrl+C — interrupt the running command', data: '\x03' },
  { label: 'Ctrl+D', title: 'Ctrl+D — end input or exit an empty shell prompt', data: '\x04' },
  { label: 'Esc', title: 'Esc — cancel or leave the current mode', data: '\x1b' },
  { label: 'Tab', title: 'Tab — complete a command or path', data: '\t' },
  { label: '←', name: 'Arrow left', title: 'Arrow left — move the cursor left', direction: 'D' },
  { label: '↓', name: 'Arrow down', title: 'Arrow down — next command in history', direction: 'B' },
  { label: '↑', name: 'Arrow up', title: 'Arrow up — previous command in history', direction: 'A' },
  { label: '→', name: 'Arrow right', title: 'Arrow right — move the cursor right', direction: 'C' },
];

const MORE_TERMINAL_KEYS: TerminalKey[] = [
  { label: 'Ctrl+Z', title: 'Ctrl+Z — suspend the running command', data: '\x1a' },
  { label: 'Ctrl+L', title: 'Ctrl+L — clear and redraw the screen', data: '\x0c' },
  { label: 'Ctrl+A', title: 'Ctrl+A — move to the start of the line', data: '\x01' },
  { label: 'Ctrl+E', title: 'Ctrl+E — move to the end of the line', data: '\x05' },
  { label: 'Ctrl+U', title: 'Ctrl+U — delete back to the start of the line', data: '\x15' },
  { label: 'Ctrl+K', title: 'Ctrl+K — delete to the end of the line', data: '\x0b' },
  { label: 'Ctrl+W', title: 'Ctrl+W — delete the previous word', data: '\x17' },
  { label: 'Ctrl+R', title: 'Ctrl+R — search command history', data: '\x12' },
];

interface TerminalModalFrameProps {
  children?: React.ReactNode;
  cwdLabel?: string;
  currentStatus?: string;
  popOutActive?: boolean;
  statusVariant?: TerminalStatusVariant;
  terminalHostRef?: React.Ref<HTMLDivElement>;
  onClose?: () => void;
  onPopOut?: () => void;
  onTerminalInput?: (data: string) => void;
  onTerminalArrow?: (direction: TerminalArrowDirection) => void;
}

const TerminalModalFrame: React.FC<TerminalModalFrameProps> = ({
  children,
  currentStatus,
  onPopOut,
  onTerminalInput,
  onTerminalArrow,
  popOutActive = false,
  statusVariant = 'live',
  terminalHostRef,
}) => {
  const moreKeysId = useId();
  const [moreKeysOpen, setMoreKeysOpen] = useState(false);
  const renderKey = (key: TerminalKey) => (
    <button
      aria-label={key.name ?? key.label}
      className="workspace-terminal-key-button"
      key={key.label}
      onClick={() => {
        if ('data' in key) {
          onTerminalInput?.(key.data);
        } else {
          onTerminalArrow?.(key.direction);
        }
      }}
      // Keep the terminal focused on touch; only a completed click sends input.
      onPointerDown={(event) => event.preventDefault()}
      title={key.title}
      type="button"
    >
      {key.label}
    </button>
  );

  return (
    <aside
      aria-label="Terminal"
      className="workspace-side-panel workspace-terminal-panel surface-panel"
      data-testid="terminal-panel"
    >
      {currentStatus && !popOutActive && statusVariant !== 'connecting' ? (
        <div className="workspace-terminal-status-bar">
          <span
            className={cn(
              'workspace-terminal-status-dot',
              statusVariant === 'error'
                ? 'is-error'
                : statusVariant === 'idle'
                  ? 'is-idle'
                  : 'is-live'
            )}
          />
          <span className="workspace-terminal-status-text">{currentStatus}</span>
        </div>
      ) : null}

      <div className="workspace-terminal-shell">
        {onPopOut && !popOutActive ? (
          <fieldset className="workspace-terminal-toolbar" aria-label="Terminal actions">
            <button
              aria-label="Open terminal in new window"
              className="workspace-terminal-icon-button"
              onClick={onPopOut}
              title="Open terminal in new window"
              type="button"
            >
              <ExternalLink aria-hidden="true" className="h-4 w-4" strokeWidth={1.9} />
            </button>
          </fieldset>
        ) : null}
        <div
          aria-busy={(statusVariant === 'connecting' && !popOutActive) || undefined}
          aria-disabled={popOutActive || undefined}
          className={cn(
            'workspace-terminal-host',
            popOutActive && 'is-pop-out-active',
            statusVariant === 'connecting' && !popOutActive && 'is-connecting'
          )}
          data-testid="terminal-host"
          inert={popOutActive || undefined}
          ref={terminalHostRef}
        >
          {children}
        </div>
        {onTerminalInput && onTerminalArrow && !popOutActive ? (
          <div className="workspace-terminal-controls">
            <div className="workspace-terminal-controls-row">
              <fieldset
                aria-label="Terminal keys"
                className="workspace-terminal-key-strip"
                disabled={statusVariant !== 'live'}
              >
                {MAIN_TERMINAL_KEYS.map(renderKey)}
              </fieldset>
              <button
                aria-controls={moreKeysId}
                aria-expanded={moreKeysOpen}
                className="workspace-terminal-key-button workspace-terminal-more-keys"
                onClick={() => setMoreKeysOpen((open) => !open)}
                onPointerDown={(event) => event.preventDefault()}
                title={
                  moreKeysOpen
                    ? 'Less keys — hide additional control keys'
                    : 'More keys — show additional control keys'
                }
                type="button"
              >
                {moreKeysOpen ? 'Less keys' : 'More keys'}
              </button>
            </div>
            <fieldset
              aria-label="More terminal keys"
              className="workspace-terminal-key-strip"
              disabled={statusVariant !== 'live'}
              hidden={!moreKeysOpen}
              id={moreKeysId}
            >
              {MORE_TERMINAL_KEYS.map(renderKey)}
            </fieldset>
          </div>
        ) : null}
        {statusVariant === 'connecting' && !popOutActive ? (
          <output className="workspace-terminal-connecting">Connecting</output>
        ) : null}
        {popOutActive ? (
          <output className="workspace-terminal-popout-shield">
            <ExternalLink
              aria-hidden="true"
              className="workspace-terminal-popout-icon"
              strokeWidth={1.8}
            />
            <strong>Terminal is open in the pop-out</strong>
            {onPopOut ? (
              <button className="workspace-terminal-popout-focus" onClick={onPopOut} type="button">
                Focus pop-out
              </button>
            ) : null}
          </output>
        ) : null}
      </div>
    </aside>
  );
};

export default TerminalModalFrame;
