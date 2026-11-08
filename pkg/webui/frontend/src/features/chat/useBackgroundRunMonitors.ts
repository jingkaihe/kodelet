import { useEffect, useEffectEvent } from 'react';
import apiService from '../../services/api';
import type { ChatStreamEvent } from '../../types';
import { isBlockingUIRequestEvent, type RunStreams } from './runStreams';

interface BackgroundRunMonitorOptions {
  streams: RunStreams;
  /** Every conversation known to be running, including runs started elsewhere. */
  runningConversationIds: string[];
  /** The conversation shown, which its own stream follows. */
  viewedConversationId: string | null;
  markRunning: (id: string) => void;
  clearRunning: (id: string) => void;
  onResourcesReloaded: (event: ChatStreamEvent, id: string) => void;
  /** Shows a prompt or notification, and reports whether the event was one. */
  onUIRequest: (event: ChatStreamEvent) => boolean;
  /** Reports the end of a run this page saw start. */
  onRunFinished: (id: string, event: ChatStreamEvent, controller: AbortController) => void;
  discardQueuedCommands: (id: string, reason: string) => void;
}

/**
 * Follows running conversations that are not shown, so the page notices when
 * they finish and can run their queued commands. Monitors use the lifecycle
 * level, without transcript detail, and share the page's event socket.
 */
export const useBackgroundRunMonitors = ({
  streams,
  runningConversationIds,
  viewedConversationId,
  markRunning,
  clearRunning,
  onResourcesReloaded,
  onUIRequest,
  onRunFinished,
  discardQueuedCommands,
}: BackgroundRunMonitorOptions) => {
  const onStreamUIRequest = useEffectEvent(onUIRequest);
  const onObservedRunFinished = useEffectEvent(onRunFinished);

  useEffect(() => {
    const runningIds = new Set(runningConversationIds);

    Object.entries(streams.monitors).forEach(([runningId, controller]) => {
      if (
        streams.commandObservers[runningId] !== controller &&
        (!runningIds.has(runningId) ||
          runningId === viewedConversationId ||
          streams.sends[runningId])
      ) {
        controller.abort();
        delete streams.monitors[runningId];
      }
    });

    runningConversationIds.forEach((runningId) => {
      if (
        runningId === viewedConversationId ||
        streams.sends[runningId] ||
        streams.commandObservers[runningId] ||
        streams.monitors[runningId]
      ) {
        return;
      }

      const controller = new AbortController();
      streams.monitors[runningId] = controller;
      let terminalSeen = false;

      void apiService
        .streamConversation(runningId, {
          signal: controller.signal,
          level: 'lifecycle',
          onEvent: (event: ChatStreamEvent) => {
            if (
              (controller.signal.aborted && !isBlockingUIRequestEvent(event)) ||
              streams.sends[runningId]
            )
              return;
            if (event.conversation_id && event.conversation_id !== runningId) {
              return;
            }

            const eventForConversation = event.conversation_id
              ? event
              : { ...event, conversation_id: runningId };

            if (event.kind === 'conversation') {
              terminalSeen = false;
              streams.observers[runningId] = controller;
              markRunning(runningId);
              return;
            }

            onResourcesReloaded(event, runningId);

            if (isBlockingUIRequestEvent(event) && onStreamUIRequest(eventForConversation)) {
              return;
            }

            if (event.kind === 'done' || event.kind === 'error') {
              if (terminalSeen) return;
              terminalSeen = true;
              onObservedRunFinished(runningId, event, controller);
            }
          },
        })
        .catch((error) => {
          if (controller.signal.aborted) {
            return;
          }

          const message =
            error instanceof Error ? error.message : 'Failed to monitor conversation stream';
          if (message !== 'conversation is not actively streaming') {
            console.error('Failed to monitor conversation stream', error);
          }
        })
        .finally(() => {
          if (streams.observers[runningId] === controller) {
            delete streams.observers[runningId];
          }
          if (streams.monitors[runningId] === controller) {
            delete streams.monitors[runningId];
          }

          if (
            !controller.signal.aborted &&
            !streams.sends[runningId] &&
            (!streams.commandObservers[runningId] ||
              streams.commandObservers[runningId] === controller)
          ) {
            if (!terminalSeen) {
              discardQueuedCommands(
                runningId,
                'the connection ended before completion was confirmed'
              );
            }
            const activeObserver = streams.observers[runningId];
            if (!activeObserver || activeObserver.signal.aborted) {
              clearRunning(runningId);
            }
          }
        });
    });
  }, [
    clearRunning,
    discardQueuedCommands,
    markRunning,
    onResourcesReloaded,
    runningConversationIds,
    streams,
    viewedConversationId,
  ]);
};
