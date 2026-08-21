import { act, render, screen, waitFor } from '@testing-library/react';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import TerminalModal from './TerminalModal';
import {
  clearTerminalPopOutRecord,
  readTerminalPopOutRecord,
  readTerminalPopOutRecordForTarget,
  TERMINAL_POP_OUT_RELOAD_GRACE_PERIOD,
  TERMINAL_POP_OUT_STORAGE_KEY,
  type TerminalPopOutRecord,
  writeTerminalPopOutRecord,
} from './terminalPopOut';

const { MockFitAddon, MockGhosttyLoad, MockTerminal, createTerminalWebSocketMock } = vi.hoisted(() => {
  class HoistedMockFitAddon {
    fit = vi.fn();
    proposeDimensions = vi.fn(() => ({ cols: 80, rows: 24 }));
  }

  type MockDataHandler = (data: string) => void;
  type MockResizeHandler = (size: { rows: number; cols: number }) => void;
  type MockReadResponse = () => string | null;

  class HoistedMockTerminal {
    static instances: HoistedMockTerminal[] = [];

    rows = 24;
    cols = 80;
    write = vi.fn((_: Uint8Array, callback?: () => void) => {
      this.dataHandler?.('parser-response');
      callback?.();
    });
    writeln = vi.fn();
    loadAddon = vi.fn();
    open = vi.fn();
    focus = vi.fn();
    resize = vi.fn((cols: number, rows: number) => {
      this.cols = cols;
      this.rows = rows;
    });
    dispose = vi.fn();
    attachCustomKeyEventHandler = vi.fn((handler: (event: KeyboardEvent) => boolean) => {
      this.customKeyEventHandler = handler;
    });
    attachCustomWheelEventHandler = vi.fn((handler: (event: WheelEvent) => boolean) => {
      this.customWheelEventHandler = handler;
    });
    hasSelection = vi.fn(() => false);
    renderer = {
      getCanvas: vi.fn(() => ({
        getBoundingClientRect: () => ({
          bottom: 505,
          height: 500,
          left: 5,
          right: 805,
          top: 5,
          width: 800,
          x: 5,
          y: 5,
          toJSON: () => ({}),
        }),
      })),
      getMetrics: vi.fn(() => ({ width: 10, height: 20, baseline: 16 })),
      remeasureFont: vi.fn(),
    };
    wasmTerm = {
      getMode: vi.fn(() => true),
      hasMouseTracking: vi.fn(() => true),
      isAlternateScreen: vi.fn(() => true),
      readResponse: vi.fn<MockReadResponse>(() => null),
    };

    private dataHandler?: MockDataHandler;
    private resizeHandler?: MockResizeHandler;
    private customKeyEventHandler?: (event: KeyboardEvent) => boolean;
    private customWheelEventHandler?: (event: WheelEvent) => boolean;

    constructor() {
      HoistedMockTerminal.instances.push(this);
    }

    onData(handler: MockDataHandler) {
      this.dataHandler = handler;
      return { dispose: vi.fn() };
    }

    onResize(handler: MockResizeHandler) {
      this.resizeHandler = handler;
      return { dispose: vi.fn() };
    }

    emitData(data: string) {
      this.dataHandler?.(data);
    }

    emitResize(rows: number, cols: number) {
      this.resizeHandler?.({ rows, cols });
    }

    handleKey(event: KeyboardEvent) {
      return this.customKeyEventHandler?.(event);
    }

    handleWheel(event: WheelEvent) {
      return this.customWheelEventHandler?.(event);
    }
  }

  return {
    MockFitAddon: HoistedMockFitAddon,
    MockGhosttyLoad: vi.fn(() => Promise.resolve({})),
    MockTerminal: HoistedMockTerminal,
    createTerminalWebSocketMock: vi.fn(),
  };
});

vi.mock('../../services/api', () => ({
  default: {
    createTerminalWebSocket: createTerminalWebSocketMock,
  },
}));

vi.mock('ghostty-web', () => ({
  Terminal: MockTerminal,
  FitAddon: MockFitAddon,
  Ghostty: {
    load: MockGhosttyLoad,
  },
}));

class MockWebSocket {
  static readonly OPEN = 1;

