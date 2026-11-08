import type {
  ChatRequest,
  ChatStreamEvent,
  ChatSubmissionResponse,
  RealtimeEvent,
  TurnReceipt,
} from '../types';
import { type ChatEventDelivery, createChatEventDelivery } from './chatEventDelivery';

/**
 * One WebSocket carries the events of every conversation a page follows.
 *
 * Browsers allow about six HTTP/1.1 connections per origin, shared by all tabs,
 * and a streaming response holds one until it ends. Following each running
 * conversation, or submitting each run, over its own response could exhaust
 * them, queueing the requests that answer prompts or stop runs. Established
 * WebSockets do not use that pool, so the page follows conversations here and
 * keeps commands as short HTTP requests.
 */

/** A lifecycle subscription receives run start and end, resource reloads and prompts only. */
export type ChatEventLevel = 'full' | 'lifecycle';

/** A conversation's state when a subscription to it is established again. */
export interface ChatEventResubscription {
  /** Whether a run is active. */
  active: boolean;
  turnId?: string;
}

export const EVENT_CONNECTION_HEADER = 'X-Kodelet-Event-Connection';
export const EVENT_SUBSCRIPTION_HEADER = 'X-Kodelet-Event-Subscription';

type ServerMessage =
  | { type: 'hello'; connectionId: string }
  | { type: 'subscribed'; id: string; conversationId: string; active?: boolean; turnId?: string }
  | { type: 'event'; id: string; event: ChatStreamEvent }
  | { type: 'realtime'; id: string; event: RealtimeEvent }
  | { type: 'closed'; id: string; reason: string; error?: string };

type ClosedMessage = Extract<ServerMessage, { type: 'closed' }>;

interface Settlement {
  resolve: () => void;
  reject: (error: unknown) => void;
}

interface ConversationSubscription {
  kind: 'conversation';
  id: string;
  conversationId: string;
  level: ChatEventLevel;
  capabilities?: string;
  delivery: ChatEventDelivery;
  settlement: Settlement;
  /** The server holds this subscription on the current connection. */
  attached: boolean;
  /** The connection a submission was bound to by its request. */
  boundConnectionId?: string;
  /** Set while a submission's request is in flight; its response decides the outcome. */
  submitting?: boolean;
  /** A closing notice that arrived before the submission's response. */
  deferredClose?: ClosedMessage;
  /** For a submission, the turn whose end completes it. */
  turnId?: string;
  /** Set once a submission has delivered its turn's outcome. */
  turnEnded?: boolean;
  /** Set once a submission follows its turn as an ordinary subscriber. */
  resumed?: boolean;
  /** Set once the server has acknowledged this subscription. */
  acknowledged?: boolean;
  /** Called when the subscription is established again; see ChatEventSocket.watch. */
  onResubscribed?: (state: ChatEventResubscription) => void;
  /** A run seen starting but not yet ending; checked after missed events. */
  openTurnId?: string;
  /** Events held while a missed ending is reconciled. */
  held?: ChatStreamEvent[];
}

interface RealtimeSubscription {
  kind: 'realtime';
  id: string;
  callId: string;
  onEvent: (event: RealtimeEvent) => void;
  settlement: Settlement;
}

type Subscription = ConversationSubscription | RealtimeSubscription;

export interface ChatEventSocketOptions {
  url: () => string;
  getTurnReceipt: (conversationId: string, turnId: string) => Promise<TurnReceipt>;
  createSocket?: (url: string) => WebSocket;
  connectTimeoutMs?: number;
  reconnectDelaysMs?: readonly number[];
  /** Waits before each attempt to read a missed turn's saved status. */
  receiptRetryDelaysMs?: readonly number[];
}

export type SubmitChatRequest = (
  request: ChatRequest,
  headers: Record<string, string>,
  signal?: AbortSignal
) => Promise<Response>;

const DEFAULT_RECONNECT_DELAYS_MS = [250, 500, 1000, 2000, 4000, 8000];
const DEFAULT_RECEIPT_RETRY_DELAYS_MS = [0, 500, 1500];

