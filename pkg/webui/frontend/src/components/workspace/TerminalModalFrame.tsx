import React from 'react';
import { cn } from '../../utils';

export type TerminalStatusVariant = 'live' | 'idle' | 'error';

interface TerminalModalFrameProps {
  children?: React.ReactNode;
  cwdLabel?: string;
  currentStatus?: string;
  statusVariant?: TerminalStatusVariant;
  terminalHostRef?: React.Ref<HTMLDivElement>;
  onClose?: () => void;
}

const TerminalModalFrame: React.FC<TerminalModalFrameProps> = ({
  children,
  currentStatus,
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
