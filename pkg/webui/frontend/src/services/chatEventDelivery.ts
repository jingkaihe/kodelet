import type { ChatStreamEvent } from '../types';

export const STREAM_TEXT_UPDATE_INTERVAL_MS = 75;

/** Delivers one stream's chat events to the page in order. */
export interface ChatEventDelivery {
  /** Delivers an event. Errors thrown by the page's callback propagate. */
  deliver(event: ChatStreamEvent): void;
  /**
   * Ends the prompts this stream showed: once it is disconnected, the server
   * dismisses them and they can no longer be answered.
   */
  dismissPrompts(): void;
  /** Flushes buffered text, dismisses prompts and stops the coalescing timer. */
  close(): void;
}

/**
 * Coalesces adjacent text updates rather than the stream, so tools, prompts and
 * completion never overtake text. The first text of a burst is shown at once,
 * then at most one update per interval. A callback error raised from the timer
 * is reported to onDeferredError, as it cannot propagate to the stream.
 */
export const createChatEventDelivery = (
  onEvent: (event: ChatStreamEvent) => void,
  options: { conversationId?: string; onDeferredError?: (error: unknown) => void } = {}
): ChatEventDelivery => {
  let conversationId = options.conversationId;
  const pendingPrompts = new Map<string, string | undefined>();
  let pendingDelta: ChatStreamEvent | undefined;
  let deltaTimer: ReturnType<typeof setTimeout> | undefined;
  let lastDeltaDelivery = Number.NEGATIVE_INFINITY;

  const flushDelta = () => {
    clearTimeout(deltaTimer);
    deltaTimer = undefined;
    const event = pendingDelta;
    pendingDelta = undefined;
    if (event) {
      lastDeltaDelivery = performance.now();
      onEvent(event);
    }
  };

  const dismissPrompts = () => {
    const prompts = [...pendingPrompts];
    pendingPrompts.clear();
    for (const [id, scope] of prompts) {
      onEvent({ kind: 'ui-request-end', conversation_id: scope, ui_request_id: id });
    }
  };

  return {
    deliver(event) {
      conversationId = event.conversation_id || conversationId;
      if (event.kind === 'text-delta' || event.kind === 'thinking-delta') {
        if (
          pendingDelta &&
          (pendingDelta.kind !== event.kind ||
            pendingDelta.conversation_id !== event.conversation_id)
        ) {
          flushDelta();
        }
        pendingDelta = pendingDelta
          ? { ...event, delta: (pendingDelta.delta || '') + (event.delta || '') }
          : event;
        if (deltaTimer !== undefined) return;
        const wait = lastDeltaDelivery + STREAM_TEXT_UPDATE_INTERVAL_MS - performance.now();
        if (wait <= 0) {
          flushDelta();
          return;
        }
        deltaTimer = setTimeout(() => {
          try {
            flushDelta();
          } catch (error) {
            options.onDeferredError?.(error);
          }
        }, wait);
        return;
      }
      flushDelta();
      const id = event.ui_input?.id || event.ui_confirm?.id || event.ui_select?.id;
      if (id) pendingPrompts.set(id, conversationId);
      if (event.kind === 'ui-request-end' && event.ui_request_id) {
        pendingPrompts.delete(event.ui_request_id);
      }
      onEvent(event);
    },
    dismissPrompts,
    close() {
      // Flush on EOF, cancellation or failure: do not lose the tail of an
      // interrupted answer or leave a callback timer running.
      try {
        flushDelta();
      } finally {
        // A disconnected stream loses response authority, not execution ownership.
        dismissPrompts();
      }
    },
  };
};