/** Reported when the page cannot open its event socket. */
export const EVENT_SOCKET_UNAVAILABLE =
  'Could not connect to live updates at /api/chat/events. Check that Kodelet is running and that any reverse proxy allows WebSocket connections.';
const LOST_CONNECTION = 'Lost connection to the server. Reload the page to reconnect.';
const UNCONFIRMED_OUTCOME =
  'Could not confirm how the run ended. Reload the conversation to see its saved result.';
const CALL_DISCONNECTED = 'The call disconnected. Try again.';

const abortError = () => new DOMException('The operation was aborted.', 'AbortError');

const sleep = (ms: number) => new Promise<void>((resolve) => setTimeout(resolve, ms));

const randomTurnId = (): string =>
  typeof globalThis.crypto?.randomUUID === 'function'
    ? globalThis.crypto.randomUUID()
    : `turn-${Date.now()}-${Math.random().toString(36).slice(2)}`;

/** The events that report a turn's saved outcome, or none while it is unfinished. */
export const turnReceiptEvents = (receipt: TurnReceipt): ChatStreamEvent[] => {
  const scope = { conversation_id: receipt.conversationId, turn_id: receipt.turnId };
  switch (receipt.status) {
    case 'succeeded':
      return [{ kind: 'done', ...scope }];
    case 'cancelled':
      return [{ kind: 'done', cancelled: true, ...scope }];
    case 'interrupted':
      return [
        { kind: 'error', error: receipt.error || 'The run stopped before it finished.', ...scope },
      ];
    case 'failed':
      return [{ kind: 'error', error: receipt.error || 'The run failed.', ...scope }];
    default:
      return [];
  }
};

const isTerminal = (event: ChatStreamEvent) => event.kind === 'done' || event.kind === 'error';

const unconfirmedOutcome = (
  subscription: { conversationId: string },
  turnId: string
): ChatStreamEvent => ({
  kind: 'error',
  error: UNCONFIRMED_OUTCOME,
  conversation_id: subscription.conversationId,
  turn_id: turnId,
});

const parseMessage = (data: unknown): ServerMessage | null => {
  if (typeof data !== 'string') return null;
  try {
    const message = JSON.parse(data);
    return message && typeof message.type === 'string' ? (message as ServerMessage) : null;
  } catch {
    return null;
  }
};

export class ChatEventSocket {
  private socket: WebSocket | null = null;
  private connectionId: string | null = null;
  private connecting: Promise<string | null> | null = null;
  private reconnecting = false;
  private nextId = 0;
  private readonly subscriptions = new Map<string, Subscription>();

  constructor(private readonly options: ChatEventSocketOptions) {}

  /**
   * Opens the socket if needed, and resolves to its connection ID, or to null
   * when it cannot be opened: the server is unreachable, or a proxy refuses
   * WebSockets.
   */
  connect(): Promise<string | null> {
    if (this.socket && this.connectionId) return Promise.resolve(this.connectionId);
    this.connecting ??= this.open().finally(() => {
      this.connecting = null;
    });
    return this.connecting;
  }

  /**
   * Follows a conversation until aborted, rejected, or ended by the server.
   * After a disconnect, or after falling behind, the socket follows it again
   * and reports a run ending it missed. Other events may be missing, so
   * onResubscribed is then called, once that ending has been delivered, so the
   * page can reload the conversation's saved state.
   */
  watch(
    conversationId: string,
    options: {
      level?: ChatEventLevel;
      capabilities?: string;
      signal?: AbortSignal;
      onEvent: (event: ChatStreamEvent) => void;
      onResubscribed?: (state: ChatEventResubscription) => void;
    }
  ): Promise<void> {
    if (options.signal?.aborted) return Promise.reject(abortError());
    const subscription = this.conversationSubscription(
      conversationId,
      options.onEvent,
      options.level ?? 'full'
    );
    subscription.capabilities = options.capabilities;
    subscription.onResubscribed = options.onResubscribed;
    const finished = this.register(subscription, options.signal);
    if (this.connectionId) this.attach(subscription);
    else this.ensureConnection();
    return finished;
  }

