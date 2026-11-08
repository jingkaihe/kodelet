import { useState } from 'react';
import type { ChatStreamEvent } from '../../types';

/**
 * The chat page's event streams for running conversations, by role. Each entry
 * is the AbortController of one stream, keyed by conversation ID. One stream can
 * hold several roles: a monitor that saw a run start is also its observer, and
 * may complete the conversation's queued commands.
 *
 * Streams share the page's event socket, so following many conversations does
 * not use up the browser's HTTP connections.
 */
export interface RunStreams {
  /** The stream submitting each conversation's current turn; it owns the turn's prompts. */
  sends: Record<string, AbortController>;
  /** Lifecycle streams following running conversations that are not shown. */
  monitors: Record<string, AbortController>;
  /** The stream that saw each conversation's current run start. */
  observers: Record<string, AbortController>;
  /** The stream whose successful completion runs each conversation's queued commands. */
  commandObservers: Record<string, AbortController>;
  /** The stream following the conversation shown. */
  viewed: AbortController | null;
  /** The send stream of the conversation shown when it was submitted. */
  viewedSend: AbortController | null;
  /** Incremented to retire callbacks of the shown conversation's earlier streams. */
  viewedGeneration: number;
}

const createRunStreams = (): RunStreams => ({
  sends: {},
  monitors: {},
  observers: {},
  commandObservers: {},
  viewed: null,
  viewedSend: null,
  viewedGeneration: 0,
});

/** The page's stream registry. It is mutable and stable for the page's lifetime. */
export const useRunStreams = (): RunStreams => useState(createRunStreams)[0];

/** Aborts every stream, for example when the page unmounts. */
export const abortRunStreams = (streams: RunStreams) => {
  streams.viewedGeneration += 1;
  streams.viewedSend?.abort();
  for (const group of [streams.sends, streams.monitors, streams.commandObservers]) {
    for (const controller of Object.values(group)) controller.abort();
  }
  streams.viewed?.abort();
  streams.sends = {};
  streams.monitors = {};
  streams.commandObservers = {};
  streams.observers = {};
};

/** Prompt events, which the stream owning a run delivers even after it is aborted. */
export const isBlockingUIRequestEvent = (event: ChatStreamEvent): boolean =>
  event.kind === 'ui-input-request' ||
  event.kind === 'ui-confirm-request' ||
  event.kind === 'ui-select-request' ||
  event.kind === 'ui-request-end';
