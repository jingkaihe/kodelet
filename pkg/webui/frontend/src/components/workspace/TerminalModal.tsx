import React, { useCallback, useEffect, useMemo, useRef, useState } from 'react';
import { FitAddon, Ghostty, Terminal } from 'ghostty-web';
import ghosttyWasmUrl from 'ghostty-web/ghostty-vt.wasm?url';
import type {
  TerminalClientMessage,
  TerminalExitEvent,
  TerminalServerEvent,
} from '../../types';
import apiService from '../../services/api';
import TerminalModalFrame, { type TerminalStatusVariant } from './TerminalModalFrame';
import {
  clearTerminalPopOutRecord,
  createTerminalPopOutChannel,
  isTerminalPopOutMessage,
  readTerminalPopOutRecord,
  readTerminalPopOutRecordById,
  TERMINAL_POP_OUT_HEARTBEAT_INTERVAL,
  TERMINAL_POP_OUT_RELOAD_GRACE_PERIOD,
  TERMINAL_POP_OUT_STORAGE_KEY,
} from './terminalPopOut';

interface TerminalModalProps {
  cwdLabel: string;
  conversationId?: string;
  runnerId?: string;
  open: boolean;
  onClose: () => void;
  showPopOut?: boolean;
}

const FALLBACK_TERMINAL_FONT_FAMILY = '"SFMono-Regular", Menlo, Monaco, Consolas, "Liberation Mono", "Ubuntu Mono", monospace';
const TERMINAL_BOTTOM_RESERVED_ROWS = 1;
const TERMINAL_FONT_SIZE = 13;
const SGR_MOUSE_MODE = 1006;
const WHEEL_BUTTON_UP = 64;
const WHEEL_BUTTON_DOWN = 65;
const WHEEL_BUTTON_LEFT = 66;
const WHEEL_BUTTON_RIGHT = 67;
const WHEEL_PIXEL_FALLBACK = 33;
const POP_OUT_CLOSED_POLL_INTERVAL = 250;
const POP_OUT_NAVIGATION_GRACE_PERIOD = 15000;
const TERMINAL_POP_OUT_WINDOW_FEATURES = 'popup=yes,width=1120,height=760,resizable=yes,scrollbars=no';

let ghosttyLoadPromise: Promise<Ghostty> | null = null;
let activeTerminalPopOutWindow: Window | null = null;
let activeTerminalPopOutCwd = '';
let activeTerminalPopOutPendingUntil = 0;

const getTerminalPopOutURL = (cwd: string): URL => {
  const url = new URL('/terminal', window.location.origin);
  if (cwd) {
    url.searchParams.set('cwd', cwd);
  }
  return url;
};

const clearActiveTerminalPopOutWindow = () => {
  activeTerminalPopOutWindow = null;
  activeTerminalPopOutCwd = '';
  activeTerminalPopOutPendingUntil = 0;
};

const isExpectedTerminalPopOutWindow = (candidate: Window, cwd: string): boolean => {
  try {
    const url = new URL(candidate.location.href, window.location.href);
    return (
      url.origin === window.location.origin &&
      url.pathname === '/terminal' &&
      (url.searchParams.get('cwd') ?? '') === cwd
    );
  } catch {
    return false;
  }
};

const rememberActiveTerminalPopOutWindow = (
  candidate: Window,
  cwd: string,
  pendingNavigation: boolean
) => {
  activeTerminalPopOutWindow = candidate;
  activeTerminalPopOutCwd = cwd;
  activeTerminalPopOutPendingUntil = pendingNavigation
    ? Date.now() + POP_OUT_NAVIGATION_GRACE_PERIOD
    : 0;
};

const getActiveTerminalPopOutWindow = (cwd?: string) => {
  if (activeTerminalPopOutWindow?.closed) {
    clearActiveTerminalPopOutWindow();
  } else if (activeTerminalPopOutWindow) {
    if (
      isExpectedTerminalPopOutWindow(
        activeTerminalPopOutWindow,
        activeTerminalPopOutCwd
      )
    ) {
      activeTerminalPopOutPendingUntil = 0;
    } else if (Date.now() >= activeTerminalPopOutPendingUntil) {
      clearActiveTerminalPopOutWindow();
    }
  }

  return cwd === undefined || activeTerminalPopOutCwd === cwd
    ? activeTerminalPopOutWindow
    : null;
};