  readyState = MockWebSocket.OPEN;
  binaryType = 'blob';
  send = vi.fn();
  close = vi.fn();

  private listeners = new Map<string, Array<(event?: unknown) => void>>();

  addEventListener(type: string, listener: (event?: unknown) => void) {
    const existing = this.listeners.get(type) ?? [];
    existing.push(listener);
    this.listeners.set(type, existing);
  }

  emit(type: string, event?: unknown) {
    for (const listener of this.listeners.get(type) ?? []) {
      listener(event);
    }
  }
}

const localTarget = { kind: 'local', cwd: '/tmp/project' } as const;

describe('TerminalModal', () => {
  beforeEach(() => {
    MockTerminal.instances = [];
    createTerminalWebSocketMock.mockReset();
    window.localStorage.removeItem(TERMINAL_POP_OUT_STORAGE_KEY);
    window.sessionStorage.clear();
  });

  it('suppresses parser-generated input until replay completes', async () => {
    const socket = new MockWebSocket();
    createTerminalWebSocketMock.mockReturnValue(socket);

    render(<TerminalModal cwdLabel="/tmp/project" onClose={vi.fn()} open target={localTarget} />);

    await waitFor(() => expect(MockTerminal.instances[0]).toBeDefined());
    const terminal = MockTerminal.instances[0];

    act(() => {
      socket.emit('open');
      socket.emit('message', { data: JSON.stringify({ type: 'ready', cwd: '/tmp/project', name: 'bash', git: false, pid: 123 }) });
      socket.emit('message', { data: new ArrayBuffer(8) });
    });

    expect(socket.send).not.toHaveBeenCalledWith(JSON.stringify({ type: 'input', data: 'parser-response' }));

    act(() => {
      socket.emit('message', { data: JSON.stringify({ type: 'replay-complete' }) });
      terminal.emitData('ls\n');
    });

    expect(socket.send).toHaveBeenCalledWith(JSON.stringify({ type: 'input', data: 'ls\n' }));
  });

  it('drains every terminal response generated by a PTY output chunk', async () => {
    const socket = new MockWebSocket();
    createTerminalWebSocketMock.mockReturnValue(socket);

    render(<TerminalModal cwdLabel="/tmp/project" onClose={vi.fn()} open target={localTarget} />);

    await waitFor(() => expect(MockTerminal.instances[0]).toBeDefined());
    const terminal = MockTerminal.instances[0];
    terminal.write.mockImplementation((_: Uint8Array, callback?: () => void) => {
      callback?.();
    });
    terminal.wasmTerm.readResponse
      .mockReturnValueOnce('\x1b[1;1R')
      .mockReturnValueOnce('\x1b]4;0;rgb:00/00/00\x1b\\')
      .mockReturnValueOnce(null);

    act(() => {
      socket.emit('message', { data: JSON.stringify({ type: 'replay-complete' }) });
      socket.emit('message', { data: new ArrayBuffer(8) });
    });

    expect(socket.send).toHaveBeenCalledWith(JSON.stringify({ type: 'input', data: '\x1b[1;1R' }));
    expect(socket.send).toHaveBeenCalledWith(JSON.stringify({ type: 'input', data: '\x1b]4;0;rgb:00/00/00\x1b\\' }));
  });

  it('reserves bottom space when fitting the terminal panel', async () => {
    const socket = new MockWebSocket();
    createTerminalWebSocketMock.mockReturnValue(socket);

    render(<TerminalModal cwdLabel="/tmp/project" onClose={vi.fn()} open target={localTarget} />);

    await waitFor(() => expect(MockTerminal.instances[0]).toBeDefined());
    const terminal = MockTerminal.instances[0];
    expect(terminal.resize).toHaveBeenCalledWith(80, 23);
  });

  it('opens a runner-scoped remote terminal pop-out with conversation validation', async () => {
    const socket = new MockWebSocket();
    const openSpy = vi.spyOn(window, 'open').mockReturnValue(null);
    createTerminalWebSocketMock.mockReturnValue(socket);
    writeTerminalPopOutRecord({
      id: 'local-pop-out',
      target: { kind: 'local', cwd: '/runner/project' },
      state: 'active',
      updatedAt: Date.now(),
      version: 3,
    });

    render(
      <TerminalModal
        cwdLabel="/runner/project"
        onClose={vi.fn()}
        open
        target={{ kind: 'runner', runnerId: 'runner-1', conversationId: 'conv-123' }}
      />
    );

    await waitFor(() =>
      expect(createTerminalWebSocketMock).toHaveBeenCalledWith({
        target: { kind: 'runner', runnerId: 'runner-1', conversationId: 'conv-123' },
        rows: 23,
        cols: 80,
      })
    );
    act(() => {
      screen.getByRole('button', { name: 'Open terminal in new window' }).click();
    });

    expect(openSpy).toHaveBeenCalledWith(
      'http://localhost:3000/terminal?runnerId=runner-1&conversationId=conv-123',
      'kodelet-terminal',
      'popup=yes,width=1120,height=760,resizable=yes,scrollbars=no'
    );

    openSpy.mockRestore();
  });

  it('keeps remote pop-out disabled until conversation affinity is established', async () => {
    const socket = new MockWebSocket();
    createTerminalWebSocketMock.mockReturnValue(socket);

    render(
      <TerminalModal
        cwdLabel="/runner/project"
        onClose={vi.fn()}
        open
        target={{ kind: 'runner', runnerId: 'runner-1' }}
      />
    );

    await waitFor(() =>
      expect(createTerminalWebSocketMock).toHaveBeenCalledWith({
        target: { kind: 'runner', runnerId: 'runner-1' },
        rows: 23,
        cols: 80,
      })
    );
    expect(screen.queryByRole('button', { name: 'Open terminal in new window' })).not.toBeInTheDocument();
  });

  it('recognizes an existing runner pop-out before conversation affinity is established', () => {
    const popOutWindow = {
      closed: false,
      focus: vi.fn(),
      location: {
        href: 'http://localhost:3000/terminal?runnerId=runner-1&conversationId=conv-a',
      },
    };
    const openSpy = vi
      .spyOn(window, 'open')
      .mockReturnValue(popOutWindow as unknown as Window);
    writeTerminalPopOutRecord({
      id: 'runner-pop-out',
      target: { kind: 'runner', runnerId: 'runner-1', conversationId: 'conv-a' },
      state: 'active',
      updatedAt: Date.now(),
      version: 3,
    });

    render(
      <TerminalModal
        cwdLabel="/runner/project"
        onClose={vi.fn()}
        open
        target={{ kind: 'runner', runnerId: 'runner-1' }}
      />
    );

    expect(screen.getByText('Terminal is open in the pop-out')).toBeInTheDocument();
    expect(screen.queryByRole('button', { name: 'Open terminal in new window' })).not.toBeInTheDocument();
    act(() => {
      screen.getByRole('button', { name: 'Focus pop-out' }).click();
    });
    expect(popOutWindow.focus).toHaveBeenCalledTimes(1);
    expect(createTerminalWebSocketMock).not.toHaveBeenCalled();

    popOutWindow.closed = true;
    openSpy.mockRestore();
  });

  it('does not create a runner-only pop-out when stale ownership disappears before focus', async () => {
    const socket = new MockWebSocket();
    createTerminalWebSocketMock.mockReturnValue(socket);
    const openSpy = vi.spyOn(window, 'open').mockReturnValue(null);
    writeTerminalPopOutRecord({
      id: 'stale-runner-pop-out',
      target: { kind: 'runner', runnerId: 'runner-1', conversationId: 'conv-a' },
      state: 'active',
      updatedAt: Date.now(),
      version: 3,
    });

    render(
      <TerminalModal
        cwdLabel="/runner/project"
        onClose={vi.fn()}
        open
        target={{ kind: 'runner', runnerId: 'runner-1' }}
      />
    );

    clearTerminalPopOutRecord('stale-runner-pop-out');
    act(() => {
      screen.getByRole('button', { name: 'Focus pop-out' }).click();
    });

    expect(openSpy).not.toHaveBeenCalled();
    expect(screen.queryByText('Terminal is open in the pop-out')).not.toBeInTheDocument();
    await waitFor(() => expect(createTerminalWebSocketMock).toHaveBeenCalledTimes(1));
    openSpy.mockRestore();
  });

  it('recognizes a version 2 conversation-scoped pop-out during rolling upgrades', () => {
    window.localStorage.setItem(
      TERMINAL_POP_OUT_STORAGE_KEY,
      JSON.stringify({
        id: 'legacy-runner-pop-out',
        cwd: '/runner/project',
        conversationId: 'conv-legacy',
        state: 'active',
        updatedAt: Date.now(),
        version: 2,
      })
    );

    render(
      <TerminalModal
        cwdLabel="/runner/project"
        onClose={vi.fn()}
        open
        target={{ kind: 'runner', runnerId: 'runner-1', conversationId: 'conv-legacy' }}
      />
    );

    expect(screen.getByText('Terminal is open in the pop-out')).toBeInTheDocument();
    expect(createTerminalWebSocketMock).not.toHaveBeenCalled();
    expect(
      readTerminalPopOutRecordForTarget({
        kind: 'runner',
        runnerId: 'runner-1',
        conversationId: 'conv-legacy',
      })
    ).toEqual(
      expect.objectContaining({
        id: 'legacy-runner-pop-out',
        target: {
          kind: 'runner',
          runnerId: 'runner-1',
          conversationId: 'conv-legacy',
        },
      })
    );
  });

  it('keeps one terminal attachment when conversation validation changes on the same runner', async () => {
    const socket = new MockWebSocket();
    createTerminalWebSocketMock.mockReturnValue(socket);
    const { rerender } = render(
      <TerminalModal
        cwdLabel="/runner/project"
        onClose={vi.fn()}
        open
        target={{ kind: 'runner', runnerId: 'runner-1', conversationId: 'conv-a' }}
      />
    );

    await waitFor(() => expect(createTerminalWebSocketMock).toHaveBeenCalledTimes(1));
    rerender(
      <TerminalModal
        cwdLabel="/runner/project"
        onClose={vi.fn()}
        open
        target={{ kind: 'runner', runnerId: 'runner-1', conversationId: 'conv-b' }}
      />
    );

    await act(async () => Promise.resolve());
    expect(createTerminalWebSocketMock).toHaveBeenCalledTimes(1);
    expect(socket.close).not.toHaveBeenCalled();
  });

  it('recognizes a runner pop-out from every conversation on that runner', () => {
    writeTerminalPopOutRecord({
      id: 'runner-pop-out',
      target: { kind: 'runner', runnerId: 'runner-1', conversationId: 'conv-a' },
      state: 'active',
      updatedAt: Date.now(),
      version: 3,
    });

    render(
      <TerminalModal
        cwdLabel="/runner/project"
        onClose={vi.fn()}
        open
        target={{ kind: 'runner', runnerId: 'runner-1', conversationId: 'conv-b' }}
      />
    );

    expect(screen.getByText('Terminal is open in the pop-out')).toBeInTheDocument();
    expect(createTerminalWebSocketMock).not.toHaveBeenCalled();
  });

  it('reconnects a remote terminal without changing its runner target', async () => {
    const firstSocket = new MockWebSocket();
    const secondSocket = new MockWebSocket();
    createTerminalWebSocketMock
      .mockReturnValueOnce(firstSocket)
      .mockReturnValueOnce(secondSocket);

    render(
      <TerminalModal
        cwdLabel="/runner/project"
        onClose={vi.fn()}
        open
        target={{ kind: 'runner', runnerId: 'runner-1', conversationId: 'conv-a' }}
      />
    );

    await waitFor(() => expect(createTerminalWebSocketMock).toHaveBeenCalledTimes(1));
    act(() => {
      firstSocket.emit('close');
    });

    expect(screen.getByText('Reconnecting…')).toBeInTheDocument();
    await waitFor(() => expect(createTerminalWebSocketMock).toHaveBeenCalledTimes(2), {
      timeout: 1500,
    });
    expect(createTerminalWebSocketMock).toHaveBeenLastCalledWith({
      target: { kind: 'runner', runnerId: 'runner-1', conversationId: 'conv-a' },
      rows: 23,
      cols: 80,
    });
  });

  it('allows ghostty-web to process terminal keystrokes', async () => {
    const socket = new MockWebSocket();
    createTerminalWebSocketMock.mockReturnValue(socket);

    render(<TerminalModal cwdLabel="/tmp/project" onClose={vi.fn()} open target={localTarget} />);

    await waitFor(() => expect(MockTerminal.instances[0]).toBeDefined());
    const terminal = MockTerminal.instances[0];
    act(() => {
      socket.emit('message', { data: JSON.stringify({ type: 'replay-complete' }) });
    });

    expect(terminal.handleKey(new KeyboardEvent('keydown', { key: 'a' }))).toBe(false);
    expect(terminal.handleKey(new KeyboardEvent('keydown', { key: 'Tab' }))).toBe(false);
  });

  it('does not close when Escape is pressed inside the terminal', async () => {
    const socket = new MockWebSocket();
    const onClose = vi.fn();
    createTerminalWebSocketMock.mockReturnValue(socket);

    render(<TerminalModal cwdLabel="/tmp/project" onClose={onClose} open target={localTarget} />);

    await waitFor(() => expect(MockTerminal.instances[0]).toBeDefined());
    screen.getByTestId('terminal-host').dispatchEvent(new KeyboardEvent('keydown', {
      bubbles: true,
      key: 'Escape',
    }));

    expect(onClose).not.toHaveBeenCalled();
  });

  it('closes when Escape is pressed outside the terminal', async () => {
    const socket = new MockWebSocket();
    const onClose = vi.fn();
    createTerminalWebSocketMock.mockReturnValue(socket);

    render(<TerminalModal cwdLabel="/tmp/project" onClose={onClose} open target={localTarget} />);

    await waitFor(() => expect(MockTerminal.instances[0]).toBeDefined());
    window.dispatchEvent(new KeyboardEvent('keydown', { key: 'Escape' }));

    expect(onClose).toHaveBeenCalledTimes(1);
  });

  it('does not close behind a higher-priority inert overlay', async () => {
    const socket = new MockWebSocket();
    const onClose = vi.fn();
    createTerminalWebSocketMock.mockReturnValue(socket);

    render(
      <div inert>
        <TerminalModal cwdLabel="/tmp/project" onClose={onClose} open target={localTarget} />
      </div>
    );

    await waitFor(() => expect(MockTerminal.instances[0]).toBeDefined());
    window.dispatchEvent(new KeyboardEvent('keydown', { key: 'Escape' }));

    expect(onClose).not.toHaveBeenCalled();
  });

  it('reports wheel events to mouse-tracking terminal apps', async () => {
    const socket = new MockWebSocket();
    createTerminalWebSocketMock.mockReturnValue(socket);

    render(<TerminalModal cwdLabel="/tmp/project" onClose={vi.fn()} open target={localTarget} />);

    await waitFor(() => expect(MockTerminal.instances[0]).toBeDefined());
    const terminal = MockTerminal.instances[0];
    act(() => {
      socket.emit('message', { data: JSON.stringify({ type: 'replay-complete' }) });
    });

    const event = new WheelEvent('wheel', {
      clientX: 25,
      clientY: 45,
      deltaY: 10,
    });

    expect(terminal.handleWheel(event)).toBe(true);
    expect(socket.send).toHaveBeenCalledWith(JSON.stringify({ type: 'input', data: '\x1b[<65;3;3M' }));
  });

  it('leaves normal terminal scrollback handling to ghostty-web', async () => {
    const socket = new MockWebSocket();
    createTerminalWebSocketMock.mockReturnValue(socket);

    render(<TerminalModal cwdLabel="/tmp/project" onClose={vi.fn()} open target={localTarget} />);

    await waitFor(() => expect(MockTerminal.instances[0]).toBeDefined());
    const terminal = MockTerminal.instances[0];
    terminal.wasmTerm.isAlternateScreen.mockReturnValue(false);

    expect(terminal.handleWheel(new WheelEvent('wheel', { deltaY: 10 }))).toBe(false);
  });

  it('renders a simplified header', async () => {
    const socket = new MockWebSocket();
    createTerminalWebSocketMock.mockReturnValue(socket);

    render(<TerminalModal cwdLabel="/tmp/project" onClose={vi.fn()} open target={localTarget} />);

    await waitFor(() => expect(MockTerminal.instances[0]).toBeDefined());

    expect(screen.queryByRole('heading', { name: 'Terminal' })).not.toBeInTheDocument();
    expect(screen.queryByText('/tmp/project')).not.toBeInTheDocument();
    expect(screen.getByTestId('terminal-panel')).toBeInTheDocument();
    expect(screen.queryByTestId('terminal-modal-backdrop')).not.toBeInTheDocument();
    expect(screen.queryByText('Workspace')).not.toBeInTheDocument();
    expect(screen.queryByText('shell')).not.toBeInTheDocument();
  });

  it('opens the terminal pop-out window for the current cwd', async () => {
    const socket = new MockWebSocket();
    const openSpy = vi.spyOn(window, 'open').mockReturnValue(null);
    createTerminalWebSocketMock.mockReturnValue(socket);

    render(<TerminalModal cwdLabel="/tmp/project" onClose={vi.fn()} open target={localTarget} />);

    await waitFor(() => expect(MockTerminal.instances[0]).toBeDefined());
    act(() => {
      screen.getByRole('button', { name: 'Open terminal in new window' }).click();
    });

    expect(openSpy).toHaveBeenCalledWith(
      'http://localhost:3000/terminal?cwd=%2Ftmp%2Fproject',
      'kodelet-terminal',
      'popup=yes,width=1120,height=760,resizable=yes,scrollbars=no'
    );
    expect(socket.close).not.toHaveBeenCalled();
    expect(screen.queryByText('Terminal is open in the pop-out')).not.toBeInTheDocument();

    openSpy.mockRestore();
  });

  it('pauses the embedded terminal until the pop-out window closes', async () => {
    const firstSocket = new MockWebSocket();
    const restoredSocket = new MockWebSocket();
    const popOutWindow = {
      closed: false,
      focus: vi.fn(),
      location: {
        href: 'http://localhost:3000/terminal?cwd=%2Ftmp%2Fproject',
      },
    };
    const openSpy = vi
      .spyOn(window, 'open')
      .mockReturnValue(popOutWindow as unknown as Window);
    createTerminalWebSocketMock
      .mockReturnValueOnce(firstSocket)
      .mockReturnValueOnce(restoredSocket);

    render(<TerminalModal cwdLabel="/tmp/project" onClose={vi.fn()} open target={localTarget} />);

    await waitFor(() => expect(MockTerminal.instances[0]).toBeDefined());
    act(() => {
      screen.getByRole('button', { name: 'Open terminal in new window' }).click();
    });

    await waitFor(() => expect(firstSocket.close).toHaveBeenCalledTimes(1));
    expect(popOutWindow.focus).toHaveBeenCalledTimes(1);
    expect(screen.getByTestId('terminal-host')).toHaveAttribute('aria-disabled', 'true');
    expect(screen.getByText('Terminal is open in the pop-out')).toBeInTheDocument();
    expect(createTerminalWebSocketMock).toHaveBeenCalledTimes(1);

    act(() => {
      screen.getByRole('button', { name: 'Focus pop-out' }).click();
    });
    expect(openSpy).toHaveBeenCalledTimes(1);
    expect(popOutWindow.focus).toHaveBeenCalledTimes(2);

    popOutWindow.closed = true;
    act(() => {
      window.dispatchEvent(new Event('focus'));
    });

    await waitFor(() => expect(createTerminalWebSocketMock).toHaveBeenCalledTimes(2));
    expect(screen.getByTestId('terminal-host')).not.toHaveAttribute('aria-disabled');
    expect(screen.queryByText('Terminal is open in the pop-out')).not.toBeInTheDocument();

    const restoredTerminal = MockTerminal.instances[1];
    act(() => {
      firstSocket.emit('message', { data: JSON.stringify({ type: 'exit', code: 7 }) });
      firstSocket.emit('close');
      firstSocket.emit('error');
    });

    expect(restoredTerminal.writeln).not.toHaveBeenCalledWith(
      '\r\n[process exited with code 7]'
    );
    expect(screen.queryByText('Disconnected')).not.toBeInTheDocument();
    expect(screen.queryByText('Terminal connection failed')).not.toBeInTheDocument();

    openSpy.mockRestore();
  });

  it('keeps the embedded terminal paused after the opener reloads', async () => {
    const record: TerminalPopOutRecord = {
      id: 'existing-pop-out',
      target: localTarget,
      updatedAt: Date.now(),
      version: 3,
    };
    window.localStorage.setItem(
      TERMINAL_POP_OUT_STORAGE_KEY,
      JSON.stringify(record)
    );

    render(<TerminalModal cwdLabel="/tmp/project" onClose={vi.fn()} open target={localTarget} />);

    expect(screen.getByTestId('terminal-host')).toHaveAttribute('aria-disabled', 'true');
    expect(screen.getByText('Terminal is open in the pop-out')).toBeInTheDocument();
    expect(createTerminalWebSocketMock).not.toHaveBeenCalled();
  });

  it('keeps an old active record suspended when a mobile pop-out may be frozen', () => {
    const record: TerminalPopOutRecord = {
      id: 'background-pop-out',
      target: localTarget,
      state: 'active',
      updatedAt: Date.now() - 60_000,
      version: 3,
    };
    window.localStorage.setItem(
      TERMINAL_POP_OUT_STORAGE_KEY,
      JSON.stringify(record)
    );
    render(<TerminalModal cwdLabel="/tmp/project" onClose={vi.fn()} open target={localTarget} />);

    expect(createTerminalWebSocketMock).not.toHaveBeenCalled();
    expect(screen.getByTestId('terminal-host')).toHaveAttribute('aria-disabled', 'true');
    expect(screen.getByText('Terminal is open in the pop-out')).toBeInTheDocument();
    expect(readTerminalPopOutRecord('/tmp/project')).toEqual(record);
  });

  it('removes an abandoned closing record after the reload grace period', async () => {
    const socket = new MockWebSocket();
    const record: TerminalPopOutRecord = {
      id: 'closed-pop-out',
      target: localTarget,
      state: 'closing',
      updatedAt: Date.now() - TERMINAL_POP_OUT_RELOAD_GRACE_PERIOD - 1,
      version: 3,
    };
    window.localStorage.setItem(
      TERMINAL_POP_OUT_STORAGE_KEY,
      JSON.stringify(record)
    );
    createTerminalWebSocketMock.mockReturnValue(socket);

    render(<TerminalModal cwdLabel="/tmp/project" onClose={vi.fn()} open target={localTarget} />);

    await waitFor(() => expect(createTerminalWebSocketMock).toHaveBeenCalledTimes(1));
    expect(screen.getByTestId('terminal-host')).not.toHaveAttribute('aria-disabled');
    expect(readTerminalPopOutRecord('/tmp/project')).toBeNull();
  });

  it('preserves active pop-out leases for other working directories', () => {
    const firstRecord: TerminalPopOutRecord = {
      id: 'first-pop-out',
      target: { kind: 'local', cwd: '/tmp/first' },
      updatedAt: Date.now(),
      version: 3,
    };
    const secondRecord: TerminalPopOutRecord = {
      id: 'second-pop-out',
      target: { kind: 'local', cwd: '/tmp/second' },
      updatedAt: Date.now() + 1,
      version: 3,
    };

    writeTerminalPopOutRecord(firstRecord);
    writeTerminalPopOutRecord(secondRecord);

    expect(readTerminalPopOutRecord('/tmp/first')).toEqual(firstRecord);
    expect(readTerminalPopOutRecord('/tmp/second')).toEqual(secondRecord);

    clearTerminalPopOutRecord(firstRecord.id);

    expect(readTerminalPopOutRecord('/tmp/first')).toBeNull();
    expect(readTerminalPopOutRecord('/tmp/second')).toEqual(secondRecord);
  });

  it('keeps local and remote pop-out leases separate for the same cwd', () => {
    const localRecord: TerminalPopOutRecord = {
      id: 'local-pop-out',
      target: { kind: 'local', cwd: '/runner/project' },
      updatedAt: Date.now(),
      version: 3,
    };
    const remoteRecord: TerminalPopOutRecord = {
      id: 'remote-pop-out',
      target: { kind: 'runner', runnerId: 'runner-1', conversationId: 'conv-123' },
      updatedAt: Date.now() + 1,
      version: 3,
    };

    writeTerminalPopOutRecord(localRecord);
    writeTerminalPopOutRecord(remoteRecord);

    expect(readTerminalPopOutRecord('/runner/project')).toEqual(localRecord);
    expect(
      readTerminalPopOutRecordForTarget({
        kind: 'runner',
        runnerId: 'runner-1',
        conversationId: 'conv-123',
      })
    ).toEqual(remoteRecord);
  });

  it('focuses a persisted pop-out opened from another tab', () => {
    const popOutWindow = {
      closed: false,
      focus: vi.fn(),
      location: {
        href: 'http://localhost:3000/terminal?cwd=%2Ftmp%2Fproject',
      },
    };
    const openSpy = vi
      .spyOn(window, 'open')
      .mockReturnValue(popOutWindow as unknown as Window);
    const record: TerminalPopOutRecord = {
      id: 'other-tab-pop-out',
      target: localTarget,
      updatedAt: Date.now(),
      version: 3,
    };
    writeTerminalPopOutRecord(record);

    render(<TerminalModal cwdLabel="/tmp/project" onClose={vi.fn()} open target={localTarget} />);

    expect(screen.getByText('Terminal is open in the pop-out')).toBeInTheDocument();
    act(() => {
      screen.getByRole('button', { name: 'Focus pop-out' }).click();
    });

    expect(openSpy).toHaveBeenCalledWith(
      '',
      'kodelet-terminal',
      'popup=yes,width=1120,height=760,resizable=yes,scrollbars=no'
    );
    expect(popOutWindow.focus).toHaveBeenCalledTimes(1);
    expect(createTerminalWebSocketMock).not.toHaveBeenCalled();

    popOutWindow.closed = true;
    openSpy.mockRestore();
  });

  it('reopens a missing persisted pop-out instead of adopting a blank window', () => {
    const popOutWindow = {
      closed: false,
      focus: vi.fn(),
      location: { href: 'about:blank' },
    };
    const openSpy = vi
      .spyOn(window, 'open')
      .mockReturnValue(popOutWindow as unknown as Window);
    writeTerminalPopOutRecord({
      id: 'missing-pop-out',
      target: localTarget,
      state: 'active',
      updatedAt: Date.now(),
      version: 3,
    });

    render(<TerminalModal cwdLabel="/tmp/project" onClose={vi.fn()} open target={localTarget} />);
    act(() => {
      screen.getByRole('button', { name: 'Focus pop-out' }).click();
    });

    expect(popOutWindow.location.href).toBe(
      'http://localhost:3000/terminal?cwd=%2Ftmp%2Fproject'
    );
    expect(popOutWindow.focus).toHaveBeenCalledTimes(1);
    expect(createTerminalWebSocketMock).not.toHaveBeenCalled();

    popOutWindow.closed = true;
    openSpy.mockRestore();
  });

  it('restores the embedded terminal after the tracked pop-out navigates away', async () => {
    const firstSocket = new MockWebSocket();
    const restoredSocket = new MockWebSocket();
    const popOutWindow = {
      closed: false,
      focus: vi.fn(),
      location: {
        href: 'http://localhost:3000/terminal?cwd=%2Ftmp%2Fproject',
      },
    };
    const openSpy = vi
      .spyOn(window, 'open')
      .mockReturnValue(popOutWindow as unknown as Window);
    createTerminalWebSocketMock
      .mockReturnValueOnce(firstSocket)
      .mockReturnValueOnce(restoredSocket);

    render(<TerminalModal cwdLabel="/tmp/project" onClose={vi.fn()} open target={localTarget} />);
    await waitFor(() => expect(MockTerminal.instances[0]).toBeDefined());
    act(() => {
      screen.getByRole('button', { name: 'Open terminal in new window' }).click();
      window.dispatchEvent(new Event('focus'));
    });
    await waitFor(() => expect(firstSocket.close).toHaveBeenCalledTimes(1));

    popOutWindow.location.href = 'https://example.com/';
    writeTerminalPopOutRecord({
      id: 'navigated-pop-out',
      target: localTarget,
      state: 'closing',
      updatedAt: Date.now() - TERMINAL_POP_OUT_RELOAD_GRACE_PERIOD - 1,
      version: 3,
    });
    act(() => {
      window.dispatchEvent(new Event('focus'));
    });

    await waitFor(() => expect(createTerminalWebSocketMock).toHaveBeenCalledTimes(2));
    expect(screen.getByTestId('terminal-host')).not.toHaveAttribute('aria-disabled');

    popOutWindow.closed = true;
    openSpy.mockRestore();
  });

});