  /**
   * Submits a turn whose events arrive on this socket, and resolves once the
   * turn ends. The request returns as soon as the turn starts, so it does not
   * hold an HTTP connection for the length of the run.
   */
  async submit(
    request: ChatRequest & { conversationId: string },
    options: {
      signal?: AbortSignal;
      onEvent: (event: ChatStreamEvent) => void;
      post: SubmitChatRequest;
    }
  ): Promise<void> {
    if (options.signal?.aborted) throw abortError();
    const turnRequest = { ...request, turnId: request.turnId || randomTurnId() };
    const subscription = this.conversationSubscription(
      request.conversationId,
      options.onEvent,
      'full'
    );
    subscription.submitting = true;
    subscription.turnId = turnRequest.turnId;
    const finished = this.register(subscription, options.signal);
    // Observe early failures; the caller awaits the same promise below.
    finished.catch(() => {});

    let response: Response | undefined;
    // A precondition failure means the server no longer knew the connection,
    // so nothing was submitted; reconnect and try once more.
    for (let attempt = 0; attempt < 2 && this.subscriptions.has(subscription.id); attempt += 1) {
      const connectionId = await this.connect();
      if (!connectionId) {
        this.end(subscription, { error: new Error(LOST_CONNECTION) });
        return finished;
      }
      subscription.boundConnectionId = connectionId;
      try {
        response = await options.post(
          turnRequest,
          { [EVENT_CONNECTION_HEADER]: connectionId, [EVENT_SUBSCRIPTION_HEADER]: subscription.id },
          options.signal
        );
      } catch (error) {
        this.end(subscription, { error, unsubscribe: true });
        return finished;
      }
      if (response.status !== 412) break;
      this.dropConnection();
    }
    if (!response || !this.subscriptions.has(subscription.id)) return finished;
    if (!response.ok) {
      this.end(subscription, { error: await responseError(response), unsubscribe: true });
      return finished;
    }

    let reply: ChatSubmissionResponse;
    try {
      reply = await response.json();
    } catch (error) {
      this.end(subscription, { error, unsubscribe: true });
      return finished;
    }
    subscription.submitting = false;
    if (!this.subscriptions.has(subscription.id)) return finished;
    if (!reply.accepted) {
      // Already submitted: report its saved outcome, or follow it until it ends.
      const events = reply.receipt ? turnReceiptEvents(reply.receipt) : [];
      if (events.length > 0) {
        for (const event of [
          { kind: 'conversation', conversation_id: reply.conversation_id, turn_id: reply.turn_id },
          ...events,
        ] satisfies ChatStreamEvent[]) {
          this.deliver(subscription, event);
        }
        this.end(subscription, { unsubscribe: true });
        return finished;
      }
      subscription.attached = false;
      if (this.connectionId) this.attach(subscription);
      else this.ensureConnection();
      return finished;
    }

    const deferredClose = subscription.deferredClose;
    subscription.deferredClose = undefined;
    if (subscription.boundConnectionId === this.connectionId) {
      subscription.attached = true;
      if (deferredClose) this.closed(subscription, deferredClose);
    } else if (this.connectionId) {
      // The page reconnected while the request was in flight; the run's events
      // went to the old connection.
      this.attach(subscription);
    } else {
      // The socket closed while the request was in flight, and reconnecting
      // may have given up meanwhile. Try again: the turn is then followed and
      // reconciled, or reported lost.
      this.ensureConnection();
    }
    return finished;
  }

  /** Delivers a live voice call's events until the call ends. */
  attachRealtime(
    callId: string,
    options: { signal?: AbortSignal; onEvent: (event: RealtimeEvent) => void }
  ): Promise<void> {
    if (options.signal?.aborted) return Promise.reject(abortError());
    const subscription: RealtimeSubscription = {
      kind: 'realtime',
      id: this.newSubscriptionId(),
      callId,
      onEvent: options.onEvent,
      settlement: { resolve: () => {}, reject: () => {} },
    };
    const finished = this.register(subscription, options.signal);
    if (!this.send({ type: 'realtime', id: subscription.id, callId })) {
      this.end(subscription, { error: new Error(CALL_DISCONNECTED) });
    }
    return finished;
  }

