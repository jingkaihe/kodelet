import { act, render, screen, waitFor } from '@testing-library/react';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import TerminalModal from './TerminalModal';

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

  private listeners = new Map<string, Array<(event?: any) => void>>();

  addEventListener(type: string, listener: (event?: any) => void) {
    const existing = this.listeners.get(type) ?? [];
    existing.push(listener);
    this.listeners.set(type, existing);
  }

  emit(type: string, event?: any) {
    for (const listener of this.listeners.get(type) ?? []) {
      listener(event);
    }
  }
}

describe('TerminalModal', () => {
  beforeEach(() => {
    MockTerminal.instances = [];
    createTerminalWebSocketMock.mockReset();
  });

  it('suppresses parser-generated input until replay completes', async () => {
    const socket = new MockWebSocket();
    createTerminalWebSocketMock.mockReturnValue(socket);

    render(<TerminalModal cwdLabel="/tmp/project" onClose={vi.fn()} open />);

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

    render(<TerminalModal cwdLabel="/tmp/project" onClose={vi.fn()} open />);

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

    render(<TerminalModal cwdLabel="/tmp/project" onClose={vi.fn()} open />);

    await waitFor(() => expect(MockTerminal.instances[0]).toBeDefined());
    const terminal = MockTerminal.instances[0];
    expect(terminal.resize).toHaveBeenCalledWith(80, 23);
  });

  it('allows ghostty-web to process terminal keystrokes', async () => {
    const socket = new MockWebSocket();
    createTerminalWebSocketMock.mockReturnValue(socket);

    render(<TerminalModal cwdLabel="/tmp/project" onClose={vi.fn()} open />);

    await waitFor(() => expect(MockTerminal.instances[0]).toBeDefined());
    const terminal = MockTerminal.instances[0];
    act(() => {
      socket.emit('message', { data: JSON.stringify({ type: 'replay-complete' }) });
    });

    expect(terminal.handleKey(new KeyboardEvent('keydown', { key: 'a' }))).toBe(false);
  });

  it('does not close when Escape is pressed inside the terminal', async () => {
    const socket = new MockWebSocket();
    const onClose = vi.fn();
    createTerminalWebSocketMock.mockReturnValue(socket);

    render(<TerminalModal cwdLabel="/tmp/project" onClose={onClose} open />);

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

    render(<TerminalModal cwdLabel="/tmp/project" onClose={onClose} open />);

    await waitFor(() => expect(MockTerminal.instances[0]).toBeDefined());
    window.dispatchEvent(new KeyboardEvent('keydown', { key: 'Escape' }));

    expect(onClose).toHaveBeenCalledTimes(1);
  });

  it('reports wheel events to mouse-tracking terminal apps', async () => {
    const socket = new MockWebSocket();
    createTerminalWebSocketMock.mockReturnValue(socket);

    render(<TerminalModal cwdLabel="/tmp/project" onClose={vi.fn()} open />);

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

    render(<TerminalModal cwdLabel="/tmp/project" onClose={vi.fn()} open />);

    await waitFor(() => expect(MockTerminal.instances[0]).toBeDefined());
    const terminal = MockTerminal.instances[0];
    terminal.wasmTerm.isAlternateScreen.mockReturnValue(false);

    expect(terminal.handleWheel(new WheelEvent('wheel', { deltaY: 10 }))).toBe(false);
  });

  it('renders a simplified header', async () => {
    const socket = new MockWebSocket();
    createTerminalWebSocketMock.mockReturnValue(socket);

    render(<TerminalModal cwdLabel="/tmp/project" onClose={vi.fn()} open />);

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

    render(<TerminalModal cwdLabel="/tmp/project" onClose={vi.fn()} open />);

    await waitFor(() => expect(MockTerminal.instances[0]).toBeDefined());
    screen.getByRole('button', { name: 'Open terminal in new window' }).click();

    expect(openSpy).toHaveBeenCalledWith(
      'http://localhost:3000/terminal?cwd=%2Ftmp%2Fproject',
      'kodelet-terminal',
      'popup=yes,width=1120,height=760,resizable=yes,scrollbars=no'
    );

    openSpy.mockRestore();
  });

});
