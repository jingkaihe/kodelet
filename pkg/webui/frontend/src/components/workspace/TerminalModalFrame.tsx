import React from 'react';
import { ExternalLink } from 'lucide-react';
import { cn } from '../../utils';

export type TerminalStatusVariant = 'live' | 'idle' | 'error';

interface TerminalModalFrameProps {
  children?: React.ReactNode;
  cwdLabel?: string;
  currentStatus?: string;
  popOutActive?: boolean;
  statusVariant?: TerminalStatusVariant;
  terminalHostRef?: React.Ref<HTMLDivElement>;
  onClose?: () => void;
  onPopOut?: () => void;
}

const TerminalModalFrame: React.FC<TerminalModalFrameProps> = ({
  children,
  currentStatus,
  onPopOut,
  popOutActive = false,
  statusVariant = 'live',
  terminalHostRef,
}) => (
    <section
      aria-label="Terminal"
      className="workspace-side-panel workspace-terminal-panel surface-panel"
      data-testid="terminal-panel"
      role="complementary"
    >
      {currentStatus && !popOutActive ? (
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
          <div className="workspace-terminal-toolbar" aria-label="Terminal actions">
            <button
              aria-label="Open terminal in new window"
              className="workspace-terminal-icon-button"
              onClick={onPopOut}
              title="Open terminal in new window"
              type="button"
            >
              <ExternalLink aria-hidden="true" className="h-4 w-4" strokeWidth={1.9} />
            </button>
          </div>
        ) : null}
        <div
          aria-disabled={popOutActive || undefined}
          className={cn(
            'workspace-terminal-host',
            popOutActive && 'is-pop-out-active'
          )}
          data-testid="terminal-host"
          inert={popOutActive || undefined}
          ref={terminalHostRef}
        >
          {children}
        </div>
        {popOutActive ? (
          <div className="workspace-terminal-popout-shield" role="status">
            <ExternalLink aria-hidden="true" className="workspace-terminal-popout-icon" strokeWidth={1.8} />
            <strong>Terminal is open in the pop-out</strong>
            {onPopOut ? (
              <button className="workspace-terminal-popout-focus" onClick={onPopOut} type="button">
                Focus pop-out
              </button>
            ) : null}
          </div>
        ) : null}
      </div>
    </section>
);

export default TerminalModalFrame;