  /** Closes the socket and ends every subscription. */
  close(): void {
    const socket = this.socket;
    this.socket = null;
    this.connectionId = null;
    for (const subscription of [...this.subscriptions.values()]) {
      this.end(subscription, { error: abortError() });
    }
    socket?.close();
  }

  private conversationSubscription(
    conversationId: string,
    onEvent: (event: ChatStreamEvent) => void,
    level: ChatEventLevel
  ): ConversationSubscription {
    const subscription: ConversationSubscription = {
      kind: 'conversation',
      id: this.newSubscriptionId(),
      conversationId,
      level,
      attached: false,
      settlement: { resolve: () => {}, reject: () => {} },
      delivery: createChatEventDelivery(onEvent, {
        conversationId,
        onDeferredError: (error) => this.end(subscription, { error, unsubscribe: true }),
      }),
    };
    return subscription;
  }

  private newSubscriptionId(): string {
    this.nextId += 1;
    return `s${this.nextId}`;
  }

  private register(subscription: Subscription, signal?: AbortSignal): Promise<void> {
    return new Promise<void>((resolve, reject) => {
      const onAbort = () => this.end(subscription, { error: abortError(), unsubscribe: true });
      subscription.settlement = {
        resolve: () => {
          signal?.removeEventListener('abort', onAbort);
          resolve();
        },
        reject: (error) => {
          signal?.removeEventListener('abort', onAbort);
          reject(error);
        },
      };
      this.subscriptions.set(subscription.id, subscription);
      signal?.addEventListener('abort', onAbort, { once: true });
    });
  }

  /**
   * Ends a subscription here: stops following it on the server if asked,
   * flushes its text, dismisses its prompts and settles its promise.
   */
  private end(subscription: Subscription, options: { error?: unknown; unsubscribe?: boolean }) {
    if (this.subscriptions.get(subscription.id) !== subscription) return;
    this.subscriptions.delete(subscription.id);
    if (options.unsubscribe) this.send({ type: 'unsubscribe', id: subscription.id });
    let error = options.error;
    if (subscription.kind === 'conversation') {
      try {
        subscription.delivery.close();
      } catch (closeError) {
        error ??= closeError;
      }
    }
    if (error === undefined) subscription.settlement.resolve();
    else subscription.settlement.reject(error);
  }

  private send(message: Record<string, unknown>): boolean {
    const socket = this.socket;
    if (!socket || socket.readyState !== 1) return false;
    socket.send(JSON.stringify(message));
    return true;
  }

  private attach(subscription: ConversationSubscription) {
    subscription.attached = true;
    if (subscription.turnId) subscription.resumed = true;
    this.send({
      type: 'subscribe',
      id: subscription.id,
      conversationId: subscription.conversationId,
      level: subscription.level,
      ...(subscription.capabilities ? { capabilities: subscription.capabilities } : {}),
    });
  }

  private open(): Promise<string | null> {
    return new Promise((resolve) => {
      let socket: WebSocket;
      try {
        socket = (this.options.createSocket ?? ((url: string) => new WebSocket(url)))(
          this.options.url()
        );
      } catch {
        resolve(null);
        return;
      }
      let settled = false;
      const settle = (connectionId: string | null) => {
        if (settled) return;
        settled = true;
        clearTimeout(timer);
        if (!connectionId) {
          socket.onmessage = null;
          socket.onclose = null;
          socket.close();
          resolve(null);
          return;
        }
        this.socket = socket;
        this.connectionId = connectionId;
        resolve(connectionId);
        // Follow again every conversation that was followed before a disconnect.
        for (const subscription of this.subscriptions.values()) {
          if (subscription.kind === 'conversation' && !subscription.attached) {
            if (!subscription.submitting) this.attach(subscription);
          }
        }
      };
      const timer = setTimeout(() => settle(null), this.options.connectTimeoutMs ?? 5000);
      socket.onmessage = (message: MessageEvent) => {
        const data = parseMessage(message.data);
        if (!settled) {
          settle(data?.type === 'hello' && data.connectionId ? data.connectionId : null);
        } else if (data && socket === this.socket) {
          this.handle(data);
        }
      };
      socket.onclose = () => {
        if (!settled) settle(null);
        else if (socket === this.socket) this.disconnected();
      };
      // A close event always follows an error.
      socket.onerror = () => {};
    });
  }

