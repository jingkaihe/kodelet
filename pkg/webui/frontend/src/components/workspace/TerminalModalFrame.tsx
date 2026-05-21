import React from 'react';
import { Grip } from 'lucide-react';
import { cn, truncateMiddle } from '../../utils';

export interface TerminalModalSize {
  width: number;
  height: number;
}

export type TerminalStatusVariant = 'live' | 'idle' | 'error';

interface TerminalModalFrameProps {
  children?: React.ReactNode;
  cwdLabel: string;
  currentStatus?: string;
  statusVariant?: TerminalStatusVariant;
  terminalHostRef?: React.Ref<HTMLDivElement>;
  terminalSize: TerminalModalSize;
  resizeHandleRef?: React.Ref<HTMLButtonElement>;
  onClose: () => void;
  onResizeStart: (event: React.PointerEvent<HTMLButtonElement>) => void;
}

const TerminalModalFrame: React.FC<TerminalModalFrameProps> = ({
  children,
  cwdLabel,
  currentStatus,
  statusVariant = 'live',
  terminalHostRef,
  terminalSize,
  resizeHandleRef,
  onClose,
  onResizeStart,
}) => (
  <div className="workspace-modal-backdrop" data-testid="terminal-modal-backdrop">
    <div
      aria-label="Terminal"
      className="workspace-modal workspace-terminal-modal surface-panel"
      data-testid="terminal-modal"
      role="dialog"
      style={{ width: terminalSize.width, height: terminalSize.height }}
    >
      <div className="workspace-modal-header workspace-terminal-header">
        <div className="workspace-modal-heading-group">
          <h2 className="workspace-modal-title">Terminal</h2>
          <p className="workspace-modal-copy" title={cwdLabel}>
            {truncateMiddle(cwdLabel, 92)}
          </p>
        </div>

        <div className="workspace-modal-actions">
          <button className="composer-capsule" onClick={onClose} type="button">
            Close
          </button>
        </div>
      </div>

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
        <div className="workspace-terminal-footer">
          <button
            aria-label="Resize terminal"
            className="workspace-terminal-resize-handle"
            data-testid="terminal-resize-handle"
            onPointerDown={onResizeStart}
            ref={resizeHandleRef}
            type="button"
          >
            <Grip aria-hidden="true" className="h-4 w-4" strokeWidth={1.7} />
          </button>
        </div>
      </div>
    </div>
  </div>
);

export default TerminalModalFrame;
