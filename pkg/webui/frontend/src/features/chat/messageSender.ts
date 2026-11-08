import { type Dispatch, type RefObject, type SetStateAction, startTransition } from 'react';
import type { NavigateFunction } from 'react-router';
import apiService from '../../services/api';
import type {
  ChatRenderMessage,
  ChatStreamEvent,
  Conversation,
  PendingImageAttachment,
  Runner,
} from '../../types';
import { showToast } from '../../utils';
import {
  buildConversationPreview,
  type DraftWorkspace,
  mergeConversationUsage,
  normalizeConversation,
  type OptimisticRemoteConversation,
  reconcileStreamCWDForDisplay,
} from './conversationModel';
import { isBlockingUIRequestEvent, type QueuedCommand, type RunStreams } from './runStreams';
import { applyChatStreamEvent } from './state';
import { buildUserContent } from './useChatAttachments';
import { upsertConversationSummary } from './useConversationList';
import { routeTabKey } from './useConversationTabs';

export interface MessageSenderOptions {
  /** The conversation shown, or null for a new chat. */
  conversationId: string | null;
  /** The ID that a new chat's first turn creates. */
  draftConversationId: string;

  // Settings a new conversation starts with.
  selectedRunnerID: string;
  selectedEnvironmentProfile: string;
  selectedCWD: string;
  selectedProfile: string;
  selectedModel: string;
  selectedReasoningEffort: string;
  chatSettingsLoaded: boolean;
  currentCWDLabel: string;
  currentRunner?: Runner;
  isRemoteConversation: boolean;

  // Page state shared with the page's other streams and handlers.
  runStreams: RunStreams;
  queuedCommandsRef: RefObject<Record<string, QueuedCommand[]>>;
  viewedConversationIdRef: RefObject<string | null>;
  routerConversationIdRef: RefObject<string | null>;
  conversationPathOverrideRef: RefObject<string | null>;
  optimisticRemoteConversationRef: RefObject<OptimisticRemoteConversation | null>;
  draftWorkspaceRef: RefObject<DraftWorkspace | null>;
  conversationRunVersion: RefObject<number>;
  realtimeCaptionSeq: RefObject<number>;

  // Run lifecycle.
  markConversationRunning: (id: string | null | undefined) => void;
  replaceRunningConversation: (
    previousId: string | null | undefined,
    nextId: string | null | undefined
  ) => void;
  registerSendController: (id: string | null | undefined, controller: AbortController) => void;
  clearRunningConversationForController: (
    id: string | null | undefined,
    controller: AbortController
  ) => void;
  noteBackgroundRunFinished: (id: string, activity: 'unread' | 'error') => void;
  replaceQueuedCommands: (id: string, commands: QueuedCommand[]) => void;
  discardQueuedCommands: (id: string, reason: string) => void;
  handleResourcesReloaded: (event: ChatStreamEvent, id: string) => void;

  // The conversation shown, its transcript and the page around it.
  handleUIInputRequest: (event: ChatStreamEvent) => boolean;
  handleExtensionWidgetEvent: (event: ChatStreamEvent) => boolean;
  projectConversation: (saved: Conversation, captionSeq: number) => ChatRenderMessage[];
  setActiveConversationId: Dispatch<SetStateAction<string | null>>;
  setConversation: Dispatch<SetStateAction<Conversation | null>>;
  setConversations: Dispatch<SetStateAction<Conversation[]>>;
  setMessages: Dispatch<SetStateAction<ChatRenderMessage[]>>;
  setStreamError: Dispatch<SetStateAction<string | null>>;
  setAttachments: Dispatch<SetStateAction<PendingImageAttachment[]>>;
  setConversationStreamVersion: Dispatch<SetStateAction<number>>;
  refreshConversations: () => Promise<unknown>;
  updatePathForStartedConversation: (id: string) => void;
  replaceConversationTab: (from: string, to: string) => void;
  navigate: NavigateFunction;
}

/**
 * Submits the chat page's turns and runs its queued commands. Create it during
 * render: like handlers defined in the page, the returned functions use that
 * render's state.
 */
