import {
  type Dispatch,
  type RefObject,
  type SetStateAction,
  useEffect,
  useEffectEvent,
} from 'react';
import apiService from '../../services/api';
import type { ChatRenderMessage, ChatStreamEvent, Conversation } from '../../types';
import { mergeConversationUsage } from './conversationModel';
import { isBlockingUIRequestEvent, type RunStreams } from './runStreams';
import { applyChatStreamEvent } from './state';

interface ViewedConversationStreamOptions {
  /** The conversation shown, if any. */
  conversationId: string | null;
  /** Whether its saved state has loaded; the stream attaches only after that. */
  loaded: boolean;
  /** A new value attaches a new stream, for example after a submitted turn ends. */
  conversationStreamVersion: number;
  runStreams: RunStreams;
  viewedConversationIdRef: RefObject<string | null>;
  /** Incremented when a run starts, so a stale completion cannot replace newer output. */
  conversationRunVersion: RefObject<number>;
  markConversationRunning: (id: string | null | undefined) => void;
  clearRunningConversation: (id: string | null | undefined) => void;
  discardQueuedCommands: (id: string, reason: string) => void;
  handleResourcesReloaded: (event: ChatStreamEvent, id: string) => void;
  /** Reports the end of a run this page saw start. */
  finishObservedRun: (id: string, event: ChatStreamEvent, controller: AbortController) => void;
  /** Reloads a finished conversation's saved state while isCurrent holds. */
  reconcileCompletedConversation: (id: string, isCurrent: () => boolean) => Promise<void>;
  /** Shows a prompt or notification, and reports whether the event was one. */
  handleUIRequest: (event: ChatStreamEvent) => boolean;
  handleExtensionWidgetEvent: (event: ChatStreamEvent) => boolean;
  setActiveConversationId: Dispatch<SetStateAction<string | null>>;
  setConversation: Dispatch<SetStateAction<Conversation | null>>;
  setMessages: Dispatch<SetStateAction<ChatRenderMessage[]>>;
  setStreamError: Dispatch<SetStateAction<string | null>>;
}

/**
 * Follows the conversation shown, applying its live events to the transcript.
 * A submitted turn's own stream takes over while it runs; this stream attaches
 * again when that turn ends. A stream that will run queued commands stays open
 * after the page navigates away, until the run it watches finishes.
 */
