import React, { useEffect, useMemo, useRef, useState } from 'react';
import { FitAddon } from '@xterm/addon-fit';
import { Terminal } from '@xterm/xterm';
import type {
  TerminalClientMessage,
  TerminalExitEvent,
  TerminalReadyEvent,
  TerminalServerEvent,
} from '../../types';
import apiService from '../../services/api';
import { cn, truncateMiddle } from '../../utils';

interface TerminalModalProps {
  cwdLabel: string;
  open: boolean;
  onClose: () => void;
}

const MIN_TERMINAL_WIDTH = 520;
const MIN_TERMINAL_HEIGHT = 320;
const DEFAULT_TERMINAL_WIDTH = 980;
const DEFAULT_TERMINAL_HEIGHT = 620;

const isTerminalServerEvent = (value: unknown): value is TerminalServerEvent => {
  if (!value || typeof value !== 'object' || Array.isArray(value)) {
    return false;
  }

  const type = (value as { type?: unknown }).type;
  return type === 'ready' || type === 'exit' || type === 'info';
};

const TerminalModal: React.FC<TerminalModalProps> = ({ cwdLabel, open, onClose }) => {
  const terminalRef = useRef<Terminal | null>(null);
  const fitAddonRef = useRef<FitAddon | null>(null);
  const socketRef = useRef<WebSocket | null>(null);
  const terminalHostRef = useRef<HTMLDivElement | null>(null);
  const modalRef = useRef<HTMLDivElement | null>(null);
  const resizeHandleRef = useRef<HTMLButtonElement | null>(null);
  const dragStateRef = useRef<{
    startX: number;
    startY: number;
    startWidth: number;
    startHeight: number;
  } | null>(null);
  const [statusText, setStatusText] = useState('Connecting…');
  const [connectionError, setConnectionError] = useState<string | null>(null);
  const [shellName, setShellName] = useState<string | null>(null);
  const [exitCode, setExitCode] = useState<number | null>(null);
  const [terminalSize, setTerminalSize] = useState({
    width: DEFAULT_TERMINAL_WIDTH,
    height: DEFAULT_TERMINAL_HEIGHT,
  });

  const currentStatus = useMemo(() => {
    if (connectionError) {
      return connectionError;
    }
    if (exitCode !== null) {
      return `Exited with code ${exitCode}`;
    }
    return statusText === 'Connected' ? '' : statusText;
  }, [connectionError, exitCode, statusText]);

  useEffect(() => {
    if (!open || !terminalHostRef.current) {
      return undefined;
    }

    const terminal = new Terminal({
      allowTransparency: true,
      convertEol: true,
      cursorBlink: true,
      cursorStyle: 'block',
      fontFamily: 'var(--font-mono)',
      fontSize: 13,
      lineHeight: 1.35,
      scrollback: 5000,
      theme: {
        background: '#171512',
        foreground: '#f4eee3',
        cursor: '#d97757',
        cursorAccent: '#171512',
        selectionBackground: 'rgba(217, 119, 87, 0.26)',
        black: '#171512',
        red: '#df7c5e',
        green: '#8ea267',
        yellow: '#cfb37a',
        blue: '#7eabd8',
        magenta: '#b795b9',
        cyan: '#87b7b1',
        white: '#efe6d7',
        brightBlack: '#635b4f',
        brightRed: '#f29b80',
        brightGreen: '#a6bf79',
        brightYellow: '#e7c98d',
        brightBlue: '#99c0e6',
        brightMagenta: '#cfadd0',
        brightCyan: '#a5d0ca',
        brightWhite: '#fffaf1',
      },
    });
    const fitAddon = new FitAddon();

    terminal.loadAddon(fitAddon);
    terminal.open(terminalHostRef.current);
    fitAddon.fit();
    terminal.focus();
    terminalRef.current = terminal;
    fitAddonRef.current = fitAddon;
    setStatusText('Connecting…');
    setConnectionError(null);
    setExitCode(null);
    setShellName(null);

    const sendMessage = (message: TerminalClientMessage) => {
      if (socketRef.current?.readyState !== WebSocket.OPEN) {
        return;
      }
      socketRef.current.send(JSON.stringify(message));
    };

    const sendResize = () => {
      if (!terminalRef.current) {
        return;
      }
      fitAddon.fit();
      sendMessage({
        type: 'resize',
        rows: terminalRef.current.rows,
        cols: terminalRef.current.cols,
      });
    };

    const socket = apiService.createTerminalWebSocket({
      cwd: cwdLabel,
      rows: terminal.rows,
      cols: terminal.cols,
    });
    socket.binaryType = 'arraybuffer';
    socketRef.current = socket;

    socket.addEventListener('open', () => {
      setStatusText('Connected');
      sendResize();
    });

    socket.addEventListener('message', (event) => {
      if (typeof event.data === 'string') {
        try {
          const payload = JSON.parse(event.data) as unknown;
          if (!isTerminalServerEvent(payload)) {
            return;
          }

          if (payload.type === 'ready') {
            const readyPayload = payload as TerminalReadyEvent;
            setShellName(readyPayload.name);
            setStatusText('Connected');
            window.setTimeout(() => sendResize(), 0);
            return;
          }

          if (payload.type === 'exit') {
            const exitPayload = payload as TerminalExitEvent;
            setExitCode(exitPayload.code);
            setStatusText(`Exited with code ${exitPayload.code}`);
            terminal.writeln(`\r\n[process exited with code ${exitPayload.code}]`);
            return;
          }

          if (payload.type === 'info') {
            terminal.writeln(`\r\n${payload.text}`);
          }
        } catch {
          terminal.writeln(`\r\n${event.data}`);
        }
        return;
      }

      if (event.data instanceof ArrayBuffer) {
        terminal.write(new Uint8Array(event.data));
      }
    });

    socket.addEventListener('close', () => {
      setStatusText((current) => (current.startsWith('Exited with code') ? current : 'Disconnected'));
    });

    socket.addEventListener('error', () => {
      setConnectionError('Terminal connection failed');
    });

    const disposableData = terminal.onData((data) => {
      sendMessage({ type: 'input', data });
    });

    const disposableResize = terminal.onResize(({ rows, cols }) => {
      sendMessage({ type: 'resize', rows, cols });
    });

    terminal.attachCustomKeyEventHandler((event) => {
      if (event.type !== 'keydown') {
        return true;
      }

      const key = event.key.toLowerCase();
      if ((event.ctrlKey || event.metaKey) && key === 'r') {
        return true;
      }
      if ((event.ctrlKey || event.metaKey) && key === 'c' && terminal.hasSelection()) {
        return true;
      }
      return true;
    });

    const handleWindowResize = () => sendResize();
    window.addEventListener('resize', handleWindowResize);

    return () => {
      window.removeEventListener('resize', handleWindowResize);
      disposableResize.dispose();
      disposableData.dispose();
      socket.close();
      socketRef.current = null;
      terminal.dispose();
      terminalRef.current = null;
      fitAddonRef.current = null;
    };
  }, [cwdLabel, open]);

  useEffect(() => {
    if (!open) {
      return undefined;
    }

    const handlePointerMove = (event: PointerEvent) => {
      const dragState = dragStateRef.current;
      if (!dragState) {
        return;
      }

      const nextWidth = Math.max(MIN_TERMINAL_WIDTH, dragState.startWidth + (event.clientX - dragState.startX));
      const nextHeight = Math.max(MIN_TERMINAL_HEIGHT, dragState.startHeight + (event.clientY - dragState.startY));
      setTerminalSize({ width: nextWidth, height: nextHeight });
    };

    const stopDragging = () => {
      dragStateRef.current = null;
      document.body.style.userSelect = '';
      document.body.style.cursor = '';
      window.setTimeout(() => fitAddonRef.current?.fit(), 0);
    };

    window.addEventListener('pointermove', handlePointerMove);
    window.addEventListener('pointerup', stopDragging);

    return () => {
      window.removeEventListener('pointermove', handlePointerMove);
      window.removeEventListener('pointerup', stopDragging);
    };
  }, [open]);

  useEffect(() => {
    if (!open) {
      return undefined;
    }

    const handleKeyDown = (event: KeyboardEvent) => {
      if (event.key === 'Escape') {
        event.preventDefault();
        onClose();
      }
    };

    window.addEventListener('keydown', handleKeyDown);
    return () => window.removeEventListener('keydown', handleKeyDown);
  }, [open, onClose]);

  useEffect(() => {
    if (!open) {
      return;
    }
    window.setTimeout(() => fitAddonRef.current?.fit(), 0);
  }, [open, terminalSize.width, terminalSize.height]);

  if (!open) {
    return null;
  }

  return (
    <div className="workspace-modal-backdrop" data-testid="terminal-modal-backdrop">
      <div
        aria-label="Terminal"
        className="workspace-modal workspace-terminal-modal surface-panel"
        data-testid="terminal-modal"
        ref={modalRef}
        role="dialog"
        style={{ width: terminalSize.width, height: terminalSize.height }}
      >
        <div className="workspace-modal-header workspace-terminal-header">
          <div className="workspace-modal-heading-group">
            <p className="eyebrow-label text-kodelet-mid-gray">Workspace</p>
            <h2 className="workspace-modal-title">Terminal</h2>
            <p className="workspace-modal-copy" title={cwdLabel}>
              {truncateMiddle(cwdLabel, 92)}
            </p>
          </div>

          <div className="workspace-modal-actions">
            <span className="workspace-modal-meta-pill">{shellName || 'shell'}</span>
            <button className="composer-capsule" onClick={onClose} type="button">
              Close
            </button>
          </div>
        </div>

        {currentStatus ? (
          <div className="workspace-terminal-status-bar">
            <span className={cn('workspace-terminal-status-dot', connectionError ? 'is-error' : exitCode !== null ? 'is-idle' : 'is-live')} />
            <span className="workspace-terminal-status-text">{currentStatus}</span>
          </div>
        ) : null}

        <div className="workspace-terminal-shell">
          <div className="workspace-terminal-host" data-testid="terminal-host" ref={terminalHostRef} />
        </div>

        <button
          aria-label="Resize terminal"
          className="workspace-terminal-resize-handle"
          data-testid="terminal-resize-handle"
          onPointerDown={(event) => {
            dragStateRef.current = {
              startX: event.clientX,
              startY: event.clientY,
              startWidth: terminalSize.width,
              startHeight: terminalSize.height,
            };
            document.body.style.userSelect = 'none';
            document.body.style.cursor = 'nwse-resize';
            resizeHandleRef.current?.setPointerCapture(event.pointerId);
          }}
          ref={resizeHandleRef}
          type="button"
        >
          <svg aria-hidden="true" className="h-4 w-4" fill="none" viewBox="0 0 24 24" xmlns="http://www.w3.org/2000/svg">
            <path d="M8 16h8" stroke="currentColor" strokeLinecap="round" strokeWidth="1.7" />
            <path d="M12 12h4" stroke="currentColor" strokeLinecap="round" strokeWidth="1.7" />
            <path d="M16 8h0.01" stroke="currentColor" strokeLinecap="round" strokeWidth="1.7" />
          </svg>
        </button>
      </div>
    </div>
  );
};

export default TerminalModal;