const loadGhostty = () => {
  ghosttyLoadPromise ??= Ghostty.load(ghosttyWasmUrl);
  return ghosttyLoadPromise;
};

const loadTerminalFont = async (fontFamily: string) => {
  if (!('fonts' in document)) {
    return;
  }

  await document.fonts.load(`${TERMINAL_FONT_SIZE}px ${fontFamily}`);
  await document.fonts.ready;
};

const isTerminalServerEvent = (value: unknown): value is TerminalServerEvent => {
  if (!value || typeof value !== 'object' || Array.isArray(value)) {
    return false;
  }

  const type = (value as { type?: unknown }).type;
  return type === 'ready' || type === 'exit' || type === 'info' || type === 'replay-complete';
};

const clamp = (value: number, min: number, max: number) => Math.min(Math.max(value, min), max);

const getWheelRepeatCount = (event: WheelEvent, cellHeight: number, rows: number) => {
  const isHorizontal = Math.abs(event.deltaX) > Math.abs(event.deltaY);
  const delta = isHorizontal ? event.deltaX : event.deltaY;
  if (delta === 0) {
    return 0;
  }

  let unitDelta: number;
  switch (event.deltaMode) {
    case 1:
      unitDelta = Math.abs(delta);
      break;
    case 2:
      unitDelta = Math.abs(delta) * rows;
      break;
    default:
      unitDelta = Math.abs(delta) / Math.max(1, cellHeight || WHEEL_PIXEL_FALLBACK);
      break;
  }

  return clamp(Math.max(1, Math.round(unitDelta)), 1, 5);
};

const getSGRWheelButton = (event: WheelEvent) => {
  if (Math.abs(event.deltaX) > Math.abs(event.deltaY)) {
    return event.deltaX > 0 ? WHEEL_BUTTON_RIGHT : WHEEL_BUTTON_LEFT;
  }
  return event.deltaY > 0 ? WHEEL_BUTTON_DOWN : WHEEL_BUTTON_UP;
};

const getSGRMouseModifiers = (event: WheelEvent) => {
  let modifiers = 0;
  if (event.shiftKey) {
    modifiers += 4;
  }
  if (event.altKey) {
    modifiers += 8;
  }
  if (event.ctrlKey) {
    modifiers += 16;
  }
  return modifiers;
};

const drainTerminalResponses = (terminal: Terminal, onResponse: (data: string) => void) => {
  const wasmTerm = terminal.wasmTerm;
  if (!wasmTerm || typeof wasmTerm.readResponse !== 'function') {
    return;
  }

  while (true) {
    const response = wasmTerm.readResponse();
    if (response === null) {
      return;
    }
    if (response !== '') {
      onResponse(response);
    }
  }
};

