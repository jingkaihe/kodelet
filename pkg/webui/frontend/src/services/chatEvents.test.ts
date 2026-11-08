import { afterEach, describe, expect, it, vi } from 'vitest';
import type { ChatStreamEvent, TurnReceipt } from '../types';
import { ChatEventSocket, EVENT_CONNECTION_HEADER, EVENT_SUBSCRIPTION_HEADER } from './chatEvents';

class FakeSocket {
  static instances: FakeSocket[] = [];
  readyState = 0;
  sent: Record<string, unknown>[] = [];
  onmessage: ((event: MessageEvent) => void) | null = null;
  onclose: (() => void) | null = null;
  onerror: (() => void) | null = null;

  constructor(readonly url: string) {
    FakeSocket.instances.push(this);
  }

  send(data: string) {
    this.sent.push(JSON.parse(data));
  }

  close() {
    if (this.readyState === 3) return;
    this.readyState = 3;
    this.onclose?.();
  }

  accept(connectionId: string) {
    this.readyState = 1;
    this.receive({ type: 'hello', connectionId });
  }

  receive(message: unknown) {
    this.onmessage?.({ data: JSON.stringify(message) } as MessageEvent);
  }

  event(id: string, event: ChatStreamEvent) {
    this.receive({ type: 'event', id, event });
  }
}

const latestSocket = () => FakeSocket.instances[FakeSocket.instances.length - 1];

const lastSent = (socket: FakeSocket) => socket.sent[socket.sent.length - 1];

const createSocket = (
  options: {
    getTurnReceipt?: (conversationId: string, turnId: string) => Promise<TurnReceipt>;
    reconnectDelaysMs?: number[];
  } = {}
) =>
  new ChatEventSocket({
    url: () => 'ws://kodelet.test/api/chat/events?clientId=page',
    createSocket: (url) => new FakeSocket(url) as unknown as WebSocket,
    getTurnReceipt:
      options.getTurnReceipt ?? (() => Promise.reject(new Error('no receipt in this test'))),
    reconnectDelaysMs: options.reconnectDelaysMs ?? [0],
    connectTimeoutMs: 1000,
  });

/** Connects a socket, answering its handshake with connectionId. */
const connected = async (events: ChatEventSocket, connectionId = 'connection-1') => {
  const connecting = events.connect();
  latestSocket().accept(connectionId);
  expect(await connecting).toBe(connectionId);
  return latestSocket();
};

const accepted = (turnId: string) =>
  new Response(
    JSON.stringify({ conversation_id: 'conversation', turn_id: turnId, accepted: true }),
    { status: 202, headers: { 'Content-Type': 'application/json' } }
  );

afterEach(() => {
  FakeSocket.instances = [];
  vi.useRealTimers();
});