export const createMessageSender = ({
  conversationId,
  draftConversationId,
  selectedRunnerID,
  selectedEnvironmentProfile,
  selectedCWD,
  selectedProfile,
  selectedModel,
  selectedReasoningEffort,
  chatSettingsLoaded,
  currentCWDLabel,
  currentRunner,
  isRemoteConversation,
  runStreams,
  queuedCommandsRef,
  viewedConversationIdRef,
  routerConversationIdRef,
  conversationPathOverrideRef,
  optimisticRemoteConversationRef,
  draftWorkspaceRef,
  conversationRunVersion,
  realtimeCaptionSeq,
  markConversationRunning,
  replaceRunningConversation,
  registerSendController,
  clearRunningConversationForController,
  noteBackgroundRunFinished,
  replaceQueuedCommands,
  discardQueuedCommands,
  handleResourcesReloaded,
  handleUIInputRequest,
  handleExtensionWidgetEvent,
  projectConversation,
  setActiveConversationId,
  setConversation,
  setConversations,
  setMessages,
  setStreamError,
  setAttachments,
  setConversationStreamVersion,
  refreshConversations,
  updatePathForStartedConversation,
  replaceConversationTab,
  navigate,
}: MessageSenderOptions) => {
  const sendMessage = async (
    prompt: string,
    attachmentsForSend: PendingImageAttachment[],
    queuedConversationId?: string
  ) => {
    conversationRunVersion.current += 1;
    const targetConversationId = queuedConversationId || conversationId || draftConversationId;
    const viewConversationIdAtStart = queuedConversationId || conversationId;
    const isNewConversation = !queuedConversationId && !conversationId;
    const isTargetViewed = () =>
      viewedConversationIdRef.current === targetConversationId ||
      (isNewConversation && viewedConversationIdRef.current === null);
    const initialUserContent = buildUserContent(prompt, attachmentsForSend);
    if (isTargetViewed()) {
      setStreamError(null);
      setMessages((currentMessages) => [
        ...currentMessages,
        { role: 'user', content: initialUserContent },
      ]);
    }
    const existingOptimisticRemoteConversation =
      !queuedConversationId &&
      conversationId &&
      optimisticRemoteConversationRef.current?.conversationId === conversationId
        ? optimisticRemoteConversationRef.current
        : null;
    const requestRunnerID = existingOptimisticRemoteConversation?.runnerId || selectedRunnerID;
    const requestEnvironmentProfile =
      existingOptimisticRemoteConversation?.environmentProfile || selectedEnvironmentProfile;
    const requestCWD = existingOptimisticRemoteConversation
      ? existingOptimisticRemoteConversation.cwd
      : isNewConversation
        ? selectedCWD.trim() || undefined
        : undefined;
    if (isNewConversation) {
      if (draftWorkspaceRef.current?.conversationId === targetConversationId) {
        draftWorkspaceRef.current.submitted = true;
      }
      optimisticRemoteConversationRef.current = requestRunnerID
        ? {
            conversationId: targetConversationId,
            runnerId: requestRunnerID,
            environmentProfile: requestEnvironmentProfile || undefined,
            cwd: requestCWD,
            confirmed: false,
          }
        : null;
      setActiveConversationId(targetConversationId);
      updatePathForStartedConversation(targetConversationId);
    }

    const controller = new AbortController();
    if (isTargetViewed()) runStreams.viewedSend = controller;
    registerSendController(targetConversationId, controller);
    runStreams.commandObservers[targetConversationId]?.abort();
    delete runStreams.commandObservers[targetConversationId];
    delete runStreams.observers[targetConversationId];
    // Only the submitting stream may complete this run. Retire any observers
    // before starting it so stale events cannot drain a second queued command.
    runStreams.monitors[targetConversationId]?.abort();
    delete runStreams.monitors[targetConversationId];
    if (isTargetViewed()) runStreams.viewed?.abort();
    markConversationRunning(targetConversationId);
    const userPreview = buildConversationPreview(prompt, attachmentsForSend);
    if (isNewConversation) {
      const now = new Date().toISOString();
      const newConversation = {
        id: targetConversationId,
        createdAt: now,
        updatedAt: now,
        messageCount: 1,
        summary: userPreview,
        preview: userPreview,
        cwd: currentCWDLabel,
        runnerId: requestRunnerID || undefined,
        environmentProfile: requestRunnerID ? requestEnvironmentProfile || undefined : undefined,
        runner: requestRunnerID ? currentRunner : undefined,
        profile: selectedProfile,
        model: selectedModel || undefined,
        reasoningEffort: chatSettingsLoaded ? selectedReasoningEffort : undefined,
        isRunning: true,
        messages: [{ role: 'user' as const, content: initialUserContent }],
        pendingSteer: [],
        toolResults: {},
      };
      setConversation(newConversation);
      setConversations((currentConversations) =>
        upsertConversationSummary(currentConversations, newConversation)
      );
    }

    let streamedConversationId = targetConversationId;
    let streamedError: string | null = null;
    let successful = false;
    let cancelled = false;
    const clearConfirmedOptimisticConversation = () => {
      if (
        optimisticRemoteConversationRef.current?.conversationId === streamedConversationId &&
        optimisticRemoteConversationRef.current.confirmed
      ) {
        optimisticRemoteConversationRef.current = null;
      }
    };

    try {
      await apiService.streamChat(
        {
          message: prompt,
          content: initialUserContent,
          conversationId: targetConversationId,
          runnerId: existingOptimisticRemoteConversation
            ? existingOptimisticRemoteConversation.runnerId
            : isNewConversation
              ? requestRunnerID || undefined
              : undefined,
          environmentProfile:
            existingOptimisticRemoteConversation || (isNewConversation && requestRunnerID)
              ? requestEnvironmentProfile || undefined
              : undefined,
          profile:
            !isNewConversation && !existingOptimisticRemoteConversation
              ? undefined
              : selectedProfile,
          options:
            (!isNewConversation && !existingOptimisticRemoteConversation) || !selectedModel
              ? undefined
              : { model: selectedModel },
          reasoningEffort:
            (!isNewConversation && !existingOptimisticRemoteConversation) || !chatSettingsLoaded
              ? undefined
              : selectedReasoningEffort,
          clientCapabilities: {
            interactiveUI: true,
            persistentWidgets: true,
            persistentSurfaces: false,
          },
          cwd: requestCWD,
        },
        {
          signal: controller.signal,
          onEvent: (event: ChatStreamEvent) => {
            if (controller.signal.aborted && !isBlockingUIRequestEvent(event)) return;
            if (event.kind === 'conversation' && event.conversation_id) {
              const streamedId = event.conversation_id;
              const canonicalCWD = event.cwd?.trim();
              const effectiveCWD = reconcileStreamCWDForDisplay(
                canonicalCWD,
                isTargetViewed() ? currentCWDLabel : '',
                isRemoteConversation
              );
              const previousStreamedId = streamedConversationId;
              const shouldAdoptStreamedConversation =
                viewedConversationIdRef.current === viewConversationIdAtStart ||
                (!viewConversationIdAtStart &&
                  viewedConversationIdRef.current === previousStreamedId) ||
                (!viewConversationIdAtStart && viewedConversationIdRef.current === streamedId);
              const shouldUpdatePath =
                !viewConversationIdAtStart &&
                streamedId !== streamedConversationId &&
                shouldAdoptStreamedConversation;
              streamedConversationId = streamedId;
              if (
                previousStreamedId &&
                optimisticRemoteConversationRef.current?.conversationId === previousStreamedId
              ) {
                optimisticRemoteConversationRef.current = {
                  ...optimisticRemoteConversationRef.current,
                  conversationId: streamedId,
                  cwd: effectiveCWD || optimisticRemoteConversationRef.current.cwd,
                  confirmed:
                    optimisticRemoteConversationRef.current.confirmed || Boolean(canonicalCWD),
                };
              }
              if (shouldAdoptStreamedConversation) {
                setActiveConversationId(streamedId);
                setConversation((currentConversation) =>
                  currentConversation
                    ? { ...currentConversation, id: streamedId, cwd: effectiveCWD }
                    : currentConversation
                );
              }
              if (
                previousStreamedId &&
                previousStreamedId !== streamedId &&
                runStreams.sends[previousStreamedId] === controller
              ) {
                delete runStreams.sends[previousStreamedId];
                const pending = queuedCommandsRef.current[previousStreamedId];
                if (pending) {
                  replaceQueuedCommands(previousStreamedId, []);
                  replaceQueuedCommands(streamedId, pending);
                }
              }
              registerSendController(streamedId, controller);
              replaceRunningConversation(previousStreamedId, streamedId);
              if (shouldUpdatePath) {
                updatePathForStartedConversation(streamedId);
              }
              if (!viewConversationIdAtStart) {
                const now = new Date().toISOString();
                setConversations((currentConversations) =>
                  upsertConversationSummary(
                    previousStreamedId && previousStreamedId !== streamedId
                      ? currentConversations.filter(
                          (currentConversation) => currentConversation.id !== previousStreamedId
                        )
                      : currentConversations,
                    {
                      id: streamedId,
                      createdAt: now,
                      updatedAt: now,
                      messageCount: 1,
                      summary: userPreview,
                      preview: userPreview,
                      cwd: effectiveCWD,
                      runnerId: selectedRunnerID || undefined,
                      environmentProfile: selectedRunnerID
                        ? selectedEnvironmentProfile || undefined
                        : undefined,
                      runner: selectedRunnerID ? currentRunner : undefined,
                      profile: selectedProfile,
                      isRunning: true,
                    }
                  )
                );
              }
            }

            const eventConversationId = event.conversation_id || streamedConversationId;
            const shouldUpdateCurrentView = Boolean(
              eventConversationId && viewedConversationIdRef.current === eventConversationId
            );
            const eventForConversation =
              event.conversation_id || !eventConversationId
                ? event
                : { ...event, conversation_id: eventConversationId };

            handleResourcesReloaded(event, eventConversationId);
            if (event.kind === 'done') {
              successful = !event.cancelled && !streamedError;
              cancelled = Boolean(event.cancelled);
            }

            if (isBlockingUIRequestEvent(event) && handleUIInputRequest(eventForConversation)) {
              return;
            }

            if (shouldUpdateCurrentView && handleExtensionWidgetEvent(event)) {
              return;
            }

            if (event.kind === 'usage' && event.usage) {
              if (shouldUpdateCurrentView) {
                setConversation((currentConversation) =>
                  mergeConversationUsage(currentConversation, event.usage)
                );
              }
              return;
            }

            if (event.kind === 'error') {
              successful = false;
              streamedError = event.error || 'Chat request failed';
              if (shouldUpdateCurrentView) {
                setStreamError(streamedError);
              }
              return;
            }

            if (event.kind === 'user-message' && !event.extensionId) {
              if (shouldUpdateCurrentView) {
                setConversation((currentConversation) =>
                  currentConversation
                    ? { ...currentConversation, pendingSteer: [] }
                    : currentConversation
                );
              }
            }

            if (shouldUpdateCurrentView && handleUIInputRequest(event)) {
              return;
            }

            if (shouldUpdateCurrentView) {
              setMessages((currentMessages) => applyChatStreamEvent(currentMessages, event));
            }
          },
        }
      );

      const finishedOnStartedConversation = Boolean(
        !viewConversationIdAtStart &&
          streamedConversationId &&
          viewedConversationIdRef.current === streamedConversationId
      );

      if (streamedError) {
        clearConfirmedOptimisticConversation();
        if (
          viewedConversationIdRef.current === viewConversationIdAtStart ||
          finishedOnStartedConversation
        ) {
          conversationPathOverrideRef.current = null;
          showToast(streamedError, 'error');
        }
        await refreshConversations();
        return;
      }

      if (optimisticRemoteConversationRef.current?.conversationId === streamedConversationId) {
        optimisticRemoteConversationRef.current = null;
      }

      if (
        streamedConversationId &&
        (viewedConversationIdRef.current === streamedConversationId ||
          finishedOnStartedConversation)
      ) {
        conversationPathOverrideRef.current = null;
        try {
          const captionSeq = realtimeCaptionSeq.current;
          const latestConversation = normalizeConversation(
            await apiService.getConversation(streamedConversationId)
          );
          if (viewedConversationIdRef.current === streamedConversationId) {
            setConversation(latestConversation);
            setMessages(projectConversation(latestConversation, captionSeq));
          }
        } catch {
          // The streamed conversation is already authoritative enough to continue. A later
          // conversation load will reconcile the persisted snapshot.
        }
        if (
          !queuedConversationId &&
          viewedConversationIdRef.current === streamedConversationId &&
          streamedConversationId !== routerConversationIdRef.current
        ) {
          replaceConversationTab(
            routeTabKey(routerConversationIdRef.current),
            streamedConversationId
          );
          startTransition(() => {
            navigate(`/c/${streamedConversationId}`, { replace: true });
          });
        }
      }

      await refreshConversations();
    } catch (error) {
      successful = false;
      if (error instanceof DOMException && error.name === 'AbortError') {
        return;
      }

      clearConfirmedOptimisticConversation();

      const failedOnStartedConversation = Boolean(
        !viewConversationIdAtStart &&
          streamedConversationId &&
          viewedConversationIdRef.current === streamedConversationId
      );

      const message = error instanceof Error ? error.message : 'Failed to send message';
      if (
        viewedConversationIdRef.current === viewConversationIdAtStart ||
        failedOnStartedConversation
      ) {
        conversationPathOverrideRef.current = null;
        if (!queuedConversationId) setAttachments(attachmentsForSend);
        setStreamError(message);
        showToast(message, 'error');
      }
    } finally {
      const ownsRun = runStreams.sends[streamedConversationId] === controller;
      if (runStreams.viewedSend === controller) {
        runStreams.viewedSend = null;
      }
      clearRunningConversationForController(streamedConversationId, controller);
      setConversationStreamVersion((currentVersion) => currentVersion + 1);
      if (ownsRun && !controller.signal.aborted && !cancelled) {
        noteBackgroundRunFinished(streamedConversationId, successful ? 'unread' : 'error');
      }
      if (ownsRun) {
        if (successful && !controller.signal.aborted) runNextQueuedCommand(streamedConversationId);
        else discardQueuedCommands(streamedConversationId, 'the run did not complete successfully');
      }
    }
  };

  const runNextQueuedCommand = (id: string) => {
    if (runStreams.sends[id]) return;
    const pending = queuedCommandsRef.current[id];
    if (!pending?.length) return;
    const [command, ...remaining] = pending;
    replaceQueuedCommands(id, remaining);
    // sendMessage acquires the per-conversation controller synchronously, before
    // any network await, and never reads or clears the current composer draft.
    void sendMessage(command.prompt, command.attachments, id);
  };

  return { sendMessage, runNextQueuedCommand };
};
