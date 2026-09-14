import { afterEach, describe, expect, it, vi } from 'vitest';
import { BrowserCDP } from './browser';

const connection = () => {
  const socket = Object.assign(new EventTarget(), { readyState: 1, send: vi.fn(), close: vi.fn() });
  const handlers = { onOpen: vi.fn(), onEvent: vi.fn(), onClose: vi.fn() };
  const client = new BrowserCDP(socket as unknown as WebSocket, handlers);
  socket.dispatchEvent(new Event('open'));
  const receive = (payload: unknown) =>
    socket.dispatchEvent(new MessageEvent('message', { data: JSON.stringify(payload) }));
  return { socket, client, handlers, receive };
};

afterEach(() => vi.useRealTimers());

describe('BrowserCDP', () => {
  it('correlates out-of-order responses independently of browser events', async () => {
    const { client, socket, handlers, receive } = connection();
    const first = client.request('Page.enable');
    const second = client.request('Runtime.enable');
    expect(socket.send).toHaveBeenNthCalledWith(
      1,
      JSON.stringify({ id: 1, method: 'Page.enable', params: {} })
    );
    receive({ method: 'Runtime.consoleAPICalled', params: { type: 'log' } });
    receive({ id: 2, result: { enabled: true } });
    receive({ id: 1, result: {} });
    expect(await second).toEqual({ enabled: true });
    expect(await first).toEqual({});
    expect(handlers.onEvent).toHaveBeenCalledWith('Runtime.consoleAPICalled', { type: 'log' });
    client.close();
  });

  it('rejects pending commands and ignores late events after disconnect', async () => {
    const { client, socket, handlers, receive } = connection();
    const rejected = expect(client.request('Runtime.evaluate')).rejects.toThrow(
      'Browser disconnected'
    );
    socket.dispatchEvent(new Event('close'));
    await rejected;
    receive({ method: 'Page.frameNavigated', params: {} });
    expect(handlers.onEvent).not.toHaveBeenCalled();
    expect(socket.close).toHaveBeenCalledTimes(1);
    client.close();
    expect(socket.close).toHaveBeenCalledTimes(1);
    await expect(client.request('Page.reload')).rejects.toThrow('disconnected');
  });

  it('reports CDP errors without closing the session', async () => {
    const { client, receive, socket } = connection();
    const rejected = expect(client.request('Bad.command')).rejects.toThrow('Unknown method');
    receive({ id: 1, error: { code: -32601, message: 'Unknown method' } });
    await rejected;
    expect(socket.close).not.toHaveBeenCalled();
    client.close();
  });

  it('bounds command waits and releases timers on close', async () => {
    vi.useFakeTimers();
    const { client } = connection();
    const rejected = expect(client.request('Runtime.evaluate')).rejects.toThrow('timed out');
    await vi.advanceTimersByTimeAsync(15000);
    await rejected;
    client.close();
    expect(vi.getTimerCount()).toBe(0);
  });

  it('closes malformed and failed connections', () => {
    const { socket, handlers } = connection();
    socket.dispatchEvent(new MessageEvent('message', { data: 'not json' }));
    expect(handlers.onClose).toHaveBeenCalledWith(
      expect.stringContaining('Invalid browser message')
    );
    expect(socket.close).toHaveBeenCalledTimes(1);
    socket.dispatchEvent(new Event('error'));
    expect(handlers.onClose).toHaveBeenCalledTimes(1);
  });

  it('times out a connection that never opens', async () => {
    vi.useFakeTimers();
    const socket = Object.assign(new EventTarget(), {
      readyState: 0,
      send: vi.fn(),
      close: vi.fn(),
    });
    const handlers = { onOpen: vi.fn(), onEvent: vi.fn(), onClose: vi.fn() };
    new BrowserCDP(socket as unknown as WebSocket, handlers);
    await vi.advanceTimersByTimeAsync(20000);
    expect(socket.close).toHaveBeenCalledTimes(1);
    expect(handlers.onClose).toHaveBeenCalledWith(expect.stringContaining('timed out'));
  });
});