describe('ChatEventSocket', () => {
  it('follows a conversation until the server ends it', async () => {
    const events = createSocket();
    const socket = await connected(events);
    const onEvent = vi.fn();
    const finished = events.watch('conversation', {
      level: 'lifecycle',
      capabilities: 'interactive',
      onEvent,
    });
    expect(socket.sent).toEqual([
      {
        type: 'subscribe',
        id: 's1',
        conversationId: 'conversation',
        level: 'lifecycle',
        capabilities: 'interactive',
      },
    ]);
    socket.receive({ type: 'subscribed', id: 's1', conversationId: 'conversation' });
    socket.event('s1', { kind: 'conversation', conversation_id: 'conversation', turn_id: 't1' });
    socket.event('s1', { kind: 'done', conversation_id: 'conversation', turn_id: 't1' });
    socket.receive({ type: 'closed', id: 's1', reason: 'ended' });
    await finished;
    expect(onEvent.mock.calls.map(([event]) => event.kind)).toEqual(['conversation', 'done']);
  });

  it('rejects a subscription the server refuses', async () => {
    const events = createSocket();
    const socket = await connected(events);
    const finished = events.watch('missing', { onEvent: vi.fn() });
    socket.receive({
      type: 'closed',
      id: 's1',
      reason: 'rejected',
      error: 'conversation not found',
    });
    await expect(finished).rejects.toThrow('conversation not found');
  });

  it('unsubscribes and rejects with an abort error when aborted', async () => {
    const events = createSocket();
    const socket = await connected(events);
    const controller = new AbortController();
    const onEvent = vi.fn();
    const finished = events.watch('conversation', { signal: controller.signal, onEvent });
    socket.event('s1', { kind: 'ui-input-request', ui_input: { id: 'prompt', title: 'Name?' } });
    controller.abort();
    await expect(finished).rejects.toMatchObject({ name: 'AbortError' });
    expect(lastSent(socket)).toEqual({ type: 'unsubscribe', id: 's1' });
    // As with a closed HTTP stream, the prompt can no longer be answered.
    expect(onEvent).toHaveBeenLastCalledWith({
      kind: 'ui-request-end',
      conversation_id: 'conversation',
      ui_request_id: 'prompt',
    });
  });

  it('submits a turn bound to the socket and resolves once the server completes it', async () => {
    const events = createSocket();
    const socket = await connected(events);
    const onEvent = vi.fn();
    const post = vi.fn(async () => {
      // Events can arrive before the request's response.
      socket.event('s1', { kind: 'conversation', conversation_id: 'conversation', turn_id: 't1' });
      return accepted('t1');
    });
    const finished = events.submit(
      { message: 'hello', conversationId: 'conversation', turnId: 't1' },
      { onEvent, post }
    );
    await vi.waitFor(() => expect(post).toHaveBeenCalledOnce());
    expect(post).toHaveBeenCalledWith(
      { message: 'hello', conversationId: 'conversation', turnId: 't1' },
      { [EVENT_CONNECTION_HEADER]: 'connection-1', [EVENT_SUBSCRIPTION_HEADER]: 's1' },
      undefined
    );
    socket.event('s1', { kind: 'text-delta', conversation_id: 'conversation', delta: 'Hi' });
    socket.event('s1', { kind: 'done', conversation_id: 'conversation', turn_id: 't1' });
    socket.receive({ type: 'closed', id: 's1', reason: 'completed' });
    await finished;
    expect(onEvent.mock.calls.map(([event]) => event.kind)).toEqual([
      'conversation',
      'text-delta',
      'done',
    ]);
    // The server ended the subscription; the request itself never subscribed.
    expect(socket.sent).toEqual([]);
  });

  it('gives each submission a turn ID and completes one that finished before its response', async () => {
    const events = createSocket();
    const socket = await connected(events);
    const onEvent = vi.fn();
    const post = vi.fn(async (request: { turnId?: string }) => {
      socket.event('s1', {
        kind: 'done',
        conversation_id: 'conversation',
        turn_id: request.turnId,
      });
      socket.receive({ type: 'closed', id: 's1', reason: 'completed' });
      return accepted(request.turnId || '');
    });
    await events.submit({ message: 'hello', conversationId: 'conversation' }, { onEvent, post });
    const [[request]] = post.mock.calls;
    expect(request.turnId).toEqual(expect.any(String));
    expect(onEvent).toHaveBeenCalledWith(expect.objectContaining({ turn_id: request.turnId }));
  });

  it('reports a refused submission with the server message', async () => {
    const events = createSocket();
    const socket = await connected(events);
    const post = vi.fn(
      async () =>
        new Response(JSON.stringify({ error: 'conversation is busy' }), {
          status: 409,
          headers: { 'Content-Type': 'application/json' },
        })
    );
    await expect(
      events.submit(
        { message: 'hello', conversationId: 'conversation', turnId: 't1' },
        { onEvent: vi.fn(), post }
      )
    ).rejects.toMatchObject({ message: 'conversation is busy', status: 409 });
    expect(socket.sent).toEqual([{ type: 'unsubscribe', id: 's1' }]);
  });

  it('reconnects and submits once more when the server no longer knows the connection', async () => {
    const events = createSocket();
    await connected(events);
    const post = vi.fn(async (_request: unknown, headers: Record<string, string>) => {
      if (headers[EVENT_CONNECTION_HEADER] === 'connection-1') {
        return new Response('{}', { status: 412 });
      }
      queueMicrotask(() => {
        latestSocket().receive({ type: 'closed', id: 's1', reason: 'completed' });
      });
      return accepted('t1');
    });
    const finished = events.submit(
      { message: 'hello', conversationId: 'conversation', turnId: 't1' },
      { onEvent: vi.fn(), post }
    );
    await vi.waitFor(() => expect(FakeSocket.instances).toHaveLength(2));
    latestSocket().accept('connection-2');
    await finished;
    expect(post.mock.calls.map(([, headers]) => headers[EVENT_CONNECTION_HEADER])).toEqual([
      'connection-1',
      'connection-2',
    ]);
  });

  it('reports a run that ended while disconnected from its saved status', async () => {
    const getTurnReceipt = vi.fn(async (conversationId: string, turnId: string) => ({
      conversationId,
      turnId,
      status: 'failed',
      error: 'provider failed',
    }));
    const events = createSocket({ getTurnReceipt });
    const socket = await connected(events);
    const onEvent = vi.fn();
    void events.watch('conversation', { onEvent });
    socket.event('s1', { kind: 'conversation', conversation_id: 'conversation', turn_id: 't1' });
    socket.event('s1', { kind: 'ui-confirm-request', ui_confirm: { id: 'prompt', title: 'OK?' } });

    socket.close();
    // The server dismissed the disconnected page's prompt.
    expect(onEvent).toHaveBeenLastCalledWith({
      kind: 'ui-request-end',
      conversation_id: 'conversation',
      ui_request_id: 'prompt',
    });
    await vi.waitFor(() => expect(FakeSocket.instances).toHaveLength(2));
    const reconnected = latestSocket();
    reconnected.accept('connection-2');
    expect(reconnected.sent).toEqual([
      { type: 'subscribe', id: 's1', conversationId: 'conversation', level: 'full' },
    ]);
    // Another run started meanwhile: its events wait behind the missed ending.
    reconnected.receive({
      type: 'subscribed',
      id: 's1',
      conversationId: 'conversation',
      active: true,
      turnId: 't2',
    });
    reconnected.event('s1', {
      kind: 'conversation',
      conversation_id: 'conversation',
      turn_id: 't2',
    });
    await vi.waitFor(() => expect(onEvent).toHaveBeenCalledTimes(5));
    expect(getTurnReceipt).toHaveBeenCalledWith('conversation', 't1');
    expect(onEvent.mock.calls.slice(3).map(([event]) => event)).toEqual([
      { kind: 'error', error: 'provider failed', conversation_id: 'conversation', turn_id: 't1' },
      { kind: 'conversation', conversation_id: 'conversation', turn_id: 't2' },
    ]);
  });

  it('follows a submitted turn after reconnecting and completes when it ends', async () => {
    const events = createSocket();
    const socket = await connected(events);
    const onEvent = vi.fn();
    const finished = events.submit(
      { message: 'hello', conversationId: 'conversation', turnId: 't1' },
      { onEvent, post: async () => accepted('t1') }
    );
    socket.event('s1', { kind: 'conversation', conversation_id: 'conversation', turn_id: 't1' });
    await vi.waitFor(() => expect(onEvent).toHaveBeenCalledOnce());

    socket.close();
    await vi.waitFor(() => expect(FakeSocket.instances).toHaveLength(2));
    const reconnected = latestSocket();
    reconnected.accept('connection-2');
    reconnected.receive({
      type: 'subscribed',
      id: 's1',
      conversationId: 'conversation',
      active: true,
      turnId: 't1',
    });
    reconnected.event('s1', {
      kind: 'conversation',
      conversation_id: 'conversation',
      turn_id: 't1',
    });
    reconnected.event('s1', { kind: 'done', conversation_id: 'conversation', turn_id: 't1' });
    await finished;
    expect(lastSent(reconnected)).toEqual({ type: 'unsubscribe', id: 's1' });
  });

  it('follows again after falling behind', async () => {
    const getTurnReceipt = vi.fn(async (conversationId: string, turnId: string) => ({
      conversationId,
      turnId,
      status: 'succeeded',
    }));
    const events = createSocket({ getTurnReceipt });
    const socket = await connected(events);
    const onEvent = vi.fn();
    void events.watch('conversation', { onEvent });
    socket.event('s1', { kind: 'conversation', conversation_id: 'conversation', turn_id: 't1' });
    socket.receive({ type: 'closed', id: 's1', reason: 'overflow' });
    expect(lastSent(socket)).toEqual({
      type: 'subscribe',
      id: 's1',
      conversationId: 'conversation',
      level: 'full',
    });
    socket.receive({ type: 'subscribed', id: 's1', conversationId: 'conversation' });
    await vi.waitFor(() =>
      expect(onEvent).toHaveBeenLastCalledWith({
        kind: 'done',
        conversation_id: 'conversation',
        turn_id: 't1',
      })
    );
  });

  it('reports the socket unavailable when it closes before its handshake', async () => {
    const events = createSocket();
    const connecting = events.connect();
    latestSocket().close();
    expect(await connecting).toBeNull();
    // The page streams over HTTP for a while rather than retrying every request.
    expect(await events.connect()).toBeNull();
    expect(FakeSocket.instances).toHaveLength(1);
  });

  it('rejects followers once reconnecting fails', async () => {
    const events = createSocket({ reconnectDelaysMs: [0, 0] });
    const socket = await connected(events);
    const finished = events.watch('conversation', { onEvent: vi.fn() });
    socket.close();
    await vi.waitFor(() => expect(FakeSocket.instances).toHaveLength(2));
    latestSocket().close();
    await vi.waitFor(() => expect(FakeSocket.instances).toHaveLength(3));
    latestSocket().close();
    await expect(finished).rejects.toThrow('Lost connection to the server');
  });

  it('delivers live voice events until the call ends, and ends the call on disconnect', async () => {
    const events = createSocket();
    const socket = await connected(events);
    const onEvent = vi.fn();
    const finished = events.attachRealtime('call-1', { onEvent });
    expect(socket.sent).toEqual([{ type: 'realtime', id: 's1', callId: 'call-1' }]);
    socket.receive({ type: 'realtime', id: 's1', event: { kind: 'state', text: 'connected' } });
    socket.receive({ type: 'realtime', id: 's1', event: { kind: 'closed' } });
    socket.receive({ type: 'closed', id: 's1', reason: 'completed' });
    await finished;
    expect(onEvent.mock.calls.map(([event]) => event.kind)).toEqual(['state', 'closed']);

    const dropped = events.attachRealtime('call-2', { onEvent: vi.fn() });
    socket.close();
    await expect(dropped).rejects.toThrow('The call disconnected');
  });

  it('reports an already submitted turn from its saved status', async () => {
    const events = createSocket();
    const socket = await connected(events);
    const onEvent = vi.fn();
    await events.submit(
      { message: 'hello', conversationId: 'conversation', turnId: 't1' },
      {
        onEvent,
        post: async () =>
          new Response(
            JSON.stringify({
              conversation_id: 'conversation',
              turn_id: 't1',
              accepted: false,
              receipt: { conversationId: 'conversation', turnId: 't1', status: 'cancelled' },
            }),
            { status: 200 }
          ),
      }
    );
    expect(onEvent.mock.calls.map(([event]) => event)).toEqual([
      { kind: 'conversation', conversation_id: 'conversation', turn_id: 't1' },
      { kind: 'done', cancelled: true, conversation_id: 'conversation', turn_id: 't1' },
    ]);
    expect(socket.sent).toEqual([{ type: 'unsubscribe', id: 's1' }]);
  });
});
