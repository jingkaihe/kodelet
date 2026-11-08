import { afterEach, describe, expect, it, vi } from 'vitest';
import type { ChatStreamEvent } from '../types';
import { createChatEventDelivery } from './chatEventDelivery';

afterEach(() => {
  vi.useRealTimers();
});

describe('createChatEventDelivery', () => {
  it('shows the first text at once and coalesces the rest without delaying event boundaries', async () => {
    vi.useFakeTimers();
    const onEvent = vi.fn();
    const delivery = createChatEventDelivery(onEvent);

    delivery.deliver({ kind: 'text-delta', delta: 'first' });
    expect(onEvent).toHaveBeenCalledExactlyOnceWith({ kind: 'text-delta', delta: 'first' });
    await vi.advanceTimersByTimeAsync(30);
    delivery.deliver({ kind: 'text-delta', delta: ' second' });
    delivery.deliver({ kind: 'text-delta', delta: ' batch' });
    await vi.advanceTimersByTimeAsync(44);
    expect(onEvent).toHaveBeenCalledOnce();
    await vi.advanceTimersByTimeAsync(1);
    expect(onEvent).toHaveBeenLastCalledWith({ kind: 'text-delta', delta: ' second batch' });

    const boundaries: ChatStreamEvent[] = [
      { kind: 'text-delta', delta: 'tail' },
      { kind: 'thinking-delta', delta: 'reasoning' },
      { kind: 'tool-use', tool_call_id: 'tool-1' },
      { kind: 'text-delta', delta: 'last' },
      { kind: 'done' },
    ];
    for (const event of boundaries) delivery.deliver(event);
    expect(onEvent.mock.calls.map(([event]) => event)).toEqual([
      { kind: 'text-delta', delta: 'first' },
      { kind: 'text-delta', delta: ' second batch' },
      ...boundaries,
    ]);
    expect(vi.getTimerCount()).toBe(0);
  });

  it('flushes buffered text and stops its timer when closed', async () => {
    vi.useFakeTimers();
    const onEvent = vi.fn();
    const delivery = createChatEventDelivery(onEvent);
    delivery.deliver({ kind: 'text-delta', delta: 'head' });
    delivery.deliver({ kind: 'text-delta', delta: 'tail' });
    expect(onEvent).toHaveBeenCalledExactlyOnceWith({ kind: 'text-delta', delta: 'head' });

    delivery.close();
    expect(onEvent).toHaveBeenCalledTimes(2);
    expect(onEvent).toHaveBeenLastCalledWith({ kind: 'text-delta', delta: 'tail' });
    expect(vi.getTimerCount()).toBe(0);
    await vi.advanceTimersByTimeAsync(100);
    expect(onEvent).toHaveBeenCalledTimes(2);
  });

  it('reports a callback error raised while flushing buffered text', async () => {
    vi.useFakeTimers();
    const failure = new Error('Callback failed');
    const onEvent = vi.fn((event: ChatStreamEvent) => {
      if (event.delta === 'tail') throw failure;
    });
    const onDeferredError = vi.fn();
    const delivery = createChatEventDelivery(onEvent, { onDeferredError });
    delivery.deliver({ kind: 'text-delta', delta: 'head' });
    delivery.deliver({ kind: 'text-delta', delta: 'tail' });

    await vi.advanceTimersByTimeAsync(100);
    expect(onDeferredError).toHaveBeenCalledExactlyOnceWith(failure);
    expect(onEvent.mock.calls.map(([event]) => event.delta)).toEqual(['head', 'tail']);
    expect(vi.getTimerCount()).toBe(0);
  });

  it('ends the prompts it showed, once, when it can no longer answer them', () => {
    const onEvent = vi.fn();
    const delivery = createChatEventDelivery(onEvent, { conversationId: 'conversation-1' });
    delivery.deliver({ kind: 'ui-input-request', ui_input: { id: 'request-1', title: 'Name?' } });
    delivery.deliver({ kind: 'ui-confirm-request', ui_confirm: { id: 'confirm-1', title: 'OK?' } });
    delivery.deliver({ kind: 'ui-request-end', ui_request_id: 'confirm-1' });

    delivery.dismissPrompts();
    expect(onEvent).toHaveBeenLastCalledWith({
      kind: 'ui-request-end',
      conversation_id: 'conversation-1',
      ui_request_id: 'request-1',
    });
    expect(onEvent).toHaveBeenCalledTimes(4);
    delivery.close();
    expect(onEvent).toHaveBeenCalledTimes(4);
  });
});