export const useViewedConversationStream = ({
  conversationId,
  loaded,
  conversationStreamVersion,
  runStreams,
  viewedConversationIdRef,
  conversationRunVersion,
  markConversationRunning,
  clearRunningConversation,
  discardQueuedCommands,
  handleResourcesReloaded,
  finishObservedRun,
  reconcileCompletedConversation,
  handleUIRequest,
  handleExtensionWidgetEvent,
  setActiveConversationId,
  setConversation,
  setMessages,
  setStreamError,
}: ViewedConversationStreamOptions) => {
  const onStreamUIInputRequest = useEffectEvent(handleUIRequest);
  const onObservedRunFinished = useEffectEvent(finishObservedRun);

  // biome-ignore lint/correctness/useExhaustiveDependencies(conversationStreamVersion): Completing a submitted stream must reattach the conversation watcher even when its ID is unchanged.
  useEffect(() => {
    if (!conversationId || !loaded || runStreams.sends[conversationId]) {
      return;
    }

    const streamInstance = runStreams.viewedGeneration + 1;
    runStreams.viewedGeneration = streamInstance;
    const controller = new AbortController();
    runStreams.viewed = controller;
    let sawEvent = false;
    let watchedTurn = 0;
    let terminalSeen = false;

    void apiService
      .streamConversation(conversationId, {
        signal: controller.signal,
        // Events may have been missed while the page was not following the
        // conversation, so reload its saved state. An active run is left to
        // stream: its end reloads the transcript, and a snapshot now would
        // replace its live output.
        onResubscribed: ({ active }) => {
          if (active) return;
          const runVersion = conversationRunVersion.current;
          void reconcileCompletedConversation(
            conversationId,
            () =>
              !controller.signal.aborted &&
              runStreams.viewedGeneration === streamInstance &&
              viewedConversationIdRef.current === conversationId &&
              !runStreams.sends[conversationId] &&
              conversationRunVersion.current === runVersion
          );
        },
        onEvent: (event: ChatStreamEvent) => {
          if (controller.signal.aborted && !isBlockingUIRequestEvent(event)) return;
          if (event.conversation_id && event.conversation_id !== conversationId) {
            return;
          }
          if (runStreams.sends[conversationId]) {
            return;
          }

          const eventForConversation = event.conversation_id
            ? event
            : { ...event, conversation_id: conversationId };

          if (isBlockingUIRequestEvent(event) && onStreamUIInputRequest(eventForConversation)) {
            sawEvent = true;
            return;
          }

          // A queued command keeps its original completion subscription alive
          // across navigation. The server does not replay missed terminal events.
          if (
            runStreams.commandObservers[conversationId] === controller &&
            runStreams.viewedGeneration !== streamInstance
          ) {
            handleResourcesReloaded(event, conversationId);
            if (!terminalSeen && (event.kind === 'done' || event.kind === 'error')) {
              terminalSeen = true;
              onObservedRunFinished(conversationId, event, controller);
            }
            return;
          }

          if (
            runStreams.viewedGeneration !== streamInstance ||
            viewedConversationIdRef.current !== conversationId
          ) {
            return;
          }

          sawEvent = true;
          if (handleExtensionWidgetEvent(event)) {
            return;
          }
          if (event.kind === 'conversation' && event.conversation_id) {
            conversationRunVersion.current += 1;
            watchedTurn += 1;
            terminalSeen = false;
            runStreams.observers[conversationId] = controller;
            setActiveConversationId(event.conversation_id);
            markConversationRunning(event.conversation_id);
            return;
          }

          handleResourcesReloaded(event, conversationId);

          if (event.kind === 'usage' && event.usage) {
            setConversation((currentConversation) =>
              mergeConversationUsage(currentConversation, event.usage)
            );
            return;
          }

          if (event.kind === 'done') {
            if (terminalSeen) return;
            terminalSeen = true;
            onObservedRunFinished(conversationId, event, controller);
            const completedTurn = watchedTurn;
            const runVersion = conversationRunVersion.current;
            void reconcileCompletedConversation(
              conversationId,
              () =>
                !controller.signal.aborted &&
                runStreams.viewedGeneration === streamInstance &&
                viewedConversationIdRef.current === conversationId &&
                !runStreams.sends[conversationId] &&
                completedTurn === watchedTurn &&
                conversationRunVersion.current === runVersion
            );
          } else if (event.kind === 'error') {
            if (terminalSeen) return;
            terminalSeen = true;
            onObservedRunFinished(conversationId, event, controller);
          }

          if (event.kind === 'error') {
            setStreamError(event.error || 'Chat request failed');
          }

          if (event.kind === 'user-message' && !event.extensionId) {
            setConversation((currentConversation) =>
              currentConversation
                ? { ...currentConversation, pendingSteer: [] }
                : currentConversation
            );
          }

          if (onStreamUIInputRequest(eventForConversation)) {
            return;
          }

          setMessages((currentMessages) => applyChatStreamEvent(currentMessages, event));
        },
      })
      .catch((error) => {
        if (controller.signal.aborted) {
          return;
        }

        if (
          runStreams.viewedGeneration !== streamInstance ||
          viewedConversationIdRef.current !== conversationId
        ) {
          return;
        }

        const message =
          error instanceof Error ? error.message : 'Failed to resume conversation stream';
        if (message === 'conversation is not actively streaming') {
          if (!runStreams.sends[conversationId]) {
            clearRunningConversation(conversationId);
          }
          return;
        }

        console.error('Failed to resume conversation stream', error);
        setStreamError(message);
      })
      .finally(() => {
        if (runStreams.observers[conversationId] === controller) {
          delete runStreams.observers[conversationId];
        }
        const activeObserver = runStreams.observers[conversationId];
        if (runStreams.commandObservers[conversationId] === controller && !terminalSeen) {
          discardQueuedCommands(
            conversationId,
            'the connection ended before completion was confirmed'
          );
          if (
            !runStreams.sends[conversationId] &&
            (!activeObserver || activeObserver.signal.aborted)
          ) {
            clearRunningConversation(conversationId);
          }
        }
        if (runStreams.viewed === controller) {
          runStreams.viewed = null;
        }

        if (
          controller.signal.aborted ||
          runStreams.viewedGeneration !== streamInstance ||
          viewedConversationIdRef.current !== conversationId
        ) {
          return;
        }

        if (!runStreams.sends[conversationId] && !runStreams.commandObservers[conversationId]) {
          if (!terminalSeen) {
            discardQueuedCommands(
              conversationId,
              'the connection ended before completion was confirmed'
            );
          }
          if (sawEvent && (!activeObserver || activeObserver.signal.aborted)) {
            clearRunningConversation(conversationId);
          }
        }
      });

    return () => {
      if (runStreams.commandObservers[conversationId] !== controller) controller.abort();
      if (runStreams.viewed === controller) {
        runStreams.viewed = null;
      }
    };
  }, [
    clearRunningConversation,
    conversationId,
    conversationRunVersion,
    conversationStreamVersion,
    discardQueuedCommands,
    handleExtensionWidgetEvent,
    handleResourcesReloaded,
    loaded,
    markConversationRunning,
    reconcileCompletedConversation,
    runStreams,
    setActiveConversationId,
    setConversation,
    setMessages,
    setStreamError,
    viewedConversationIdRef,
  ]);
};