const TerminalModal: React.FC<TerminalModalProps> = ({
  cwdLabel,
  conversationId,
  runnerId,
  open,
  onClose,
  showPopOut = true,
}) => {
  const popOutEnabled = showPopOut && !conversationId && !runnerId;
  const terminalRef = useRef<Terminal | null>(null);
  const fitAddonRef = useRef<FitAddon | null>(null);
  const socketRef = useRef<WebSocket | null>(null);
  const terminalHostRef = useRef<HTMLDivElement | null>(null);
  const replayPendingWritesRef = useRef(0);
  const replayCompleteReceivedRef = useRef(false);
  const suppressTerminalInputRef = useRef(true);
  const [statusText, setStatusText] = useState('Connecting…');
  const [connectionError, setConnectionError] = useState<string | null>(null);
  const [exitCode, setExitCode] = useState<number | null>(null);
  const [popOutActive, setPopOutActive] = useState(
    () =>
      popOutEnabled &&
      (getActiveTerminalPopOutWindow(cwdLabel) !== null ||
        readTerminalPopOutRecord(cwdLabel) !== null)
  );

  const fitTerminalToPanel = useCallback(() => {
    const terminal = terminalRef.current;
    const fitAddon = fitAddonRef.current;

    if (!terminal || !fitAddon) {
      return false;
    }

    const proposedDimensions = fitAddon.proposeDimensions();
    if (!proposedDimensions || Number.isNaN(proposedDimensions.cols) || Number.isNaN(proposedDimensions.rows)) {
      return false;
    }

    const cols = Math.max(2, proposedDimensions.cols);
    const rows = Math.max(1, proposedDimensions.rows - TERMINAL_BOTTOM_RESERVED_ROWS);

    if (terminal.cols !== cols || terminal.rows !== rows) {
      terminal.resize(cols, rows);
    }

    return true;
  }, []);

  const currentStatus = useMemo(() => {
    if (connectionError) {
      return connectionError;
    }
    if (exitCode !== null) {
      return `Exited with code ${exitCode}`;
    }
    return statusText === 'Connected' ? '' : statusText;
  }, [connectionError, exitCode, statusText]);
  const statusVariant: TerminalStatusVariant = connectionError ? 'error' : exitCode !== null ? 'idle' : 'live';

  useEffect(() => {
    if (!popOutEnabled) {
      setPopOutActive(false);
    }
  }, [popOutEnabled]);

  useEffect(() => {
    if (!open || popOutActive || !terminalHostRef.current) {
      return undefined;
    }

    let cancelled = false;
    let terminal: Terminal | null = null;
    let socket: WebSocket | null = null;
    let disposableData: { dispose: () => void } | null = null;
    let disposableResize: { dispose: () => void } | null = null;
    let resizeObserver: ResizeObserver | null = null;
    let handleWindowResize: (() => void) | null = null;
    const pendingTimeouts: number[] = [];
    const pendingAnimationFrames: number[] = [];

    const resolvedMonoFontFamily =
      window.getComputedStyle(document.documentElement)
        .getPropertyValue('--font-mono')
        .trim() || FALLBACK_TERMINAL_FONT_FAMILY;

    setStatusText('Connecting…');
    setConnectionError(null);
    setExitCode(null);

    const getCurrentConnection = (): { terminal: Terminal; socket: WebSocket } | null => {
      const currentTerminal = terminal;
      const currentSocket = socket;
      if (
        cancelled ||
        !currentTerminal ||
        !currentSocket ||
        terminalRef.current !== currentTerminal ||
        socketRef.current !== currentSocket
      ) {
        return null;
      }

      return { terminal: currentTerminal, socket: currentSocket };
    };

    const releaseReplaySuppressionIfReady = () => {
      if (
        !getCurrentConnection() ||
        !replayCompleteReceivedRef.current ||
        replayPendingWritesRef.current > 0
      ) {
        return;
      }

      suppressTerminalInputRef.current = false;
      setStatusText('Connected');
    };

    const sendMessage = (message: TerminalClientMessage) => {
      const connection = getCurrentConnection();
      if (!connection || connection.socket.readyState !== WebSocket.OPEN) {
        return;
      }
      connection.socket.send(JSON.stringify(message));
    };

    const sendTerminalInput = (data: string) => {
      if (suppressTerminalInputRef.current) {
        return;
      }
      sendMessage({ type: 'input', data });
    };

    const writeTerminalOutput = (targetTerminal: Terminal, data: Uint8Array, callback?: () => void) => {
      targetTerminal.write(data, callback);
      drainTerminalResponses(targetTerminal, sendTerminalInput);
    };

    const sendResize = () => {
      const connection = getCurrentConnection();
      if (!connection) {
        return;
      }
      fitTerminalToPanel();
      sendMessage({
        type: 'resize',
        rows: connection.terminal.rows,
        cols: connection.terminal.cols,
      });
    };

    const scheduleSettledResize = (delay = 0) => {
      const timeout = window.setTimeout(() => {
        if (cancelled) {
          return;
        }

        const firstFrame = window.requestAnimationFrame(() => {
          if (cancelled) {
            return;
          }

          const secondFrame = window.requestAnimationFrame(() => {
            if (!cancelled) {
              sendResize();
            }
          });
          pendingAnimationFrames.push(secondFrame);
        });
        pendingAnimationFrames.push(firstFrame);
      }, delay);

      pendingTimeouts.push(timeout);
    };

    void Promise.all([loadGhostty(), loadTerminalFont(resolvedMonoFontFamily)])
      .then(([ghostty]) => {
        if (cancelled || !terminalHostRef.current) {
          return;
        }

        terminal = new Terminal({
          ghostty,
          allowTransparency: true,
          convertEol: true,
          cursorBlink: true,
          cursorStyle: 'block',
          fontFamily: resolvedMonoFontFamily,
          fontSize: TERMINAL_FONT_SIZE,
          scrollback: 5000,
          theme: {
            background: '#18140f',
            foreground: '#f4eee3',
            cursor: '#d97757',
            cursorAccent: '#171512',
            selectionBackground: '#624733',
            selectionForeground: '#fffaf1',
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
        terminalRef.current = terminal;
        fitAddonRef.current = fitAddon;
        fitTerminalToPanel();
        terminal.focus();
        replayPendingWritesRef.current = 0;
        replayCompleteReceivedRef.current = false;
        suppressTerminalInputRef.current = true;

        socket = apiService.createTerminalWebSocket({
          cwd: conversationId || runnerId ? undefined : cwdLabel,
          conversationId,
          runnerId,
          rows: terminal.rows,
          cols: terminal.cols,
        });
        socket.binaryType = 'arraybuffer';
        socketRef.current = socket;

        socket.addEventListener('open', () => {
          if (!getCurrentConnection()) {
            return;
          }
          scheduleSettledResize();
        });

        socket.addEventListener('message', (event) => {
          const connection = getCurrentConnection();
          if (!connection) {
            return;
          }
          const currentTerminal = connection.terminal;

          if (typeof event.data === 'string') {
            try {
              const payload = JSON.parse(event.data) as unknown;
              if (!isTerminalServerEvent(payload)) {
                return;
              }

              if (payload.type === 'ready') {
                setStatusText('Restoring session…');
                scheduleSettledResize();
                return;
              }

              if (payload.type === 'replay-complete') {
                replayCompleteReceivedRef.current = true;
                releaseReplaySuppressionIfReady();
                return;
              }

              if (payload.type === 'exit') {
                const exitPayload = payload as TerminalExitEvent;
                setExitCode(exitPayload.code);
                setStatusText(`Exited with code ${exitPayload.code}`);
                currentTerminal.writeln(`\r\n[process exited with code ${exitPayload.code}]`);
                return;
              }

              if (payload.type === 'info') {
                currentTerminal.writeln(`\r\n${payload.text}`);
              }
            } catch {
              currentTerminal.writeln(`\r\n${event.data}`);
            }
            return;
          }

          if (event.data instanceof ArrayBuffer) {
            if (!replayCompleteReceivedRef.current) {
              replayPendingWritesRef.current += 1;
              writeTerminalOutput(currentTerminal, new Uint8Array(event.data), () => {
                if (!getCurrentConnection()) {
                  return;
                }
                replayPendingWritesRef.current = Math.max(0, replayPendingWritesRef.current - 1);
                releaseReplaySuppressionIfReady();
              });
              return;
            }

            writeTerminalOutput(currentTerminal, new Uint8Array(event.data));
          }
        });

        socket.addEventListener('close', () => {
          if (!getCurrentConnection()) {
            return;
          }
          setStatusText((current) => (current.startsWith('Exited with code') ? current : 'Disconnected'));
        });

        socket.addEventListener('error', () => {
          if (!getCurrentConnection()) {
            return;
          }
          setConnectionError('Terminal connection failed');
        });

        disposableData = terminal.onData((data) => {
          sendTerminalInput(data);
        });

        disposableResize = terminal.onResize(({ rows, cols }) => {
          sendMessage({ type: 'resize', rows, cols });
        });

        terminal.attachCustomKeyEventHandler((event) => {
          if (event.type !== 'keydown') {
            return false;
          }

          const key = event.key.toLowerCase();
          if ((event.ctrlKey || event.metaKey) && key === 'r') {
            return false;
          }
          if ((event.ctrlKey || event.metaKey) && key === 'c' && terminal?.hasSelection()) {
            return false;
          }
          return false;
        });

        terminal.attachCustomWheelEventHandler((event) => {
          if (
            suppressTerminalInputRef.current ||
            !terminal?.wasmTerm?.isAlternateScreen() ||
            !terminal.wasmTerm.hasMouseTracking() ||
            !terminal.wasmTerm.getMode(SGR_MOUSE_MODE, false)
          ) {
            return false;
          }

          const canvas = terminal.renderer?.getCanvas();
          const metrics = terminal.renderer?.getMetrics();
          if (!canvas || !metrics || metrics.width <= 0 || metrics.height <= 0) {
            return false;
          }

          const repeatCount = getWheelRepeatCount(event, metrics.height, terminal.rows);
          if (repeatCount === 0) {
            return false;
          }

          const rect = canvas.getBoundingClientRect();
          const col = clamp(Math.floor((event.clientX - rect.left) / metrics.width) + 1, 1, terminal.cols);
          const row = clamp(Math.floor((event.clientY - rect.top) / metrics.height) + 1, 1, terminal.rows);
          const button = getSGRWheelButton(event) + getSGRMouseModifiers(event);

          event.preventDefault();
          event.stopPropagation();
          sendTerminalInput(`\x1b[<${button};${col};${row}M`.repeat(repeatCount));
          return true;
        });

        handleWindowResize = () => sendResize();
        window.addEventListener('resize', handleWindowResize);
        resizeObserver = typeof ResizeObserver === 'undefined'
          ? null
          : new ResizeObserver(() => sendResize());
        if (resizeObserver) {
          resizeObserver.observe(terminalHostRef.current);
        }

        scheduleSettledResize();
        scheduleSettledResize(100);
        if ('fonts' in document) {
          void document.fonts.ready.then(() => {
            if (!cancelled) {
              terminalRef.current?.renderer?.remeasureFont();
              scheduleSettledResize();
            }
          });
        }
      })
      .catch((error: unknown) => {
        if (cancelled) {
          return;
        }

        console.error('Failed to initialize terminal', error);
        setConnectionError('Terminal initialization failed');
      });

    return () => {
      cancelled = true;
      pendingTimeouts.forEach((timeout) => {
        window.clearTimeout(timeout);
      });
      pendingAnimationFrames.forEach((animationFrame) => {
        window.cancelAnimationFrame(animationFrame);
      });
      if (handleWindowResize) {
        window.removeEventListener('resize', handleWindowResize);
      }
      resizeObserver?.disconnect();
      disposableResize?.dispose();
      disposableData?.dispose();
      socket?.close();
      if (socketRef.current === socket) {
        socketRef.current = null;
      }
      terminal?.dispose();
      if (terminalRef.current === terminal) {
        terminalRef.current = null;
      }
      fitAddonRef.current = null;
    };
  }, [conversationId, cwdLabel, fitTerminalToPanel, open, popOutActive, runnerId]);

  useEffect(() => {
    if (!popOutEnabled) {
      return undefined;
    }

    const channel = createTerminalPopOutChannel();
    const pendingCloseTimeouts = new Set<number>();
    const syncPopOutState = () => {
      setPopOutActive(
        getActiveTerminalPopOutWindow(cwdLabel) !== null ||
          readTerminalPopOutRecord(cwdLabel) !== null
      );
    };
    const handleChannelMessage = (event: MessageEvent<unknown>) => {
      if (!isTerminalPopOutMessage(event.data)) {
        return;
      }
      if (event.data.type === 'active' && event.data.record.cwd === cwdLabel) {
        setPopOutActive(true);
        return;
      }
      if (event.data.type === 'closing' && event.data.cwd === cwdLabel) {
        const closingId = event.data.id;
        const closingRecord = readTerminalPopOutRecordById(closingId);
        channel?.postMessage({ type: 'probe' });
        const closeTimeout = window.setTimeout(() => {
          pendingCloseTimeouts.delete(closeTimeout);
          const currentRecord = readTerminalPopOutRecordById(closingId);
          if (
            getActiveTerminalPopOutWindow(cwdLabel) === null &&
            closingRecord &&
            currentRecord &&
            currentRecord.id === closingId &&
            currentRecord.cwd === cwdLabel &&
            currentRecord.state === 'closing' &&
            closingRecord.cwd === cwdLabel &&
            currentRecord.updatedAt === closingRecord.updatedAt
          ) {
            clearTerminalPopOutRecord(currentRecord.id);
          }
          syncPopOutState();
        }, TERMINAL_POP_OUT_RELOAD_GRACE_PERIOD);
        pendingCloseTimeouts.add(closeTimeout);
      }
    };
    const handleStorage = (event: StorageEvent) => {
      if (event.key === TERMINAL_POP_OUT_STORAGE_KEY) {
        syncPopOutState();
      }
    };

    syncPopOutState();
    channel?.addEventListener('message', handleChannelMessage);
    channel?.postMessage({ type: 'probe' });
    const probeWindow = channel
      ? window.setInterval(
          () => channel.postMessage({ type: 'probe' }),
          TERMINAL_POP_OUT_HEARTBEAT_INTERVAL
        )
      : null;
    const pollWindow = window.setInterval(
      syncPopOutState,
      POP_OUT_CLOSED_POLL_INTERVAL
    );
    window.addEventListener('focus', syncPopOutState);
    window.addEventListener('storage', handleStorage);

    return () => {
      window.clearInterval(pollWindow);
      if (probeWindow !== null) {
        window.clearInterval(probeWindow);
      }
      pendingCloseTimeouts.forEach((timeout) => {
        window.clearTimeout(timeout);
      });
      window.removeEventListener('focus', syncPopOutState);
      window.removeEventListener('storage', handleStorage);
      channel?.removeEventListener('message', handleChannelMessage);
      channel?.close();
    };
  }, [cwdLabel, popOutEnabled]);

  useEffect(() => {
    if (!open) {
      return undefined;
    }

    const handleKeyDown = (event: KeyboardEvent) => {
      if (event.key !== 'Escape' || event.defaultPrevented) {
        return;
      }

      if (terminalHostRef.current?.parentElement?.closest('[inert]')) {
        return;
      }

      const target = event.target;
      if (target instanceof Node && terminalHostRef.current?.contains(target)) {
        return;
      }

      event.preventDefault();
      onClose();
    };

    window.addEventListener('keydown', handleKeyDown);
    return () => window.removeEventListener('keydown', handleKeyDown);
  }, [open, onClose]);

  const handlePopOut = useCallback(() => {
    const activePopOut = getActiveTerminalPopOutWindow(cwdLabel);
    if (activePopOut) {
      activePopOut.focus();
      setPopOutActive(true);
      return;
    }

    const persistedPopOut = readTerminalPopOutRecord(cwdLabel);
    if (persistedPopOut) {
      const existingPopOut = window.open(
        '',
        'kodelet-terminal',
        TERMINAL_POP_OUT_WINDOW_FEATURES
      );
      if (existingPopOut) {
        const alreadyShowingTerminal = isExpectedTerminalPopOutWindow(
          existingPopOut,
          cwdLabel
        );
        if (!alreadyShowingTerminal) {
          try {
            existingPopOut.location.href = getTerminalPopOutURL(cwdLabel).toString();
          } catch {
            clearTerminalPopOutRecord(persistedPopOut.id);
            setPopOutActive(false);
            return;
          }
        }
        rememberActiveTerminalPopOutWindow(
          existingPopOut,
          cwdLabel,
          !alreadyShowingTerminal
        );
        setPopOutActive(true);
        existingPopOut.focus();
      }
      return;
    }

    const url = getTerminalPopOutURL(cwdLabel);

    const popOutWindow = window.open(
      url.toString(),
      'kodelet-terminal',
      TERMINAL_POP_OUT_WINDOW_FEATURES
    );

    if (!popOutWindow) {
      return;
    }

    rememberActiveTerminalPopOutWindow(popOutWindow, cwdLabel, true);
    setPopOutActive(true);
    popOutWindow.focus();
  }, [cwdLabel]);

  if (!open) {
    return null;
  }

  return (
    <TerminalModalFrame
      currentStatus={popOutActive ? '' : currentStatus}
      cwdLabel={cwdLabel}
      popOutActive={popOutActive}
      statusVariant={popOutActive ? 'idle' : statusVariant}
      terminalHostRef={terminalHostRef}
      onClose={onClose}
      onPopOut={popOutEnabled ? handlePopOut : undefined}
    />
  );
};

export default TerminalModal;
