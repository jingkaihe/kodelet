import { act, fireEvent, render, screen, waitFor, within } from '@testing-library/react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import apiService from '../../services/api';
import type { BrowserSession, WorkspaceTarget } from '../../types';
import BrowserPanel from './BrowserPanel';

type Command = { id: number; method: string; params: Record<string, unknown> };

class BrowserSocket extends EventTarget {
  readyState = 0;
  commands: Command[] = [];
  responses = new Map<string, Record<string, unknown>>();
  errors = new Map<string, string>();
  close = vi.fn(() => {
    this.readyState = 3;
  });
  send = (data: string) => {
    const request: Command = JSON.parse(data);
    this.commands.push(request);
    const result =
      this.responses.get(request.method) ||
      (request.method === 'Page.getFrameTree'
        ? { frameTree: { frame: { id: 'main-frame', url: 'http://localhost:1234' } } }
        : {});
    const error = this.errors.get(request.method);
    queueMicrotask(() => {
      if (this.readyState === 1)
        this.dispatchEvent(
          new MessageEvent('message', {
            data: JSON.stringify({
              id: request.id,
              ...(error ? { error: { message: error } } : { result }),
            }),
          })
        );
    });
  };
  event(method: string, params: Record<string, unknown>) {
    this.dispatchEvent(new MessageEvent('message', { data: JSON.stringify({ method, params }) }));
  }
  matching(method: string) {
    return this.commands.filter((request) => request.method === method);
  }
}

const target: WorkspaceTarget = { kind: 'runner', runnerId: 'runner-1', conversationId: 'conv-1' };
const session: BrowserSession = {
  id: 'handle-1',
  sessionId: 'chrome-1',
  cwd: '/workspace',
  devTools: false,
};
let sockets: BrowserSocket[];
let disconnectObserver: ReturnType<typeof vi.fn>;
let resizeViewport: () => void;

const connect = async (socket = sockets[0]) => {
  await act(async () => {
    socket.readyState = 1;
    socket.dispatchEvent(new Event('open'));
  });
  await screen.findByText('Live · Runner browser');
  return socket;
};

const open = async () => {
  const result = render(<BrowserPanel target={target} />);
  await waitFor(() => expect(sockets).toHaveLength(1));
  const socket = await connect();
  return { ...result, socket };
};

const showFrame = async (
  socket: BrowserSocket,
  sessionId = 1,
  data = 'YQ==',
  metadata = { deviceWidth: 800, deviceHeight: 600 }
) => {
  await act(async () =>
    socket.event('Page.screencastFrame', {
      sessionId,
      data,
      metadata,
    })
  );
  return screen.getByRole('img', { name: 'Runner browser page' });
};

beforeEach(() => {
  sockets = [];
  disconnectObserver = vi.fn();
  vi.stubGlobal('PointerEvent', MouseEvent);
  vi.stubGlobal(
    'ResizeObserver',
    class {
      constructor(callback: () => void) {
        resizeViewport = callback;
      }
      observe = vi.fn();
      disconnect = disconnectObserver;
    }
  );
  vi.spyOn(HTMLElement.prototype, 'clientWidth', 'get').mockReturnValue(400);
  vi.spyOn(HTMLElement.prototype, 'clientHeight', 'get').mockReturnValue(300);
  vi.spyOn(HTMLImageElement.prototype, 'complete', 'get').mockReturnValue(true);
  vi.spyOn(HTMLImageElement.prototype, 'naturalWidth', 'get').mockReturnValue(800);
  vi.spyOn(HTMLImageElement.prototype, 'naturalHeight', 'get').mockReturnValue(600);
  vi.spyOn(HTMLElement.prototype, 'getBoundingClientRect').mockReturnValue({
    x: 10,
    y: 20,
    left: 10,
    top: 20,
    right: 410,
    bottom: 320,
    width: 400,
    height: 300,
    toJSON: () => ({}),
  });
  vi.spyOn(apiService, 'openBrowserSession').mockResolvedValue(session);
  vi.spyOn(apiService, 'createBrowserWebSocket').mockImplementation(() => {
    const socket = new BrowserSocket();
    sockets.push(socket);
    return socket as unknown as WebSocket;
  });
  vi.spyOn(apiService, 'stopBrowserSession').mockResolvedValue(undefined);
});