  /** Abandons a connection the server no longer knows, without waiting for it to close. */
  private dropConnection() {
    const socket = this.socket;
    if (!socket) return;
    this.disconnected();
    socket.onclose = null;
    socket.close();
  }

  private disconnected() {
    this.socket = null;
    this.connectionId = null;
    for (const subscription of [...this.subscriptions.values()]) {
      if (subscription.kind === 'realtime') {
        // A call cannot outlive its only event observer.
        this.end(subscription, { error: new Error(CALL_DISCONNECTED) });
        continue;
      }
      subscription.attached = false;
      subscription.held = undefined;
      // The server dismissed this connection's prompts when it closed.
      try {
        subscription.delivery.dismissPrompts();
      } catch (error) {
        this.end(subscription, { error });
      }
    }
    if (this.subscriptions.size > 0) void this.reconnect();
  }

  private ensureConnection() {
    void this.connect().then((connectionId) => {
      if (!connectionId && this.subscriptions.size > 0) void this.reconnect();
    });
  }

  private async reconnect() {
    if (this.reconnecting) return;
    this.reconnecting = true;
    try {
      for (const delay of this.options.reconnectDelaysMs ?? DEFAULT_RECONNECT_DELAYS_MS) {
        await sleep(delay);
        if (this.subscriptions.size === 0 || this.connectionId) return;
        if (await this.connect()) return;
      }
      const error = new Error(LOST_CONNECTION);
      for (const subscription of [...this.subscriptions.values()]) {
        // A submission in flight learns its outcome from its own request.
        if (subscription.kind === 'conversation' && subscription.submitting) continue;
        this.end(subscription, { error });
      }
    } finally {
      this.reconnecting = false;
    }
  }

  private handle(message: ServerMessage) {
    if (message.type === 'hello') return;
    const subscription = this.subscriptions.get(message.id);
    if (!subscription) return;
    switch (message.type) {
      case 'subscribed':
        if (subscription.kind === 'conversation') void this.acknowledged(subscription, message);
        return;
      case 'event':
        if (subscription.kind !== 'conversation') return;
        if (subscription.held) subscription.held.push(message.event);
        else this.deliver(subscription, message.event);
        return;
      case 'realtime':
        if (subscription.kind !== 'realtime') return;
        try {
          subscription.onEvent(message.event);
        } catch (error) {
          this.end(subscription, { error, unsubscribe: true });
        }
        return;
      case 'closed':
        this.closed(subscription, message);
        return;
    }
  }

  private deliver(subscription: ConversationSubscription, event: ChatStreamEvent) {
    if (this.subscriptions.get(subscription.id) !== subscription) return;
    if (event.kind === 'conversation' && event.turn_id) subscription.openTurnId = event.turn_id;
    const terminal = isTerminal(event);
    if (terminal && (!event.turn_id || event.turn_id === subscription.openTurnId)) {
      subscription.openTurnId = undefined;
    }
    if (terminal && subscription.turnId && event.turn_id === subscription.turnId) {
      subscription.turnEnded = true;
    }
    try {
      subscription.delivery.deliver(event);
    } catch (error) {
      this.end(subscription, { error, unsubscribe: true });
      return;
    }
    // Without the server's completion notice, a resumed submission ends with its turn.
    if (terminal && subscription.resumed && event.turn_id === subscription.turnId) {
      this.end(subscription, { unsubscribe: true });
    }
  }

