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
    hasSelection = vi.fn(() => false);

    private dataHandler?: MockDataHandler;
    private resizeHandler?: MockResizeHandler;
    private customKeyEventHandler?: (event: KeyboardEvent) => boolean;

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

});
