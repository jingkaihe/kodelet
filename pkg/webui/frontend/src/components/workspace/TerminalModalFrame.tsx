import React from 'react';
import { ExternalLink } from 'lucide-react';
import { cn } from '../../utils';

export type TerminalStatusVariant = 'live' | 'idle' | 'error';

interface TerminalModalFrameProps {
  children?: React.ReactNode;
  cwdLabel?: string;
  currentStatus?: string;
  statusVariant?: TerminalStatusVariant;
  terminalHostRef?: React.Ref<HTMLDivElement>;
  onClose?: () => void;
  onPopOut?: () => void;
}

const TerminalModalFrame: React.FC<TerminalModalFrameProps> = ({
  children,
  currentStatus,
  onPopOut,
  statusVariant = 'live',
  terminalHostRef,
}) => (
  <section
    aria-label="Terminal"
    className="workspace-side-panel workspace-terminal-panel surface-panel"
    data-testid="terminal-panel"
    role="complementary"
  >
    {currentStatus ? (
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
      {onPopOut ? (
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
        className="workspace-terminal-host"
        data-testid="terminal-host"
        ref={terminalHostRef}
      >
        {children}
      </div>
    </div>
  </section>
);

export default TerminalModalFrame;