  private closed(subscription: Subscription, message: ClosedMessage) {
    if (subscription.kind === 'realtime') {
      this.end(subscription, {
        error:
          message.reason === 'rejected'
            ? new Error(message.error || 'Could not connect to the call.')
            : undefined,
      });
      return;
    }
    if (subscription.submitting) {
      subscription.deferredClose = message;
      return;
    }
    switch (message.reason) {
      case 'overflow':
        // The page fell behind and missed events: follow again and reconcile.
        subscription.attached = false;
        try {
          subscription.delivery.dismissPrompts();
        } catch (error) {
          this.end(subscription, { error });
          return;
        }
        if (this.connectionId) this.attach(subscription);
        return;
      case 'rejected':
        this.end(subscription, {
          error: new Error(message.error || 'Could not follow the conversation.'),
        });
        return;
      default:
        if (subscription.turnId && !subscription.turnEnded) {
          // The server ended a submission without delivering its outcome:
          // report the saved one rather than assume the turn succeeded.
          void this.settleFromSavedStatus(subscription, subscription.turnId);
          return;
        }
        this.end(subscription, {});
    }
  }

  /**
   * Handles the server's acknowledgement of a subscription. Any later one
   * follows a disconnect or falling behind, so the page is told once a missed
   * run ending has been reported.
   */
  private async acknowledged(
    subscription: ConversationSubscription,
    acknowledgement: { active?: boolean; turnId?: string }
  ) {
    const resubscribed = Boolean(subscription.acknowledged);
    subscription.acknowledged = true;
    await this.reconcile(subscription, acknowledgement);
    if (!resubscribed || this.subscriptions.get(subscription.id) !== subscription) return;
    try {
      subscription.onResubscribed?.({
        active: Boolean(acknowledgement.active),
        turnId: acknowledgement.turnId,
      });
    } catch (error) {
      this.end(subscription, { error, unsubscribe: true });
    }
  }

  /** Delivers a submitted turn's saved outcome, then ends the submission. */
  private async settleFromSavedStatus(subscription: ConversationSubscription, turnId: string) {
    const held: ChatStreamEvent[] = [];
    subscription.held = held;
    const events = await this.savedOutcome(subscription, turnId, held);
    if (!events || subscription.held !== held) return;
    subscription.held = undefined;
    // The run has ended, so a saved status that is not final is not confirmed.
    const outcome = events.length > 0 ? events : [unconfirmedOutcome(subscription, turnId)];
    for (const event of [...outcome, ...held]) this.deliver(subscription, event);
    this.end(subscription, {});
  }

  /**
   * Reads a turn's saved outcome, retrying briefly, while the subscription
   * holds later events. Resolves to undefined if the subscription moved on
   * meanwhile. An outcome that cannot be read is reported as an error, never
   * as success, so queued commands do not run after a turn that may have failed.
   */
  private async savedOutcome(
    subscription: ConversationSubscription,
    turnId: string,
    held: ChatStreamEvent[]
  ): Promise<ChatStreamEvent[] | undefined> {
    for (const delay of this.options.receiptRetryDelaysMs ?? DEFAULT_RECEIPT_RETRY_DELAYS_MS) {
      if (delay > 0) await sleep(delay);
      if (subscription.held !== held) return undefined;
      try {
        return turnReceiptEvents(
          await this.options.getTurnReceipt(subscription.conversationId, turnId)
        );
      } catch {
        // Retry: the saved status is the only reliable account of the turn.
      }
    }
    return [unconfirmedOutcome(subscription, turnId)];
  }

  /**
   * After following a conversation again, reports a run that ended unseen:
   * its saved status says how. Events that arrive meanwhile wait behind it.
   */
  private async reconcile(
    subscription: ConversationSubscription,
    acknowledgement: { active?: boolean; turnId?: string }
  ) {
    const missedTurn =
      subscription.openTurnId ?? (subscription.resumed ? subscription.turnId : undefined);
    if (!missedTurn || (acknowledgement.active && acknowledgement.turnId === missedTurn)) return;
    const held: ChatStreamEvent[] = [];
    subscription.held = held;
    const events = await this.savedOutcome(subscription, missedTurn, held);
    if (!events || subscription.held !== held) return;
    subscription.held = undefined;
    for (const event of [...events, ...held]) this.deliver(subscription, event);
  }
}

const responseError = async (response: Response): Promise<Error> => {
  let message = `HTTP ${response.status}`;
  try {
    const body = await response.json();
    message = body.error || body.message || message;
  } catch {
    // Keep the status message.
  }
  return Object.assign(new Error(message), { status: response.status });
};