afterEach(() => {
  vi.restoreAllMocks();
  vi.unstubAllGlobals();
});

describe('BrowserPanel', () => {
  it('opens the target with debugging and an uncapped CSS-pixel viewport', async () => {
    vi.spyOn(HTMLElement.prototype, 'clientWidth', 'get').mockReturnValue(2300);
    vi.spyOn(HTMLElement.prototype, 'clientHeight', 'get').mockReturnValue(1800);
    const { socket } = await open();
    expect(apiService.openBrowserSession).toHaveBeenCalledWith(target, expect.any(AbortSignal));
    expect(apiService.createBrowserWebSocket).toHaveBeenCalledWith('handle-1');
    expect(socket.commands.map((entry) => entry.method)).toEqual(
      expect.arrayContaining([
        'Page.enable',
        'Runtime.enable',
        'Network.enable',
        'Page.startScreencast',
      ])
    );
    expect(screen.getByLabelText('Browser address')).toHaveValue('http://localhost:1234');
    await waitFor(() =>
      expect(socket.matching('Emulation.setDeviceMetricsOverride')).toEqual([
        expect.objectContaining({
          params: { width: 2300, height: 1800, deviceScaleFactor: 1, mobile: false },
        }),
      ])
    );
    expect(socket.matching('Page.startScreencast')[0].params).toMatchObject({
      maxWidth: 1920,
      maxHeight: 1440,
    });
  });

  it('acknowledges rendered frames and bounds the waiting frame queue', async () => {
    const { socket } = await open();
    const image = await showFrame(socket);
    expect(socket.matching('Page.screencastFrameAck')).toHaveLength(0);
    await showFrame(socket, 2, 'Yg==');
    await showFrame(socket, 3, 'Yw==');
    expect(socket.matching('Page.screencastFrameAck').map((item) => item.params.sessionId)).toEqual(
      [2]
    );
    fireEvent.load(image);
    expect(socket.matching('Page.screencastFrameAck').map((item) => item.params.sessionId)).toEqual(
      [2, 1]
    );
    const latest = screen.getByRole('img', { name: 'Runner browser page' });
    expect(latest).toHaveAttribute('src', 'data:image/jpeg;base64,Yw==');
    fireEvent.load(latest);
    expect(socket.matching('Page.screencastFrameAck').map((item) => item.params.sessionId)).toEqual(
      [2, 1, 3]
    );
  });

  it('debounces panel resizing and ignores hidden viewport measurements', async () => {
    const width = vi.spyOn(HTMLElement.prototype, 'clientWidth', 'get');
    const height = vi.spyOn(HTMLElement.prototype, 'clientHeight', 'get');
    const { socket, unmount } = await open();
    await waitFor(() =>
      expect(socket.matching('Emulation.setDeviceMetricsOverride')).toHaveLength(1)
    );
    width.mockReturnValue(500);
    resizeViewport();
    width.mockReturnValue(700);
    height.mockReturnValue(1500);
    resizeViewport();
    await waitFor(() =>
      expect(socket.matching('Emulation.setDeviceMetricsOverride')).toHaveLength(2)
    );
    expect(socket.matching('Emulation.setDeviceMetricsOverride')[1].params).toMatchObject({
      width: 700,
      height: 1500,
    });
    vi.useFakeTimers();
    try {
      width.mockReturnValue(0);
      resizeViewport();
      await act(async () => vi.advanceTimersByTime(100));
      expect(socket.matching('Emulation.setDeviceMetricsOverride')).toHaveLength(2);
      width.mockReturnValue(800);
      resizeViewport();
      unmount();
      await act(async () => vi.advanceTimersByTime(100));
      expect(socket.matching('Emulation.setDeviceMetricsOverride')).toHaveLength(2);
    } finally {
      vi.useRealTimers();
    }
  });

  it('resynchronizes the capture surface after main-frame navigation even at the same size', async () => {
    const { socket } = await open();
    await waitFor(() =>
      expect(socket.matching('Emulation.setDeviceMetricsOverride')).toHaveLength(1)
    );
    vi.useFakeTimers();
    try {
      await act(async () => {
        socket.event('Page.frameNavigated', {
          frame: { id: 'child', parentId: 'main-frame', url: 'http://localhost:1234/frame' },
        });
        vi.advanceTimersByTime(100);
      });
      expect(socket.matching('Emulation.setDeviceMetricsOverride')).toHaveLength(1);
      await act(async () => {
        socket.event('Page.frameNavigated', {
          frame: { id: 'main-frame', url: 'http://localhost:1234/next' },
        });
        vi.advanceTimersByTime(100);
      });
      expect(socket.matching('Emulation.setDeviceMetricsOverride')).toHaveLength(2);
      await act(async () => {
        socket.event('Page.loadEventFired', {});
        vi.advanceTimersByTime(100);
      });
      expect(
        socket.matching('Emulation.setDeviceMetricsOverride').map(({ params }) => params)
      ).toEqual(Array(3).fill({ width: 400, height: 300, deviceScaleFactor: 1, mobile: false }));
    } finally {
      vi.useRealTimers();
    }
  });

  it.each([
    {
      width: 800,
      height: 400,
      clientX: 110,
      clientY: 120,
      x: 200,
      y: 100,
      marginX: 110,
      marginY: 30,
    },
    {
      width: 400,
      height: 800,
      clientX: 172.5,
      clientY: 95,
      x: 100,
      y: 200,
      marginX: 20,
      marginY: 95,
    },
  ])('maps a contained $width × $height frame and ignores its margins', async ({
    width,
    height,
    clientX,
    clientY,
    x,
    y,
    marginX,
    marginY,
  }) => {
    vi.spyOn(HTMLImageElement.prototype, 'naturalWidth', 'get').mockReturnValue(width / 2);
    vi.spyOn(HTMLImageElement.prototype, 'naturalHeight', 'get').mockReturnValue(height / 2);
    const { socket } = await open();
    fireEvent.load(
      await showFrame(socket, 1, 'YQ==', { deviceWidth: width, deviceHeight: height })
    );
    const input = screen.getByLabelText('Remote browser input');
    fireEvent.pointerDown(input, { clientX: marginX, clientY: marginY, button: 0, buttons: 1 });
    fireEvent.pointerUp(input, { clientX: marginX, clientY: marginY, button: 0 });
    fireEvent.wheel(input, { clientX: marginX, clientY: marginY, deltaY: 10 });
    expect(socket.matching('Input.dispatchMouseEvent')).toHaveLength(0);
    fireEvent.pointerDown(input, { clientX, clientY, button: 0, buttons: 1 });
    fireEvent.pointerUp(input, { clientX, clientY, button: 0 });
    expect(socket.matching('Input.dispatchMouseEvent').map((item) => item.params)).toEqual([
      expect.objectContaining({ type: 'mousePressed', x, y }),
      expect.objectContaining({ type: 'mouseReleased', x, y }),
    ]);
  });

  it('releases a captured pointer dragged outside the displayed frame', async () => {
    const { socket } = await open();
    fireEvent.load(await showFrame(socket));
    const input = screen.getByLabelText('Remote browser input');
    Object.defineProperty(input, 'hasPointerCapture', { value: () => true });
    fireEvent.pointerUp(input, { clientX: 1000, clientY: 1000, button: 0 });
    expect(socket.matching('Input.dispatchMouseEvent')[0].params).toMatchObject({
      type: 'mouseReleased',
      x: 799,
      y: 599,
    });
  });

  it('waits for a decoded frame, then scales mouse and wheel input without scrolling the portal', async () => {
    const { socket } = await open();
    const decoded = vi.spyOn(HTMLImageElement.prototype, 'complete', 'get').mockReturnValue(false);
    const image = await showFrame(socket);
    const input = screen.getByLabelText('Remote browser input');
    fireEvent.pointerDown(input, { clientX: 110, clientY: 95, button: 0, buttons: 1 });
    expect(socket.matching('Input.dispatchMouseEvent')).toHaveLength(0);
    decoded.mockReturnValue(true);
    fireEvent.load(image);
    fireEvent.pointerDown(input, { clientX: 110, clientY: 95, button: 0, buttons: 1 });
    fireEvent.pointerUp(input, { clientX: 110, clientY: 95, button: 0, buttons: 0 });
    const wheel = new WheelEvent('wheel', {
      clientX: 110,
      clientY: 95,
      deltaY: 2,
      deltaMode: 1,
      bubbles: true,
      cancelable: true,
    });
    await act(async () => input.dispatchEvent(wheel));
    expect(wheel.defaultPrevented).toBe(true);
    const mouse = socket.matching('Input.dispatchMouseEvent').map((item) => item.params);
    expect(mouse).toEqual([
      expect.objectContaining({ type: 'mousePressed', x: 200, y: 150, button: 'left', buttons: 1 }),
      expect.objectContaining({
        type: 'mouseReleased',
        x: 200,
        y: 150,
        button: 'left',
        buttons: 0,
      }),
      expect.objectContaining({ type: 'mouseWheel', x: 200, y: 150, deltaY: 32 }),
    ]);
  });

  it('confines keyboard forwarding to the viewport and provides an F6 escape', async () => {
    const { socket } = await open();
    const input = screen.getByLabelText('Remote browser input');
    const address = screen.getByLabelText('Browser address');
    fireEvent.keyDown(address, { key: 'x', code: 'KeyX' });
    expect(socket.matching('Input.dispatchKeyEvent')).toHaveLength(0);
    input.focus();
    fireEvent.keyDown(input, { key: 'a', code: 'KeyA', keyCode: 65 });
    fireEvent.keyUp(input, { key: 'a', code: 'KeyA', keyCode: 65 });
    expect(socket.matching('Input.dispatchKeyEvent').map((item) => item.params)).toEqual([
      expect.objectContaining({ type: 'keyDown', key: 'a', text: 'a', windowsVirtualKeyCode: 65 }),
      expect.objectContaining({ type: 'keyUp', key: 'a' }),
    ]);
    fireEvent.keyDown(input, { key: 'F6', code: 'F6' });
    expect(address).toHaveFocus();
    expect(socket.matching('Input.dispatchKeyEvent')).toHaveLength(2);
  });

  it('forwards IME composition and pasted text once', async () => {
    const { socket } = await open();
    const input = screen.getByLabelText('Remote browser input');
    fireEvent.compositionStart(input);
    fireEvent.keyDown(input, { key: 'Process', isComposing: true });
    fireEvent.input(input, { target: { value: '文' }, inputType: 'insertCompositionText' });
    expect(socket.matching('Input.insertText')).toHaveLength(0);
    fireEvent.compositionEnd(input, { data: '文字' });
    fireEvent.input(input, { target: { value: '文字' }, inputType: 'insertFromComposition' });
    fireEvent.paste(input, { clipboardData: { getData: () => 'pasted text' } });
    expect(socket.matching('Input.insertText').map((item) => item.params.text)).toEqual([
      '文字',
      'pasted text',
    ]);
  });

  it('navigates on the runner, displays navigation errors, and supports history/reload', async () => {
    const { socket } = await open();
    socket.responses.set('Page.navigate', { errorText: 'net::ERR_CONNECTION_REFUSED' });
    fireEvent.change(screen.getByLabelText('Browser address'), {
      target: { value: 'localhost:4567/abc' },
    });
    fireEvent.click(screen.getByRole('button', { name: 'Go' }));
    expect(await screen.findByRole('alert')).toHaveTextContent('net::ERR_CONNECTION_REFUSED');
    expect(socket.matching('Page.navigate')[0].params).toEqual({
      url: 'http://localhost:4567/abc',
    });
    socket.responses.set('Page.getNavigationHistory', {
      currentIndex: 1,
      entries: [{ id: 10 }, { id: 20 }, { id: 30 }],
    });
    fireEvent.click(screen.getByRole('button', { name: 'Back' }));
    await waitFor(() =>
      expect(socket.matching('Page.navigateToHistoryEntry')[0]?.params).toEqual({ entryId: 10 })
    );
    fireEvent.click(screen.getByRole('button', { name: 'Forward' }));
    await waitFor(() =>
      expect(socket.matching('Page.navigateToHistoryEntry')[1]?.params).toEqual({ entryId: 30 })
    );
    fireEvent.click(screen.getByRole('button', { name: 'Reload page' }));
    expect(socket.matching('Page.reload')).toHaveLength(1);
    fireEvent.change(screen.getByLabelText('Browser address'), {
      target: { value: 'javascript:alert(1)' },
    });
    fireEvent.click(screen.getByRole('button', { name: 'Go' }));
    expect(screen.getByRole('alert')).toHaveTextContent('Use an HTTP or HTTPS address');
    expect(socket.matching('Page.navigate')).toHaveLength(1);
  });

  it.each([
    'alert',
    'confirm',
    'beforeunload',
  ])('shows a shared %s dialog safely without stealing focus or answering automatically', async (type) => {
    const { socket } = await open();
    const address = screen.getByLabelText('Browser address');
    address.focus();
    const message = '<img src=x onerror=alert(1)> Continue?';
    await act(async () => socket.event('Page.javascriptDialogOpening', { type, message }));
    const dialog = screen.getByRole('dialog', { name: 'Page JavaScript dialog' });
    expect(dialog).toHaveAccessibleDescription(message);
    expect(within(dialog).queryByRole('img')).not.toBeInTheDocument();
    expect(within(dialog).queryByRole('textbox')).not.toBeInTheDocument();
    expect(address).toHaveFocus();
    expect(socket.matching('Page.handleJavaScriptDialog')).toHaveLength(0);
    fireEvent.click(within(dialog).getByRole('button', { name: 'Accept' }));
    await waitFor(() => expect(screen.queryByRole('dialog')).not.toBeInTheDocument());
    expect(socket.matching('Page.handleJavaScriptDialog')[0].params).toEqual({ accept: true });
  });

  it.each([true, false])('handles a prompt explicitly with accept=%s', async (accept) => {
    const { socket } = await open();
    await act(async () =>
      socket.event('Page.javascriptDialogOpening', {
        type: 'prompt',
        message: 'Name?',
        defaultPrompt: 'Default name',
      })
    );
    const input = screen.getByLabelText('Dialog prompt text');
    expect(input).toHaveValue('Default name');
    fireEvent.keyDown(input, { key: 'a', code: 'KeyA' });
    fireEvent.change(input, { target: { value: 'New name' } });
    expect(socket.matching('Input.dispatchKeyEvent')).toHaveLength(0);
    expect(socket.matching('Input.insertText')).toHaveLength(0);
    fireEvent.click(screen.getByRole('button', { name: accept ? 'Accept' : 'Dismiss' }));
    await waitFor(() => expect(screen.queryByRole('dialog')).not.toBeInTheDocument());
    expect(socket.matching('Page.handleJavaScriptDialog')[0].params).toEqual(
      accept ? { accept: true, promptText: 'New name' } : { accept: false }
    );
  });

  it('honors shared dialog closure and does not clear a newer dialog on a late reply', async () => {
    const { socket } = await open();
    await act(async () =>
      socket.event('Page.javascriptDialogOpening', { type: 'confirm', message: 'First dialog' })
    );
    vi.spyOn(socket, 'send').mockImplementationOnce((data) => {
      socket.commands.push(JSON.parse(data));
    });
    fireEvent.click(screen.getByRole('button', { name: 'Dismiss' }));
    expect(screen.getByRole('button', { name: 'Accept' })).toBeDisabled();
    expect(screen.getByRole('button', { name: 'Dismiss' })).toBeDisabled();
    const request = socket.matching('Page.handleJavaScriptDialog')[0];
    await act(async () => {
      socket.event('Page.javascriptDialogClosed', { result: false });
      socket.event('Page.javascriptDialogOpening', { type: 'alert', message: 'Next dialog' });
      socket.dispatchEvent(
        new MessageEvent('message', {
          data: JSON.stringify({ id: request.id, result: {} }),
        })
      );
    });
    expect(screen.getByRole('dialog')).toHaveTextContent('Next dialog');
    expect(screen.getByRole('button', { name: 'Accept' })).toBeEnabled();
    await act(async () => socket.event('Page.javascriptDialogClosed', { result: true }));
    expect(screen.queryByRole('dialog')).not.toBeInTheDocument();
    expect(socket.matching('Page.handleJavaScriptDialog')).toHaveLength(1);
  });

  it('shows dialog command errors and allows retry without silently dismissing', async () => {
    const { socket } = await open();
    await act(async () =>
      socket.event('Page.javascriptDialogOpening', { type: 'confirm', message: 'Continue?' })
    );
    socket.errors.set('Page.handleJavaScriptDialog', 'Could not handle dialog');
    fireEvent.click(screen.getByRole('button', { name: 'Dismiss' }));
    expect(await screen.findByRole('alert')).toHaveTextContent('Could not handle dialog');
    expect(screen.getByRole('dialog')).toBeInTheDocument();
    expect(screen.getByRole('button', { name: 'Dismiss' })).toBeEnabled();
    socket.errors.clear();
    fireEvent.click(screen.getByRole('button', { name: 'Dismiss' }));
    await waitFor(() => expect(screen.queryByRole('dialog')).not.toBeInTheDocument());
    expect(screen.queryByRole('alert')).not.toBeInTheDocument();
  });

  it('renders console text safely and evaluates in the remote page', async () => {
    const { socket } = await open();
    fireEvent.click(screen.getByRole('tab', { name: 'Console' }));
    const html = '<img src=x onerror=alert(1)>';
    await act(async () =>
      socket.event('Runtime.consoleAPICalled', { type: 'log', args: [{ value: html }] })
    );
    expect(screen.getByRole('log')).toHaveTextContent(html);
    expect(within(screen.getByRole('log')).queryByRole('img')).not.toBeInTheDocument();
    socket.responses.set('Runtime.evaluate', { result: { value: 42 } });
    fireEvent.change(screen.getByLabelText('Console expression'), { target: { value: '21 * 2' } });
    fireEvent.click(screen.getByRole('button', { name: 'Run' }));
    await waitFor(() => expect(screen.getByRole('log')).toHaveTextContent('42'));
    expect(socket.matching('Runtime.evaluate')[0].params).toMatchObject({
      expression: '21 * 2',
      timeout: 10000,
    });
    await waitFor(() => expect(socket.matching('Runtime.releaseObjectGroup')).toHaveLength(1));
    fireEvent.click(screen.getByRole('button', { name: 'Clear console' }));
    expect(screen.getByRole('log')).toBeEmptyDOMElement();
  });

  it('surfaces browser-side evaluation termination without disconnecting', async () => {
    const { socket } = await open();
    socket.errors.set('Runtime.evaluate', 'Execution was terminated');
    fireEvent.click(screen.getByRole('tab', { name: 'Console' }));
    fireEvent.change(screen.getByLabelText('Console expression'), {
      target: { value: 'while (true) {}' },
    });
    fireEvent.click(screen.getByRole('button', { name: 'Run' }));
    expect(await screen.findByRole('alert')).toHaveTextContent('Execution was terminated');
    expect(socket.matching('Runtime.evaluate')[0].params.timeout).toBe(10000);
    expect(socket.close).not.toHaveBeenCalled();
    expect(screen.getByRole('button', { name: 'Run' })).toBeEnabled();
  });

  it('updates the address for main-frame SPA navigation, not child frames', async () => {
    const { socket } = await open();
    await act(async () =>
      socket.event('Page.navigatedWithinDocument', {
        frameId: 'child-frame',
        url: 'http://localhost:1234/iframe',
      })
    );
    expect(screen.getByLabelText('Browser address')).toHaveValue('http://localhost:1234');
    await act(async () =>
      socket.event('Page.navigatedWithinDocument', {
        frameId: 'main-frame',
        url: 'http://localhost:1234/settings',
      })
    );
    expect(screen.getByLabelText('Browser address')).toHaveValue('http://localhost:1234/settings');
  });

  it('surfaces admission errors without opening a socket', async () => {
    vi.mocked(apiService.openBrowserSession).mockRejectedValue(
      new Error('Browser access is disabled on the control plane.')
    );
    render(<BrowserPanel target={target} />);
    expect(await screen.findByRole('alert')).toHaveTextContent('Browser access is disabled');
    expect(apiService.createBrowserWebSocket).not.toHaveBeenCalled();
    expect(screen.getByRole('button', { name: 'Reconnect' })).toBeInTheDocument();
  });

  it('keeps the connection available when stopping is rejected', async () => {
    const { socket } = await open();
    vi.mocked(apiService.stopBrowserSession).mockRejectedValue(new Error('Permission denied'));
    fireEvent.click(screen.getByRole('button', { name: 'Stop workspace browser' }));
    expect(await screen.findByRole('alert')).toHaveTextContent('Permission denied');
    expect(socket.close).not.toHaveBeenCalled();
    expect(screen.getByLabelText('Remote browser input')).toBeEnabled();
  });

  it('bounds network entries and displays responses and failures', async () => {
    const { socket } = await open();
    fireEvent.click(screen.getByRole('tab', { name: 'Network' }));
    await act(async () => {
      for (let i = 0; i < 205; i++)
        socket.event('Network.requestWillBeSent', {
          requestId: String(i),
          request: { method: 'GET', url: `http://localhost:1234/${i}` },
        });
      socket.event('Network.responseReceived', { requestId: '203', response: { status: 404 } });
      socket.event('Network.loadingFailed', { requestId: '204', errorText: 'net::ERR_FAILED' });
    });
    expect(screen.getAllByRole('row')).toHaveLength(201);
    expect(screen.queryByText('http://localhost:1234/0')).not.toBeInTheDocument();
    expect(screen.getByText('404')).toBeInTheDocument();
    expect(screen.getByText('net::ERR_FAILED')).toBeInTheDocument();
  });

  it('picks a read-only element snapshot without clicking the remote application', async () => {
    const { socket } = await open();
    fireEvent.load(await showFrame(socket));
    socket.responses.set('Runtime.evaluate', {
      result: { value: { html: '<button>Save</button>' } },
    });
    fireEvent.click(screen.getByRole('tab', { name: 'Inspect' }));
    fireEvent.click(screen.getByRole('button', { name: 'Pick element' }));
    const input = screen.getByLabelText('Remote browser input');
    fireEvent.pointerDown(input, { clientX: 110, clientY: 95, button: 0 });
    fireEvent.pointerUp(input, { clientX: 110, clientY: 95, button: 0 });
    await screen.findByText(/<button>Save<\/button>/);
    expect(socket.matching('Input.dispatchMouseEvent')).toHaveLength(0);
    expect(socket.matching('Runtime.evaluate')[0].params.expression).toContain(
      'document.elementFromPoint(200, 150)'
    );
    expect(socket.matching('Runtime.evaluate')[0].params.timeout).toBe(10000);
  });

  it('explains missing DevTools assets instead of loading third-party scripts', async () => {
    await open();
    fireEvent.click(screen.getByRole('tab', { name: 'DevTools' }));
    expect(
      screen.getByText(/requires compiled DevTools assets configured on the runner/)
    ).toBeInTheDocument();
    expect(screen.queryByTitle('Runner Chrome DevTools')).not.toBeInTheDocument();
  });

  it('embeds only the authenticated session DevTools endpoint when available', async () => {
    vi.mocked(apiService.openBrowserSession).mockResolvedValue({ ...session, devTools: true });
    await open();
    fireEvent.click(screen.getByRole('tab', { name: 'DevTools' }));
    expect(screen.getByTitle('Runner Chrome DevTools')).toHaveAttribute(
      'src',
      apiService.browserDevToolsURL(session.id)
    );
  });

  it('detaches without stopping the shared session and ignores late connection resolution', async () => {
    const { socket, unmount } = await open();
    await act(async () =>
      socket.event('Page.javascriptDialogOpening', { type: 'alert', message: 'Agent dialog' })
    );
    unmount();
    expect(socket.close).toHaveBeenCalledTimes(1);
    expect(socket.matching('Page.handleJavaScriptDialog')).toHaveLength(0);
    expect(disconnectObserver).toHaveBeenCalled();
    expect(apiService.stopBrowserSession).not.toHaveBeenCalled();

    let resolve: (value: BrowserSession) => void = () => {};
    vi.mocked(apiService.openBrowserSession).mockReturnValue(
      new Promise((done) => {
        resolve = done;
      })
    );
    const pending = render(<BrowserPanel target={target} />);
    pending.unmount();
    await act(async () => resolve(session));
    expect(sockets).toHaveLength(1);
    const calls = vi.mocked(apiService.openBrowserSession).mock.calls;
    const signal = calls[calls.length - 1]?.[1];
    expect(signal?.aborted).toBe(true);
  });

  it('reconnects explicitly after a dropped socket and stops only on request', async () => {
    const { socket } = await open();
    await act(async () =>
      socket.event('Page.javascriptDialogOpening', { type: 'alert', message: 'Agent dialog' })
    );
    await act(async () => socket.dispatchEvent(new Event('close')));
    expect(screen.queryByRole('dialog')).not.toBeInTheDocument();
    expect(socket.matching('Page.handleJavaScriptDialog')).toHaveLength(0);
    expect(screen.getByLabelText('Remote browser input')).toBeDisabled();
    expect(screen.getByRole('alert')).toHaveTextContent('Browser disconnected');
    expect(sockets).toHaveLength(1);
    fireEvent.click(screen.getByRole('button', { name: 'Reconnect' }));
    await waitFor(() => expect(sockets).toHaveLength(2));
    await connect(sockets[1]);
    expect(apiService.openBrowserSession).toHaveBeenCalledTimes(2);
    await act(async () =>
      sockets[1].event('Page.javascriptDialogOpening', { type: 'alert', message: 'Agent dialog' })
    );
    fireEvent.click(screen.getByRole('button', { name: 'Stop workspace browser' }));
    await screen.findByText('Browser stopped');
    expect(screen.queryByRole('dialog')).not.toBeInTheDocument();
    expect(apiService.stopBrowserSession).toHaveBeenCalledWith('handle-1');
    expect(sockets[1].close).toHaveBeenCalledTimes(1);
    expect(screen.getByRole('button', { name: 'Start browser' })).toBeInTheDocument();
    expect(screen.queryByRole('alert')).not.toBeInTheDocument();
  });
});
