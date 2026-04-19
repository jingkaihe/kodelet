import { act, fireEvent, render, screen } from '@testing-library/react';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import TerminalModal from './TerminalModal';

const { MockFitAddon, MockTerminal, createTerminalWebSocketMock } = vi.hoisted(() => {
  class HoistedMockFitAddon {
    fit = vi.fn();
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
    dispose = vi.fn();
    attachCustomKeyEventHandler = vi.fn(() => true);
    hasSelection = vi.fn(() => false);

    private dataHandler?: MockDataHandler;
    private resizeHandler?: MockResizeHandler;

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
  }

  return {
    MockFitAddon: HoistedMockFitAddon,
    MockTerminal: HoistedMockTerminal,
    createTerminalWebSocketMock: vi.fn(),
  };
});

vi.mock('../../services/api', () => ({
  default: {
    createTerminalWebSocket: createTerminalWebSocketMock,
  },
}));

vi.mock('@xterm/addon-fit', () => ({
  FitAddon: MockFitAddon,
}));

vi.mock('@xterm/xterm', () => ({
  Terminal: MockTerminal,
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

  it('suppresses parser-generated input until replay completes', () => {
    const socket = new MockWebSocket();
    createTerminalWebSocketMock.mockReturnValue(socket);

    render(<TerminalModal cwdLabel="/tmp/project" onClose={vi.fn()} open />);

    const terminal = MockTerminal.instances[0];
    expect(terminal).toBeDefined();

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

  it('renders a simplified header', () => {
    const socket = new MockWebSocket();
    createTerminalWebSocketMock.mockReturnValue(socket);

    render(<TerminalModal cwdLabel="/tmp/project" onClose={vi.fn()} open />);

    expect(screen.getByRole('heading', { name: 'Terminal' })).toBeInTheDocument();
    expect(screen.getByText('/tmp/project')).toBeInTheDocument();
    expect(screen.queryByText('Workspace')).not.toBeInTheDocument();
    expect(screen.queryByText('shell')).not.toBeInTheDocument();
  });

  it('closes from the header button', () => {
    const socket = new MockWebSocket();
    const onClose = vi.fn();
    createTerminalWebSocketMock.mockReturnValue(socket);

    render(<TerminalModal cwdLabel="/tmp/project" onClose={onClose} open />);

    fireEvent.click(screen.getByRole('button', { name: 'Close' }));

    expect(onClose).toHaveBeenCalledTimes(1);
  });
});
