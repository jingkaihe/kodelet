import { PanelLeft, RotateCw } from 'lucide-react';
import type React from 'react';
import {
  startTransition,
  useCallback,
  useEffect,
  useEffectEvent,
  useLayoutEffect,
  useMemo,
  useRef,
  useState,
} from 'react';
import { useNavigate, useParams } from 'react-router';
import AboutKodeletDialog from '../components/chat/AboutKodeletDialog';
import ChatComposer from '../components/chat/ChatComposer';
import ChatSidebar, {
  ChatSidebarCollapsedRail,
  ConversationSearchDialog,
  previewConversation,
} from '../components/chat/ChatSidebar';
import ChatTranscript from '../components/chat/ChatTranscript';
import ChatWorkspacePanel from '../components/chat/ChatWorkspacePanel';
import ConversationStatistics from '../components/chat/ConversationStatistics';
import ConversationTabs, {
  adjacentConversationTabId,
  type ConversationTab,
} from '../components/chat/ConversationTabs';
import DictationDialog from '../components/chat/DictationDialog';
import ExtensionWidgets from '../components/chat/ExtensionWidgets';
import KeyboardShortcutsDialog from '../components/chat/KeyboardShortcutsDialog';
import NewChatContextDialog from '../components/chat/NewChatContextDialog';
import PendingSteerList from '../components/chat/PendingSteerList';
import ProviderSettingsDialog from '../components/chat/ProviderSettingsDialog';
import UIInputDialog from '../components/chat/UIInputDialog';
import { discardTerminalPopOuts } from '../components/workspace/terminalPopOut';
import {
  type DraftWorkspace,
  generateConversationId,
  normalizeConversation,
  type OptimisticRemoteConversation,
} from '../features/chat/conversationModel';
import { createMessageSender } from '../features/chat/messageSender';
import { abortRunStreams, type QueuedCommand, useRunStreams } from '../features/chat/runStreams';
import { applyRealtimeTranscript, conversationToChatMessages } from '../features/chat/state';
import { useBackgroundRunMonitors } from '../features/chat/useBackgroundRunMonitors';
import { buildUserContent, useChatAttachments } from '../features/chat/useChatAttachments';
import {
  MAX_SIDEBAR_WIDTH,
  MIN_SIDEBAR_WIDTH,
  useChatLayout,
} from '../features/chat/useChatLayout';
import { useChatSettings } from '../features/chat/useChatSettings';
import {
  SIDEBAR_CONVERSATION_LIMIT,
  upsertConversationSummary,
  useConversationList,
} from '../features/chat/useConversationList';
import { useConversationSearch } from '../features/chat/useConversationSearch';
import {
  NEW_CHAT_TAB_ID,
  routeTabKey,
  useConversationTabs,
} from '../features/chat/useConversationTabs';
import { isVoiceCommand, useDictation } from '../features/chat/useDictation';
import { useExtensionWidgets } from '../features/chat/useExtensionWidgets';
import { useRealtime } from '../features/chat/useRealtime';
import { useSlashCommands } from '../features/chat/useSlashCommands';
import { useTranscriptAutoScroll } from '../features/chat/useTranscriptAutoScroll';
import { useUIRequestDialog } from '../features/chat/useUIRequestDialog';
import { useViewedConversationStream } from '../features/chat/useViewedConversationStream';
import { useWorkspaceResize } from '../features/chat/useWorkspaceResize';
import apiService from '../services/api';
import type {
  AuthPrincipal,
  BrowserTarget,
  ChatStreamEvent,
  Conversation,
  GitDiffResponse,
  PendingImageAttachment,
  RealtimeEvent,
  Runner,
  WorkspaceTarget,
} from '../types';
import { cn, showToast } from '../utils';

/** What finished in a conversation while its tab was in the background. */
type TabActivity = 'unread' | 'error';

const withoutKey = <T,>(record: Record<string, T>, key: string): Record<string, T> => {
  if (!(key in record)) return record;
  const next = { ...record };
  delete next[key];
  return next;
};

const conversationTabStep = (event: KeyboardEvent): number => {
  if (!event.altKey || event.shiftKey || event.ctrlKey || event.metaKey) return 0;
  // Option changes event.key on macOS; match the physical bracket keys.
  if (event.code === 'BracketRight') return 1;
  if (event.code === 'BracketLeft') return -1;
  return 0;
};

const isMacPlatform = (): boolean =>
  typeof navigator !== 'undefined' && /Mac|iPhone|iPad/.test(navigator.userAgent);

const slashCommandName = (message: string): string | undefined =>
  message.trim().match(/^\/(\S+)/)?.[1];

const ChatPage: React.FC = () => {
  const navigate = useNavigate();
  const { id } = useParams<{ id: string }>();
  const conversationId = id || null;
  const [conversation, setConversation] = useState<Conversation | null>(null);
  const [messages, setMessages] = useState(() => conversationToChatMessages(null));
  const {
    widgets: extensionWidgets,
    handleEvent: handleExtensionWidgetEvent,
    reset: resetExtensionWidgets,
  } = useExtensionWidgets();
  const [authPrincipal, setAuthPrincipal] = useState<AuthPrincipal | null>(null);
  const [activeConversationId, setActiveConversationId] = useState<string | null>(conversationId);
  const [draftConversationId, setDraftConversationId] = useState(generateConversationId);
  const sessionConversationId = conversationId || draftConversationId;
  const draftWorkspaceRef = useRef<DraftWorkspace | null>(null);
  const draftHeartbeatInFlightRef = useRef(false);
  const [runners, setRunners] = useState<Runner[]>([]);
  const [draft, setDraft] = useState('');
  const composerRef = useRef<HTMLTextAreaElement | null>(null);
  const pendingCaret = useRef<number | null>(null);
  const dictationOwner = useRef('');
  // Live captions by ID, with the order in which they arrived.
  const realtimeCaptions = useRef(new Map<string, { event: RealtimeEvent; seq: number }>());
  const realtimeCaptionSeq = useRef(0);
  const conversationRunVersion = useRef(0);
  const realtimeTurn = useRef<{ conversationId: string; turnId?: string } | null>(null);
  // The server saves each caption before streaming it, so a GET requested after
  // a caption arrived has that text or newer. Reapply only this target's captions
  // received after the request (captionSeq), which may have raced it; the saved
  // history replaces the rest, and persistence and ordering stay server-owned.
  const projectConversation = useCallback((saved: Conversation, captionSeq: number) => {
    let projected = conversationToChatMessages(saved);
    for (const { event, seq } of realtimeCaptions.current.values()) {
      if (seq > captionSeq && event.conversationId === saved.id) {
        projected = applyRealtimeTranscript(projected, event);
      }
    }
    return projected;
  }, []);
  useLayoutEffect(() => {
    void sessionConversationId;
    realtimeCaptions.current.clear();
    realtimeTurn.current = null;
    conversationRunVersion.current += 1;
  }, [sessionConversationId]);
  const [conversationLoading, setConversationLoading] = useState(false);
  const [conversationError, setConversationError] = useState<string | null>(null);
  const [streamError, setStreamError] = useState<string | null>(null);
  const [conversationStreamVersion, setConversationStreamVersion] = useState(0);
  const [steering, setSteering] = useState(false);
  const [queuedCommands, setQueuedCommands] = useState<Record<string, QueuedCommand[]>>({});
  const queuedCommandsRef = useRef(queuedCommands);
  const [resourceVersions, setResourceVersions] = useState<Record<string, number>>({});
  const [locallyRunningConversationIds, setLocallyRunningConversationIds] = useState<string[]>([]);
  const { attachments, setAttachments, composerProps: attachmentProps } = useChatAttachments();
  const [gitDiffLoading, setGitDiffLoading] = useState(false);
  const [gitDiffError, setGitDiffError] = useState<string | null>(null);
  const [gitDiff, setGitDiff] = useState<GitDiffResponse | null>(null);
  const [providerSettingsOpen, setProviderSettingsOpen] = useState(false);
  const [infoDialog, setInfoDialog] = useState<'about' | 'shortcuts' | null>(null);
  const [composerFocusRequested, setComposerFocusRequested] = useState(false);
  const loadedConversationId = conversation?.id ?? null;
  const { followTranscript, resetTranscriptScroll, transcriptScrollProps } =
    useTranscriptAutoScroll();
  const runStreams = useRunStreams();
  const isSending = useCallback((id: string) => Boolean(runStreams.sends[id]), []);
  const gitDiffRequestRef = useRef(0);
  const workspaceTargetKeyRef = useRef('');
  const viewedConversationIdRef = useRef<string | null>(conversationId);
  const contextSettings = useChatSettings({ conversationId, viewedConversationIdRef, runners });
  const {
    settings: chatSettings,
    loaded: chatSettingsLoaded,
    dialogOpen: newChatDialogOpen,
    selected: {
      profile: selectedProfile,
      model: selectedModel,
      reasoningEffort: selectedReasoningEffort,
      runnerId: selectedRunnerID,
      environmentProfile: selectedEnvironmentProfile,
      cwd: selectedCWD,
    },
  } = contextSettings;
  const conversationPathOverrideRef = useRef<string | null>(null);
  const optimisticRemoteConversationRef = useRef<OptimisticRemoteConversation | null>(null);
  const routerConversationIdRef = useRef<string | null>(conversationId);
  const conversationTabs = useConversationTabs(conversationId);
  const tabKey = conversationTabs.activeId;
  const openTabIdsRef = useRef(new Set<string>());
  openTabIdsRef.current = new Set(conversationTabs.tabs.map((tab) => tab.id));
  const [tabActivity, setTabActivity] = useState<Record<string, TabActivity>>({});
  // Unsent composer text and images, kept per open tab while another tab is shown.
  const tabComposerRef = useRef(
    new Map<string, { draft: string; attachments: PendingImageAttachment[] }>()
  );
  const composerStateRef = useRef({ draft, attachments });
  composerStateRef.current = { draft, attachments };
  const composerTabKeyRef = useRef(tabKey);
  // Set when a tab turns into its started conversation, which is not a tab switch.
  const replacedTabRef = useRef<{ from: string; to: string } | null>(null);
  const replaceConversationTab = (from: string, to: string) => {
    replacedTabRef.current = { from, to };
    conversationTabs.replace(from, to);
  };
  useLayoutEffect(() => {
    const previousKey = composerTabKeyRef.current;
    if (previousKey === tabKey) return;
    composerTabKeyRef.current = tabKey;
    const replaced = replacedTabRef.current;
    replacedTabRef.current = null;
    if (replaced?.from === previousKey && replaced.to === tabKey) return;
    // Closed and started-in-place tabs leave nothing behind.
    if (openTabIdsRef.current.has(previousKey)) {
      tabComposerRef.current.set(previousKey, composerStateRef.current);
    }
    const restored = tabComposerRef.current.get(tabKey);
    tabComposerRef.current.delete(tabKey);
    setDraft(restored?.draft ?? '');
    setAttachments((current) => restored?.attachments ?? (current.length > 0 ? [] : current));
  }, [tabKey, setAttachments]);
  /**
   * Clears a sent draft from its tab, which may no longer be the one shown. A tab
   * that now holds something else keeps it.
   */
  const clearTabComposer = (
    key: string,
    sent: { draft: string; attachments: PendingImageAttachment[] }
  ) => {
    const holdsSent = (state?: { draft: string; attachments: PendingImageAttachment[] }) =>
      state?.draft === sent.draft &&
      state.attachments.length === sent.attachments.length &&
      state.attachments.every((attachment, index) => attachment.id === sent.attachments[index].id);
    if (composerTabKeyRef.current === key) {
      if (holdsSent(composerStateRef.current)) {
        setDraft('');
        setAttachments([]);
      }
    } else if (holdsSent(tabComposerRef.current.get(key))) {
      tabComposerRef.current.delete(key);
    }
  };
  const {
    conversations,
    setConversations,
    total: conversationTotal,
    cwdOptions: conversationCWDOptions,
    setCWDOptions: setConversationCWDOptions,
    loading: sidebarLoading,
    refresh: refreshConversations,
    setRunning,
  } = useConversationList({ isSending, optimisticRemoteConversationRef });
  const conversationSearch = useConversationSearch({
    conversations,
    total: conversationTotal,
    loading: sidebarLoading,
    limit: SIDEBAR_CONVERSATION_LIMIT,
    setCWDOptions: setConversationCWDOptions,
  });
  const { isOpen: sidebarSearchOpen, close: handleCloseConversationSearch } = conversationSearch;
  const uiRequest = useUIRequestDialog({
    fallbackConversationId: activeConversationId || conversationId,
    onBlockingRequest: () => {
      contextSettings.interruptDialog();
      handleCloseConversationSearch();
    },
  });
  const uiRequestDialog = uiRequest.dialog;
  const higherPriorityDialogOpen =
    uiRequestDialog !== null ||
    newChatDialogOpen ||
    providerSettingsOpen ||
    infoDialog !== null ||
    sidebarSearchOpen;

  const setConversationRunning = useCallback(
    (id: string | null | undefined, isRunning: boolean) => {
      if (!id) {
        return;
      }

      setRunning(id, isRunning);
      setLocallyRunningConversationIds((currentIds) => {
        if (isRunning) {
          return currentIds.includes(id) ? currentIds : [...currentIds, id];
        }

        return currentIds.filter((currentId) => currentId !== id);
      });

      setConversation((currentConversation) =>
        currentConversation?.id === id ? { ...currentConversation, isRunning } : currentConversation
      );
    },
    [setRunning]
  );

  const markConversationRunning = useCallback(
    (id: string | null | undefined) => setConversationRunning(id, true),
    [setConversationRunning]
  );

  const clearRunningConversation = useCallback(
    (id: string | null | undefined) => setConversationRunning(id, false),
    [setConversationRunning]
  );

  // Resolves to whether the saved conversation replaced the transcript.
  const reconcileCompletedConversation = useCallback(
    async (id: string, isCurrent: () => boolean): Promise<boolean> => {
      try {
        const captionSeq = realtimeCaptionSeq.current;
        const data = await apiService.getConversation(id);
        if (!isCurrent()) return false;
        if (data.isRunning) {
          // Another turn may already have started server-side before its stream
          // header reaches us. Do not replace its live output with a checkpoint.
          markConversationRunning(id);
          return false;
        }
        const normalized = normalizeConversation(data);
        setConversation(normalized);
        setMessages(projectConversation(normalized, captionSeq));
        setConversationRunning(id, Boolean(normalized.isRunning));
        void refreshConversations();
        return true;
      } catch (error) {
        console.error('Failed to refresh completed conversation', error);
        return false;
      }
    },
    [markConversationRunning, projectConversation, refreshConversations, setConversationRunning]
  );

  const replaceRunningConversation = useCallback(
    (previousId: string | null | undefined, nextId: string | null | undefined) => {
      clearRunningConversation(previousId);
      markConversationRunning(nextId);
    },
    [clearRunningConversation, markConversationRunning]
  );

  const registerSendController = useCallback(
    (id: string | null | undefined, controller: AbortController) => {
      if (!id) {
        return;
      }

      runStreams.sends[id] = controller;
    },
    []
  );

  const clearRunningConversationForController = useCallback(
    (id: string | null | undefined, controller: AbortController) => {
      if (!id) {
        return;
      }

      if (runStreams.sends[id] === controller) {
        delete runStreams.sends[id];
        clearRunningConversation(id);
      }
    },
    [clearRunningConversation]
  );

  const replaceQueuedCommands = useCallback((id: string, commands: QueuedCommand[]) => {
    const next = { ...queuedCommandsRef.current };
    if (commands.length > 0) next[id] = commands;
    else delete next[id];
    queuedCommandsRef.current = next;
    setQueuedCommands(next);
  }, []);

  const discardQueuedCommands = useCallback(
    (id: string, reason: string) => {
      if (!queuedCommandsRef.current[id]?.length) return;
      replaceQueuedCommands(id, []);
      const observer = runStreams.commandObservers[id];
      if (observer && observer !== runStreams.viewed) observer.abort();
      delete runStreams.commandObservers[id];
      showToast(`Queued commands for ${id} were discarded: ${reason}`, 'info');
    },
    [replaceQueuedCommands]
  );

  const handleResourcesReloaded = useCallback((event: ChatStreamEvent, id: string) => {
    if (event.kind === 'resources-reloaded') {
      setResourceVersions((current) => ({ ...current, [id]: (current[id] || 0) + 1 }));
    }
  }, []);

  const refreshRunners = useCallback(async () => {
    try {
      const response = await apiService.getRunners();
      setRunners(response.runners || []);
    } catch (error) {
      console.error('Failed to load runners', error);
    }
  }, []);

  useEffect(() => {
    void refreshRunners();

    void apiService
      .getAuthPrincipal()
      .then(setAuthPrincipal)
      .catch((error) => {
        console.error('Failed to load authenticated principal', error);
        setAuthPrincipal(null);
      });

    const runnerRefresh = window.setInterval(() => {
      void refreshRunners();
    }, 5000);

    return () => window.clearInterval(runnerRefresh);
  }, [refreshRunners]);

  useEffect(() => {
    return () => {
      conversationRunVersion.current += 1;
      queuedCommandsRef.current = {};
      abortRunStreams(runStreams);
    };
  }, []);

  const runningConversationIds = useMemo(() => {
    const runningIds = conversations
      .filter((listedConversation) => listedConversation.isRunning)
      .map((listedConversation) => listedConversation.id);

    return Array.from(new Set([...runningIds, ...locallyRunningConversationIds]));
  }, [conversations, locallyRunningConversationIds]);

  // Flags a run that finished while its tab was open but not shown.
  const noteBackgroundRunFinished = useCallback((id: string, activity: TabActivity) => {
    if (viewedConversationIdRef.current === id || !openTabIdsRef.current.has(id)) return;
    setTabActivity((current) =>
      current[id] === activity ? current : { ...current, [id]: activity }
    );
  }, []);

  useEffect(() => {
    if (conversationId) setTabActivity((current) => withoutKey(current, conversationId));
  }, [conversationId]);

  // Ends a run this page saw start, then runs or discards its queued commands.
  const finishObservedRun = (id: string, event: ChatStreamEvent, controller: AbortController) => {
    const owner = runStreams.commandObservers[id];
    if (owner && owner !== controller) return;
    if (event.kind === 'error') noteBackgroundRunFinished(id, 'error');
    else if (!event.cancelled) noteBackgroundRunFinished(id, 'unread');
    if (runStreams.observers[id] === controller) {
      delete runStreams.observers[id];
    }
    clearRunningConversation(id);
    if (event.kind === 'done' && !event.cancelled) runNextQueuedCommand(id);
    else discardQueuedCommands(id, 'the run failed or was cancelled');
  };

  useBackgroundRunMonitors({
    streams: runStreams,
    runningConversationIds,
    viewedConversationId: conversationId,
    markRunning: markConversationRunning,
    clearRunning: clearRunningConversation,
    onResourcesReloaded: handleResourcesReloaded,
    onUIRequest: uiRequest.handleEvent,
    onRunFinished: finishObservedRun,
    discardQueuedCommands,
  });

  const selectedConversationId = conversationId || activeConversationId;
  const activeRunningConversationId =
    selectedConversationId &&
    (runningConversationIds.includes(selectedConversationId) ||
      (conversation?.id === selectedConversationId && conversation.isRunning))
      ? selectedConversationId
      : null;
  const currentConversationIsStreaming = Boolean(activeRunningConversationId);

  const discardDraftWorkspace = useCallback(() => {
    const workspace = draftWorkspaceRef.current;
    draftWorkspaceRef.current = null;
    if (!workspace || workspace.submitted) return false;
    void (async () => {
      let failed = false;
      // The server's deletion guard is per conversation, not per runner.
      for (const runnerId of workspace.runnerIds) {
        try {
          await apiService.discardDraftWorkspace({
            runnerId,
            conversationId: workspace.conversationId,
          });
          discardTerminalPopOuts(runnerId, workspace.conversationId);
        } catch (error) {
          if ((error as { status?: number })?.status === 409) return;
          console.error('Failed to clean up discarded draft sessions', error);
          failed = true;
        }
      }
      if (failed) showToast('Some discarded draft sessions could not be stopped.', 'error');
    })();
    return true;
  }, []);

  useEffect(() => {
    let disposed = false;
    const heartbeat = async () => {
      const workspace = draftWorkspaceRef.current;
      if (disposed || !workspace || workspace.submitted || draftHeartbeatInFlightRef.current)
        return;
      const stillOwnsDraft = () =>
        !disposed && draftWorkspaceRef.current === workspace && !workspace.submitted;
      draftHeartbeatInFlightRef.current = true;
      try {
        for (const runnerId of workspace.runnerIds) {
          if (!stillOwnsDraft()) return;
          try {
            await apiService.heartbeatDraftWorkspace({
              runnerId,
              conversationId: workspace.conversationId,
            });
          } catch (error) {
            if ((error as { status?: number })?.status === 410 && stillOwnsDraft()) {
              discardDraftWorkspace();
              setDraftConversationId(generateConversationId());
              return;
            }
            // Transport failures are left to the runner's authoritative lease expiry.
          }
        }
      } finally {
        draftHeartbeatInFlightRef.current = false;
      }
    };
    const handlePageShow = () => {
      void heartbeat();
    };
    const handleVisibilityChange = () => {
      if (document.visibilityState === 'visible') void heartbeat();
    };
    const heartbeatTimer = window.setInterval(() => {
      void heartbeat();
    }, 30_000);
    window.addEventListener('pageshow', handlePageShow);
    document.addEventListener('visibilitychange', handleVisibilityChange);
    return () => {
      disposed = true;
      window.clearInterval(heartbeatTimer);
      window.removeEventListener('pageshow', handlePageShow);
      document.removeEventListener('visibilitychange', handleVisibilityChange);
    };
  }, [discardDraftWorkspace]);

  useEffect(() => {
    viewedConversationIdRef.current = conversationId;
    routerConversationIdRef.current = conversationId;
    // A new chat kept in its tab keeps its sessions; once the tab is closed, replaced, or
    // started, they are released.
    const discarded =
      conversationId && !openTabIdsRef.current.has(NEW_CHAT_TAB_ID)
        ? discardDraftWorkspace()
        : false;
    // Submitted and abandoned drafts both need a new identity when returning to new chat.
    setDraftConversationId((draftId) =>
      discarded || draftId === conversationId ? generateConversationId() : draftId
    );
  }, [conversationId, discardDraftWorkspace]);

  useEffect(() => {
    const optimisticRemoteConversation = optimisticRemoteConversationRef.current;
    if (
      optimisticRemoteConversation &&
      optimisticRemoteConversation.conversationId !== conversationId
    ) {
      optimisticRemoteConversationRef.current = null;
    }
    if (
      conversationId &&
      (conversationPathOverrideRef.current === `/c/${conversationId}` ||
        optimisticRemoteConversation?.conversationId === conversationId)
    ) {
      return;
    }
    conversationPathOverrideRef.current = null;
    resetTranscriptScroll();

    runStreams.viewedGeneration += 1;
    setActiveConversationId(conversationId);
    resetExtensionWidgets();
    setSteering(false);
    setStreamError(null);

    if (
      runStreams.viewed &&
      !Object.values(runStreams.commandObservers).includes(runStreams.viewed)
    ) {
      runStreams.viewed.abort();
    }
    runStreams.viewed = null;

    if (!conversationId) {
      setConversation(null);
      setMessages([]);
      setConversationError(null);
      return;
    }

    setConversationLoading(true);
    setConversationError(null);

    const captionSeq = realtimeCaptionSeq.current;
    void apiService
      .getConversation(conversationId)
      .then((data) => {
        const normalizedConversation = normalizeConversation(data);
        setActiveConversationId(normalizedConversation.id);
        setConversation(normalizedConversation);
        setMessages(projectConversation(normalizedConversation, captionSeq));
      })
      .catch((error: unknown) => {
        const message = error instanceof Error ? error.message : 'Failed to load conversation';
        setConversationError(message);
      })
      .finally(() => {
        setConversationLoading(false);
      });
  }, [conversationId, resetExtensionWidgets, resetTranscriptScroll, projectConversation]);

  useViewedConversationStream({
    conversationId,
    loaded: !conversationLoading && loadedConversationId === conversationId,
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
    handleUIRequest: uiRequest.handleEvent,
    handleExtensionWidgetEvent,
    setActiveConversationId,
    setConversation,
    setMessages,
    setStreamError,
  });

  // biome-ignore lint/correctness/useExhaustiveDependencies(messages): New transcript content triggers scrolling when the reader is following the bottom.
  // biome-ignore lint/correctness/useExhaustiveDependencies(currentConversationIsStreaming): Starting or stopping the streaming indicator changes the transcript height.
  // biome-ignore lint/correctness/useExhaustiveDependencies(conversationLoading): Replacing the loading view with the transcript changes its height.
  useLayoutEffect(() => {
    followTranscript();
  }, [messages, currentConversationIsStreaming, conversationLoading, followTranscript]);

  const navigateToTab = (id: string) => {
    startTransition(() => {
      navigate(id === NEW_CHAT_TAB_ID ? '/' : `/c/${id}`);
    });
  };

  const handleNewChat = () => {
    if (conversationId && conversationTabs.tabs.some((tab) => tab.id === NEW_CHAT_TAB_ID)) {
      // One new chat at a time: return to it with its settings and unsent text.
      closeMobileSidebar();
      setStreamError(null);
      navigateToTab(NEW_CHAT_TAB_ID);
      return;
    }
    discardDraftWorkspace();
    closeMobileSidebar();
    setConversation(null);
    optimisticRemoteConversationRef.current = null;
    setActiveConversationId(null);
    setDraftConversationId(generateConversationId());
    setMessages([]);
    resetExtensionWidgets();
    setConversationError(null);
    setStreamError(null);
    contextSettings.resetForNewChat();
    startTransition(() => {
      navigate('/');
    });
  };

  const handleSelectConversation = (nextConversationId: string) => {
    closeMobileSidebar();
    if (nextConversationId === conversationId) {
      return;
    }

    setStreamError(null);
    startTransition(() => {
      navigate(`/c/${nextConversationId}`);
    });
  };

  /** Releases the new chat's terminal and browser sessions and gives the next one a fresh identity. */
  const abandonNewChat = () => {
    discardDraftWorkspace();
    setDraftConversationId(generateConversationId());
    tabComposerRef.current.delete(NEW_CHAT_TAB_ID);
  };

  // Opening from the sidebar or search replaces an untouched new chat, like a browser's new tab page.
  const handleOpenConversation = (nextConversationId: string) => {
    if (
      !conversationId &&
      !draft.trim() &&
      attachments.length === 0 &&
      !draftWorkspaceRef.current?.runnerIds.size
    ) {
      if (conversationTabs.tabs.some((tab) => tab.id === nextConversationId)) {
        conversationTabs.close(NEW_CHAT_TAB_ID);
      } else {
        replaceConversationTab(NEW_CHAT_TAB_ID, nextConversationId);
      }
    }
    handleSelectConversation(nextConversationId);
  };

  const handleSelectTab = (id: string) => {
    if (id !== NEW_CHAT_TAB_ID) {
      handleSelectConversation(id);
    } else if (conversationId) {
      closeMobileSidebar();
      setStreamError(null);
      navigateToTab(NEW_CHAT_TAB_ID);
    }
  };

  // Closing a tab only hides the conversation; a running agent keeps running.
  const handleCloseTab = (id: string) => {
    const nextTabId = conversationTabs.close(id);
    tabComposerRef.current.delete(id);
    setTabActivity((current) => withoutKey(current, id));
    if (id === NEW_CHAT_TAB_ID) abandonNewChat();
    if (nextTabId === undefined) return;
    if (nextTabId === NEW_CHAT_TAB_ID && !conversationId) {
      // The only tab was the new chat: start it over.
      setDraft('');
      setAttachments([]);
      return;
    }
    if (nextTabId === NEW_CHAT_TAB_ID) {
      setStreamError(null);
      navigateToTab(NEW_CHAT_TAB_ID);
      return;
    }
    handleSelectConversation(nextTabId);
  };

  const [tabConfigRequested, setTabConfigRequested] = useState(false);
  const handleOpenTabConfig = (id: string) => {
    if (id !== NEW_CHAT_TAB_ID) return;
    // Open the new chat settings once the new chat is shown.
    setTabConfigRequested(true);
    if (conversationId) handleSelectTab(id);
  };

  const handleSelectSearchResult = (nextConversationId: string) => {
    const selectedConversation = conversationSearch.results.find(
      (searchResult) => searchResult.id === nextConversationId
    );
    if (selectedConversation) {
      setConversations((currentConversations) => {
        const existingConversation = currentConversations.find(
          (currentConversation) => currentConversation.id === nextConversationId
        );
        const nextConversation = existingConversation?.isRunning
          ? { ...selectedConversation, isRunning: true }
          : selectedConversation;
        return upsertConversationSummary(currentConversations, nextConversation);
      });
    }
    handleCloseConversationSearch();
    handleOpenConversation(nextConversationId);
  };

  const handleForkConversation = async (sourceConversationId: string) => {
    try {
      const response = await apiService.forkConversation(sourceConversationId);
      await refreshConversations();
      showToast('Conversation copied', 'success');
      closeMobileSidebar();
      startTransition(() => {
        navigate(`/c/${response.conversation_id}`);
      });
    } catch (error) {
      const message = error instanceof Error ? error.message : 'Failed to copy conversation';
      showToast(message, 'error');
    }
  };

  const handleDeleteConversation = async (targetConversationId: string) => {
    if (runningConversationIds.includes(targetConversationId)) {
      showToast('Stop the active conversation before deleting it', 'info');
      return;
    }

    try {
      await apiService.deleteConversation(targetConversationId);
      const nextTabId = conversationTabs.close(targetConversationId);
      tabComposerRef.current.delete(targetConversationId);
      setTabActivity((current) => withoutKey(current, targetConversationId));

      if (
        targetConversationId === conversationId ||
        targetConversationId === activeConversationId ||
        runningConversationIds.includes(targetConversationId)
      ) {
        if (optimisticRemoteConversationRef.current?.conversationId === targetConversationId) {
          optimisticRemoteConversationRef.current = null;
        }
        const sendController = runStreams.sends[targetConversationId];
        if (sendController) {
          sendController.abort();
          delete runStreams.sends[targetConversationId];
          if (runStreams.viewedSend === sendController) {
            runStreams.viewedSend = null;
          }
        }
        runStreams.viewed?.abort();
        setConversation(null);
        setActiveConversationId(null);
        setMessages([]);
        setConversationError(null);
        setStreamError(null);
        clearRunningConversation(targetConversationId);
        navigateToTab(nextTabId ?? NEW_CHAT_TAB_ID);
      }

      await refreshConversations();
      showToast('Conversation deleted', 'neutral');
    } catch (error) {
      const message = error instanceof Error ? error.message : 'Failed to delete conversation';
      showToast(message, 'error');
    }
  };

  const handleOpenSidebarSearch = () => {
    closeMobileSidebar();
    conversationSearch.open();
  };

  const updatePathForStartedConversation = (streamedId: string) => {
    const nextPath = `/c/${streamedId}`;

    // The new chat tab becomes the started conversation in place.
    replaceConversationTab(routeTabKey(routerConversationIdRef.current), streamedId);

    conversationPathOverrideRef.current = nextPath;
    viewedConversationIdRef.current = streamedId;
    routerConversationIdRef.current = streamedId;
    startTransition(() => {
      navigate(nextPath, { replace: true });
    });
  };

  const handleUIInputRequest = uiRequest.handleEvent;

  const handleSubmit = async () => {
    const prompt = draft.trim();
    if (slashCommandName(prompt) === 'about') {
      setDraft('');
      setInfoDialog('about');
      return;
    }
    if (realtime.enabled && /^\/talk(?:\s|$)/.test(prompt)) {
      // Local call control is never submitted as a coding turn or queued recipe.
      if (!realtime.isBusy()) {
        const started = realtime.start();
        if (!realtime.blockedReason && !dictation.isBusy()) setDraft('');
        await started;
      }
      return;
    }
    if (steering || !isRemoteConversation) return;
    if (dictation.enabled && isVoiceCommand(prompt) && realtime.isBusy()) {
      showToast('End the call before dictating.', 'info');
      return;
    }
    // Dictation is a client-local input action, never a queued command or turn.
    if (dictation.handleCommand(prompt) || dictation.isBusy()) return;
    const attachmentsForSend = attachments;
    if (!prompt && attachments.length === 0) {
      return;
    }
    if (currentConversationIsStreaming && !prompt) {
      showToast('Steering requires a text message', 'error');
      return;
    }

    if (currentConversationIsStreaming) {
      const targetConversationId = activeRunningConversationId;
      if (!targetConversationId) {
        return;
      }

      if (isQueueableCommand(prompt)) {
        if (
          !runStreams.sends[targetConversationId] &&
          !runStreams.commandObservers[targetConversationId]
        ) {
          const observer = runStreams.observers[targetConversationId];
          if (!observer || observer.signal.aborted) {
            showToast(
              'Wait for the active conversation stream to connect before queueing commands',
              'info'
            );
            return;
          }
          runStreams.commandObservers[targetConversationId] = observer;
        }
        replaceQueuedCommands(targetConversationId, [
          ...(queuedCommandsRef.current[targetConversationId] || []),
          { prompt, attachments: attachmentsForSend },
        ]);
        dictation.dismissError();
        setDraft('');
        setAttachments([]);
        setStreamError(null);
        showToast('Command queued until the current run finishes successfully', 'success');
        return;
      }

      setSteering(true);
      setStreamError(null);
      const steeringTabKey = composerTabKeyRef.current;
      const steeringDraft = { draft, attachments: attachmentsForSend };

      try {
        const queuedContent = buildUserContent(prompt, attachmentsForSend);
        await apiService.steerConversation(targetConversationId, prompt, queuedContent);
        dictation.dismissError();
        setConversation((currentConversation) =>
          currentConversation?.id === targetConversationId
            ? {
                ...currentConversation,
                pendingSteer: [
                  ...(currentConversation.pendingSteer || []),
                  { role: 'user', content: queuedContent },
                ],
              }
            : currentConversation
        );
        clearTabComposer(steeringTabKey, steeringDraft);
        showToast('Steering queued for the active conversation', 'success');
      } catch (error) {
        const message = error instanceof Error ? error.message : 'Failed to steer conversation';
        setStreamError(message);
        showToast(message, 'error');
      } finally {
        setSteering(false);
      }

      return;
    }

    dictation.dismissError();
    setDraft('');
    setAttachments([]);
    await sendMessage(prompt, attachmentsForSend);
  };

  const handleStop = () => {
    const conversationToStop = activeRunningConversationId;
    if (!conversationToStop) {
      return;
    }

    discardQueuedCommands(conversationToStop, 'the run was stopped');
    const sendController = runStreams.sends[conversationToStop];
    if (sendController) {
      sendController.abort();
      delete runStreams.sends[conversationToStop];
      if (runStreams.viewedSend === sendController) {
        runStreams.viewedSend = null;
      }
    } else {
      runStreams.viewed?.abort();
    }
    setSteering(false);
    clearRunningConversation(conversationToStop);
    uiRequest.close();
    void apiService.stopConversation(conversationToStop).catch((error) => {
      console.error('Failed to stop conversation', error);
    });
    showToast('Stopped the active conversation', 'info');
  };

  const isStartedConversationPending =
    Boolean(conversationId) && conversationPathOverrideRef.current === `/c/${conversationId}`;
  const isStartedConversationAwaitingLoad =
    isStartedConversationPending && loadedConversationId !== conversationId;
  const hasOptimisticRemoteConversation =
    Boolean(conversationId) &&
    optimisticRemoteConversationRef.current?.conversationId === conversationId;
  const optimisticConversationContextEditable =
    hasOptimisticRemoteConversation && optimisticRemoteConversationRef.current?.confirmed === false;
  const conversationMatchesRoute = !conversationId || conversation?.id === conversationId;
  const workspaceConversation =
    conversationMatchesRoute || isStartedConversationAwaitingLoad ? conversation : null;
  const currentProfileLabel = conversationId
    ? workspaceConversation?.profile?.trim() || ''
    : selectedProfile;
  const currentModelLabel = conversationId
    ? workspaceConversation?.model?.trim() || ''
    : selectedModel;
  const currentReasoningEffortLabel = conversationId
    ? workspaceConversation?.reasoningEffort || ''
    : selectedReasoningEffort;
  const currentRunnerID = conversationId ? workspaceConversation?.runnerId || '' : selectedRunnerID;
  const currentRunner = useMemo(
    () => runners.find((runner) => runner.id === currentRunnerID) || workspaceConversation?.runner,
    [currentRunnerID, runners, workspaceConversation?.runner]
  );
  const isRemoteConversation = Boolean(currentRunnerID);
  const terminalAuthorized = Boolean(
    authPrincipal?.roles.includes('terminal') || authPrincipal?.roles.includes('admin')
  );
  const runnerWorkspaceAvailable = Boolean(
    currentRunner?.connected && (currentRunner.status === 'idle' || currentRunner.status === 'busy')
  );
  const discoveryConversationID =
    conversationId && !hasOptimisticRemoteConversation ? conversationId : undefined;
  const remoteWorkspaceConversationID = isRemoteConversation ? discoveryConversationID : undefined;
  const discoveryProfile = discoveryConversationID ? undefined : selectedProfile;
  const currentEnvironmentProfile = conversationId
    ? conversation?.environmentProfile || ''
    : selectedEnvironmentProfile;

  const currentCWDLabel = useMemo(() => {
    if (currentRunnerID) {
      if (conversationId) {
        return workspaceConversation?.cwd || currentRunner?.workspace.path || 'Remote runner';
      }
      return selectedCWD || currentRunner?.workspace.path || 'Remote runner';
    }
    if (isStartedConversationAwaitingLoad) {
      return selectedCWD || chatSettings.defaultCWD || '';
    }

    if (conversationId) {
      return workspaceConversation?.cwd || chatSettings.defaultCWD || '';
    }
    return selectedCWD || chatSettings.defaultCWD || '';
  }, [
    chatSettings.defaultCWD,
    conversationId,
    currentRunner?.workspace.path,
    currentRunnerID,
    isStartedConversationAwaitingLoad,
    selectedCWD,
    workspaceConversation?.cwd,
  ]);
  const { sendMessage, runNextQueuedCommand } = createMessageSender({
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
  });

  const runnerDirectoryAvailable =
    currentCWDLabel === currentRunner?.workspace.path || Boolean(currentRunner?.workspaceCwd);
  const workspaceTerminalAvailable = Boolean(
    isRemoteConversation &&
      terminalAuthorized &&
      runnerWorkspaceAvailable &&
      runnerDirectoryAvailable &&
      currentRunner?.workspaceTerminal
  );
  const workspaceGitDiffAvailable = Boolean(
    isRemoteConversation &&
      runnerWorkspaceAvailable &&
      runnerDirectoryAvailable &&
      currentRunner?.workspaceGitDiff
  );
  const workspaceBrowserAvailable = Boolean(
    isRemoteConversation &&
      terminalAuthorized &&
      runnerWorkspaceAvailable &&
      runnerDirectoryAvailable &&
      // Only the live runner listing carries the server's effective browser permission.
      runners.some((runner) => runner.id === currentRunnerID && runner.workspaceBrowser)
  );
  const workspaceToolsAvailable =
    workspaceTerminalAvailable || workspaceGitDiffAvailable || workspaceBrowserAvailable;

  // Drafts address their selected directory directly until saved affinity is available.
  const draftWorkspaceCWD =
    !remoteWorkspaceConversationID && currentCWDLabel !== currentRunner?.workspace.path
      ? currentCWDLabel
      : undefined;
  const workspaceTarget = useMemo<WorkspaceTarget>(
    () => ({
      kind: 'runner',
      runnerId: currentRunnerID,
      conversationId: remoteWorkspaceConversationID,
      cwd: currentCWDLabel,
    }),
    [currentRunnerID, remoteWorkspaceConversationID, currentCWDLabel]
  );
  const workspaceTargetKey = `runner:${currentRunnerID}:conversation:${remoteWorkspaceConversationID || ''}:cwd:${currentCWDLabel}:generation:${currentRunner?.generation || 0}`;
  workspaceTargetKeyRef.current = workspaceTargetKey;

  const terminalTarget = useMemo<WorkspaceTarget>(
    () => ({
      kind: 'runner',
      runnerId: currentRunnerID,
      conversationId: sessionConversationId,
      cwd: currentCWDLabel,
    }),
    [currentRunnerID, sessionConversationId, currentCWDLabel]
  );
  const browserTarget = useMemo<BrowserTarget>(
    () => ({
      runnerId: currentRunnerID,
      conversationId: sessionConversationId,
      cwd: draftWorkspaceCWD,
    }),
    [currentRunnerID, sessionConversationId, draftWorkspaceCWD]
  );
  const sessionTargetKey = `runner:${currentRunnerID}:conversation:${sessionConversationId}:cwd:${currentCWDLabel}:generation:${currentRunner?.generation || 0}`;

  const dictationTargetKey = `runner:${currentRunnerID}:conversation:${sessionConversationId}:cwd:${currentCWDLabel}:environment:${currentEnvironmentProfile}`;
  const insertDictation = useEffectEvent(
    (text: string, startingDraft: string, caret: number, fromCommand: boolean) => {
      const unchanged = draft === startingDraft;
      const base = fromCommand && unchanged ? '' : draft;
      const position = unchanged ? Math.min(caret, base.length) : base.length;
      const before = base.slice(0, position);
      const after = base.slice(position);
      const insertion = `${before && !/\s$/.test(before) ? ' ' : ''}${text}${after && !/^\s/.test(after) ? ' ' : ''}`;
      pendingCaret.current = before.length + insertion.length;
      setDraft(before + insertion + after);
    }
  );
  const dictation = useDictation({
    targetKey: dictationTargetKey,
    onStart: (fromCommand) => {
      const startingDraft = draft;
      const caret = composerRef.current?.selectionStart ?? draft.length;
      dictationOwner.current = dictationTargetKey;
      pendingCaret.current = null;
      return (text) => insertDictation(text, startingDraft, caret, fromCommand);
    },
  });
  const realtime = useRealtime({
    targetKey: `${dictationTargetKey}:profile:${currentProfileLabel}:model:${currentModelLabel}:effort:${currentReasoningEffortLabel}`,
    isDraft: !conversationId,
    // Saved affinity supplies the coding profile/model independently of the
    // server's realtime profile, just like a normal turn in this conversation.
    chat: {
      message: '',
      conversationId: sessionConversationId,
      ...(!conversationId && {
        runnerId: selectedRunnerID || undefined,
        environmentProfile: selectedEnvironmentProfile || undefined,
        cwd: selectedCWD.trim() || undefined,
        profile: selectedProfile,
        options: selectedModel ? { model: selectedModel } : undefined,
        reasoningEffort: chatSettingsLoaded ? selectedReasoningEffort : undefined,
      }),
      clientCapabilities: {
        interactiveUI: true,
        persistentWidgets: true,
        persistentSurfaces: false,
      },
    },
    blockedReason:
      conversationId && (conversationLoading || !conversation || hasOptimisticRemoteConversation)
        ? 'Wait for the conversation to load.'
        : !isRemoteConversation || (!conversationId && !runnerWorkspaceAvailable)
          ? 'Select an available workspace runner to start a call.'
          : !conversationId && !chatSettingsLoaded
            ? 'Wait for the chat settings to load.'
            : undefined,
    isCaptureBusy: dictation.isBusy,
    onStarted: async (id, signal) => {
      if (conversationId) return;
      const captionSeq = realtimeCaptionSeq.current;
      const saved = normalizeConversation(await apiService.getConversation(id, signal));
      if (signal.aborted) return;
      if (draftWorkspaceRef.current?.conversationId === id) {
        draftWorkspaceRef.current.submitted = true;
      }
      setActiveConversationId(id);
      setConversation(saved);
      setMessages(projectConversation(saved, captionSeq));
      setConversationError(null);
      setConversations((current) => upsertConversationSummary(current, saved));
      updatePathForStartedConversation(id);
    },
    onEvent: (event) => {
      const viewedId = viewedConversationIdRef.current;
      if (event.conversationId && event.conversationId !== viewedId) return;
      if (event.kind === 'transcript') {
        if (event.id) {
          const previous = realtimeCaptions.current.get(event.id);
          realtimeCaptionSeq.current += 1;
          realtimeCaptions.current.set(event.id, {
            event: {
              ...event,
              anchor: previous ? previous.event.anchor : event.anchor,
              conversationId: viewedId || undefined,
            },
            seq: realtimeCaptionSeq.current,
          });
        }
        setMessages((current) => applyRealtimeTranscript(current, event));
      } else if (event.kind === 'agent-started' && event.conversationId) {
        if (
          realtimeTurn.current?.conversationId !== event.conversationId ||
          realtimeTurn.current?.turnId !== event.turnId
        ) {
          conversationRunVersion.current += 1;
          realtimeTurn.current = { conversationId: event.conversationId, turnId: event.turnId };
        }
        markConversationRunning(event.conversationId);
        // Healthy idle watchers already observe future turns. Reuse the normal
        // attachment effect only when that observer has actually gone away.
        if (
          (!runStreams.viewed || runStreams.viewed.signal.aborted) &&
          !runStreams.sends[event.conversationId]
        ) {
          setStreamError(null);
          setConversationStreamVersion((version) => version + 1);
        }
      } else if (
        event.kind === 'agent-completed' &&
        event.conversationId &&
        realtimeTurn.current?.conversationId === event.conversationId &&
        realtimeTurn.current?.turnId === event.turnId
      ) {
        const id = event.conversationId;
        const runVersion = conversationRunVersion.current;
        void reconcileCompletedConversation(
          id,
          () =>
            viewedConversationIdRef.current === id &&
            conversationRunVersion.current === runVersion &&
            !runStreams.sends[id]
        );
      }
    },
  });
  const modalOpen = higherPriorityDialogOpen || dictation.busy;
  const layout = useChatLayout(modalOpen, {
    terminal: workspaceTerminalAvailable,
    diff: workspaceGitDiffAvailable,
    browser: workspaceBrowserAvailable,
  });
  const {
    mobileLayout,
    workspaceOverlayLayout,
    sidebarVisible,
    setSidebarVisible,
    sidebarWidth,
    isResizingSidebar,
    sidebarShellRef,
    workspacePanelView,
    setWorkspacePanelView,
    sidebarOverlayOpen,
    workspaceOverlayOpen,
    closeMobileSidebar,
    handleSidebarToggle,
    handleSidebarResizeStart,
    handleSidebarResizeKeyDown,
  } = layout;
  const workspaceResize = useWorkspaceResize(layout, workspaceToolsAvailable, modalOpen);

  useEffect(() => {
    if (conversationId) return;
    if (draftWorkspaceRef.current?.conversationId !== draftConversationId) {
      draftWorkspaceRef.current = {
        conversationId: draftConversationId,
        runnerIds: new Set(),
        submitted: false,
      };
    }
    if (
      !draftWorkspaceRef.current.submitted &&
      currentRunner?.workspaceSessionCleanup &&
      ((workspacePanelView === 'terminal' && workspaceTerminalAvailable) ||
        (workspacePanelView === 'browser' && workspaceBrowserAvailable))
    ) {
      draftWorkspaceRef.current.runnerIds.add(currentRunnerID);
    }
  }, [
    conversationId,
    draftConversationId,
    currentRunnerID,
    currentRunner?.workspaceSessionCleanup,
    workspacePanelView,
    workspaceTerminalAvailable,
    workspaceBrowserAvailable,
  ]);

  const cancelDictation = dictation.cancel;
  useLayoutEffect(() => {
    if (higherPriorityDialogOpen && dictation.busy) cancelDictation();
  }, [higherPriorityDialogOpen, dictation.busy, cancelDictation]);

  const restoreDictationFocus = useEffectEvent(() => {
    const editor = composerRef.current;
    if (
      !editor ||
      editor.disabled ||
      editor.closest('[inert]') ||
      higherPriorityDialogOpen ||
      dictation.isBusy() ||
      dictationOwner.current !== dictationTargetKey
    )
      return;
    editor.focus();
    if (pendingCaret.current !== null) {
      editor.setSelectionRange(pendingCaret.current, pendingCaret.current);
      pendingCaret.current = null;
    }
  });

  useEffect(() => {
    if (!composerFocusRequested || modalOpen || sidebarOverlayOpen || workspaceOverlayOpen) return;
    // Run after overlay focus restoration so closing a panel cannot steal focus back.
    const timer = window.setTimeout(() => {
      if (!composerRef.current?.disabled) composerRef.current?.focus();
      setComposerFocusRequested(false);
    }, 0);
    return () => window.clearTimeout(timer);
  }, [composerFocusRequested, modalOpen, sidebarOverlayOpen, workspaceOverlayOpen]);

  const slashCommands = useSlashCommands({
    draft,
    setDraft,
    // Discovered commands can queue during a run; keep their suggestions visible.
    disabled: steering,
    dictationEnabled: dictation.enabled,
    realtimeEnabled: realtime.enabled,
    discoveryAvailable: Boolean(
      isRemoteConversation && runnerWorkspaceAvailable && currentRunner?.workspaceDiscovery
    ),
    runnerId: currentRunnerID,
    generation: currentRunner?.generation,
    refreshVersion: resourceVersions[remoteWorkspaceConversationID || ''] || 0,
    conversationId: remoteWorkspaceConversationID,
    cwd: currentCWDLabel,
    environmentProfile: currentEnvironmentProfile,
    profile: discoveryProfile,
    onSubmit: handleSubmit,
  });
  const slashCommandNames = useMemo(
    () => new Set(slashCommands.commands.map((command) => command.name)),
    [slashCommands.commands]
  );
  // Only discovered commands wait for the run to finish. Other slash-prefixed
  // text, such as a file path, is ordinary steering.
  const isQueueableCommand = (message: string): boolean => {
    const name = slashCommandName(message);
    return (
      name !== undefined &&
      name !== 'about' &&
      !(dictation.enabled && name === 'voice') &&
      !(realtime.enabled && name === 'talk') &&
      slashCommandNames.has(name)
    );
  };

  const composerModelLabel = currentModelLabel
    ? [currentProfileLabel, currentModelLabel].filter(Boolean).join('/')
    : '';
  const composerContextText =
    [composerModelLabel, currentReasoningEffortLabel].filter(Boolean).join(' · ') ||
    (conversationId ? 'Saved settings' : 'Model settings');
  const contextIsStatic = Boolean(conversationId) && !optimisticConversationContextEditable;
  const openChatContext = () => contextSettings.openDialog(currentProfileLabel);
  const openRequestedTabConfig = useEffectEvent(() => contextSettings.openDialog(selectedProfile));
  useEffect(() => {
    if (!tabConfigRequested || conversationId) return;
    setTabConfigRequested(false);
    openRequestedTabConfig();
  }, [tabConfigRequested, conversationId]);

  const patchOptimisticConversationContext = (update: Partial<Conversation>) => {
    const optimisticConversation = optimisticRemoteConversationRef.current;
    if (
      !conversationId ||
      optimisticConversation?.conversationId !== conversationId ||
      optimisticConversation.confirmed
    ) {
      return;
    }
    setConversation((currentConversation) =>
      currentConversation?.id === conversationId
        ? { ...currentConversation, ...update }
        : currentConversation
    );
    setConversations((currentConversations) =>
      currentConversations.map((currentConversation) =>
        currentConversation.id === conversationId
          ? { ...currentConversation, ...update }
          : currentConversation
      )
    );
  };

  const handleQuickModelChange = (value: string) => {
    const update = contextSettings.selectModel(value);
    if (update) patchOptimisticConversationContext(update);
  };

  const handleQuickReasoningEffortChange = (effort: string) => {
    patchOptimisticConversationContext(contextSettings.selectReasoningEffort(effort));
  };

  const composerQuickPick =
    !contextIsStatic && contextSettings.quickPick
      ? {
          ...contextSettings.quickPick,
          onModelChange: handleQuickModelChange,
          onReasoningEffortChange: handleQuickReasoningEffortChange,
        }
      : undefined;

  const canSubmit =
    isRemoteConversation &&
    (currentConversationIsStreaming
      ? draft.trim().length > 0
      : draft.trim().length > 0 || attachments.length > 0);
  const composerPlaceholder = !isRemoteConversation
    ? conversationId
      ? 'This local conversation is read-only'
      : 'Select a workspace runner to start'
    : currentConversationIsStreaming
      ? 'Ask kodelet...'
      : slashCommands.placeholder || 'Ask kodelet...';
  const workspaceExecutionMessage = !isRemoteConversation
    ? conversationId
      ? 'This conversation uses the disabled control-plane workspace and is read-only.'
      : 'The control-plane workspace is disabled. Select a workspace runner to start a chat.'
    : null;
  const submitActionLabel =
    slashCommandName(draft) === 'about'
      ? 'Open About Kodelet'
      : steering
        ? 'Queueing…'
        : currentConversationIsStreaming
          ? isQueueableCommand(draft)
            ? 'Queue command'
            : 'Steer'
          : 'Send';
  const pendingSteerMessages = conversation?.pendingSteer || [];
  const pendingCommandMessages = (queuedCommands[selectedConversationId || ''] || []).map(
    (command) => ({
      role: 'user' as const,
      content: buildUserContent(command.prompt, command.attachments),
    })
  );

  const conversationSummaries = useMemo(
    () => new Map(conversations.map((listed) => [listed.id, listed])),
    [conversations]
  );
  const draftTabRunner = runners.find((runner) => runner.id === selectedRunnerID);
  const draftTabCWD = selectedRunnerID
    ? selectedCWD || draftTabRunner?.workspace.path || ''
    : selectedCWD || chatSettings.defaultCWD || '';
  const tabSummaries = useMemo(
    () =>
      conversationTabs.tabs.flatMap((tab) => {
        const summary =
          conversationSummaries.get(tab.id) ?? (conversation?.id === tab.id ? conversation : null);
        return summary
          ? [{ id: tab.id, title: previewConversation(summary), cwd: summary.cwd }]
          : [];
      }),
    [conversation, conversationSummaries, conversationTabs.tabs]
  );
  const { remember: rememberConversationTabs } = conversationTabs;
  useEffect(() => {
    // Keep titles for tabs whose conversation later leaves the sidebar list.
    rememberConversationTabs(tabSummaries);
  }, [rememberConversationTabs, tabSummaries]);
  const knownTabSummaries = new Map(tabSummaries.map((summary) => [summary.id, summary]));
  const conversationTabItems: ConversationTab[] = conversationTabs.tabs.map((tab) => {
    if (tab.id === NEW_CHAT_TAB_ID) {
      return { id: tab.id, title: 'New chat', cwd: draftTabCWD || undefined, draft: true };
    }
    const summary = knownTabSummaries.get(tab.id);
    return {
      id: tab.id,
      title:
        summary?.title ||
        tab.title ||
        (conversationLoading && tab.id === conversationId ? 'Loading…' : 'Untitled conversation'),
      cwd: summary?.cwd || tab.cwd,
      status:
        uiRequestDialog?.request.conversationId === tab.id
          ? 'attention'
          : runningConversationIds.includes(tab.id) || activeRunningConversationId === tab.id
            ? 'running'
            : (tabActivity[tab.id] ?? 'idle'),
    };
  });

  const fetchGitDiff = async () => {
    const requestID = ++gitDiffRequestRef.current;
    const requestTargetKey = workspaceTargetKey;
    setGitDiffLoading(true);
    setGitDiffError(null);

    try {
      const response = await apiService.getGitDiff(workspaceTarget);
      if (
        requestID === gitDiffRequestRef.current &&
        requestTargetKey === workspaceTargetKeyRef.current
      ) {
        setGitDiff(response);
      }
    } catch (error) {
      const message = error instanceof Error ? error.message : 'Failed to load git diff';
      if (
        requestID === gitDiffRequestRef.current &&
        requestTargetKey === workspaceTargetKeyRef.current
      ) {
        setGitDiffError(message);
        setGitDiff(null);
      }
    } finally {
      if (
        requestID === gitDiffRequestRef.current &&
        requestTargetKey === workspaceTargetKeyRef.current
      ) {
        setGitDiffLoading(false);
      }
    }
  };

  const onWorkspaceTargetChange = useEffectEvent(() => {
    if (workspacePanelView === 'diff' && workspaceGitDiffAvailable) {
      void fetchGitDiff();
    }
  });

  // biome-ignore lint/correctness/useExhaustiveDependencies(workspaceTargetKey): A workspace change invalidates cached diff requests; opening or refreshing the panel is handled separately.
  useEffect(() => {
    gitDiffRequestRef.current += 1;
    setGitDiff(null);
    setGitDiffError(null);
    setGitDiffLoading(false);
    onWorkspaceTargetChange();
  }, [workspaceTargetKey]);

  const handleToggleWorkspacePanel = () => {
    if (workspacePanelView === null) {
      if (workspaceOverlayLayout) {
        setSidebarVisible(false);
      }
      if (workspaceTerminalAvailable) {
        setWorkspacePanelView('terminal');
      } else if (workspaceGitDiffAvailable) {
        setWorkspacePanelView('diff');
        void fetchGitDiff();
      } else if (workspaceBrowserAvailable) {
        setWorkspacePanelView('browser');
      }
      return;
    }

    setWorkspacePanelView(null);
  };

  const handleSelectGitDiffPanel = () => {
    if (!workspaceGitDiffAvailable || workspacePanelView === 'diff') {
      return;
    }

    setWorkspacePanelView('diff');
    void fetchGitDiff();
  };

  const handleSelectTerminalPanel = () => {
    if (workspaceTerminalAvailable) {
      if (workspaceOverlayLayout) setSidebarVisible(false);
      setWorkspacePanelView('terminal');
    }
  };

  const handleShortcut = useEffectEvent((event: KeyboardEvent) => {
    if (event.defaultPrevented || event.isComposing || higherPriorityDialogOpen || dictation.busy) {
      return;
    }

    let action: (() => void) | undefined;
    const tabStep = conversationTabStep(event);
    if (tabStep !== 0) {
      const target = event.target instanceof Element ? event.target : null;
      // Shells own their keys, and on macOS Option+[ and ] type quotation marks.
      if (
        target?.closest('.workspace-terminal-host') ||
        (isMacPlatform() &&
          target?.closest('input, textarea, [contenteditable]:not([contenteditable="false"])'))
      ) {
        return;
      }
      action = () => {
        const nextTabId = adjacentConversationTabId(conversationTabItems, tabKey, tabStep);
        if (nextTabId && nextTabId !== tabKey) handleSelectTab(nextTabId);
      };
    } else if (event.altKey) {
      if (
        realtime.isBusy() ||
        !event.shiftKey ||
        event.ctrlKey ||
        event.metaKey ||
        steering ||
        !isRemoteConversation ||
        (event.target instanceof Element && event.target.closest('.workspace-terminal-host'))
      ) {
        return;
      }
      // Option changes event.key on macOS; use the physical key as well.
      if (dictation.enabled && (event.code === 'KeyV' || event.key.toLowerCase() === 'v')) {
        action = dictation.start;
      } else if (
        realtime.enabled &&
        !realtime.blockedReason &&
        (event.code === 'KeyR' || event.key.toLowerCase() === 'r')
      ) {
        action = () => void realtime.start();
      }
    } else if (
      event.ctrlKey &&
      !event.metaKey &&
      !event.shiftKey &&
      (event.key === '`' || event.code === 'Backquote')
    ) {
      if (!workspaceTerminalAvailable) return;
      action = () => {
        if (workspacePanelView === 'terminal') {
          setWorkspacePanelView(null);
          setComposerFocusRequested(true);
        } else {
          handleSelectTerminalPanel();
        }
      };
    } else {
      // Shell applications (notably tmux's Ctrl+B prefix) own all other keys.
      if (event.target instanceof Element && event.target.closest('.workspace-terminal-host')) {
        return;
      }
      if (event.ctrlKey === event.metaKey) return;
      const key = event.key.toLowerCase();
      if (!event.shiftKey && key === 'b') {
        action = handleSidebarToggle;
      } else if (!event.shiftKey && key === 'k') {
        action = handleOpenSidebarSearch;
      } else if (event.shiftKey && key === 'o') {
        action = handleNewChat;
      } else if (event.shiftKey && key === 'l') {
        action = () => {
          closeMobileSidebar();
          if (workspaceOverlayOpen) setWorkspacePanelView(null);
          setComposerFocusRequested(true);
        };
      } else if (!event.shiftKey && key === '/') {
        action = () => setInfoDialog('shortcuts');
      }
    }
    if (!action) return;
    event.preventDefault();
    event.stopPropagation();
    if (!event.repeat) action();
  });

  useEffect(() => {
    const handleKeyDown = (event: KeyboardEvent) => handleShortcut(event);
    // Capture the terminal toggle before the terminal sends it to the shell.
    window.addEventListener('keydown', handleKeyDown, true);
    return () => window.removeEventListener('keydown', handleKeyDown, true);
  }, []);

  const handleCommitNewChatContext = (voice?: string) => {
    const context = contextSettings.commitDialog();
    if (!context) return;
    if (voice !== undefined) realtime.setVoice(voice);
    const optimisticConversation = optimisticRemoteConversationRef.current;
    if (
      conversationId &&
      optimisticConversation?.conversationId === conversationId &&
      !optimisticConversation.confirmed
    ) {
      const nextRunner = runners.find((runner) => runner.id === context.runnerId);
      const effectiveCWD = context.cwd || nextRunner?.workspace.path || '';
      const contextUpdate = {
        profile: context.profile,
        model: context.model || undefined,
        reasoningEffort: context.reasoningEffort || undefined,
        cwd: effectiveCWD,
        runnerId: context.runnerId || undefined,
        environmentProfile: context.environmentProfile || undefined,
        runner: nextRunner,
      };
      optimisticRemoteConversationRef.current = {
        ...optimisticConversation,
        runnerId: context.runnerId,
        environmentProfile: context.environmentProfile || undefined,
        cwd: context.cwd || undefined,
      };
      patchOptimisticConversationContext(contextUpdate);
    }
  };

  const conversationSearchReturnFocusSelector = mobileLayout
    ? '[data-testid="sidebar-attached-toggle-mobile"]'
    : sidebarVisible
      ? '[data-testid="sidebar-search-toggle"]'
      : '[data-testid="sidebar-collapsed-search"]';
  return (
    <div className="relative h-full bg-transparent">
      {uiRequestDialog ? (
        <UIInputDialog
          mode={uiRequestDialog.mode}
          request={uiRequestDialog.request}
          submitting={uiRequest.submitting}
          onCancel={() => {
            void uiRequest.respond(uiRequestDialog, { status: 'dismissed' });
          }}
          onSubmit={(value) => {
            void uiRequest.respond(uiRequestDialog, {
              status: 'submitted',
              value,
            });
          }}
        />
      ) : null}

      {infoDialog === 'shortcuts' && !uiRequestDialog ? (
        <KeyboardShortcutsDialog onClose={() => setInfoDialog(null)} />
      ) : null}

      {infoDialog === 'about' && !uiRequestDialog ? (
        <AboutKodeletDialog
          onClose={() => setInfoDialog(null)}
          runner={runners.find((runner) => runner.id === currentRunnerID)}
          terminalAuthorized={terminalAuthorized}
          toolInventoryTarget={
            isRemoteConversation && runnerWorkspaceAvailable
              ? {
                  runnerId: currentRunnerID,
                  conversationId: remoteWorkspaceConversationID,
                  cwd: remoteWorkspaceConversationID ? undefined : currentCWDLabel || undefined,
                  environmentProfile: currentEnvironmentProfile,
                  profile: discoveryProfile,
                }
              : undefined
          }
          usage={conversation?.id === selectedConversationId ? conversation?.usage : undefined}
        />
      ) : null}

      {providerSettingsOpen &&
      !uiRequestDialog &&
      !newChatDialogOpen &&
      !sidebarSearchOpen &&
      !infoDialog ? (
        <ProviderSettingsDialog onClose={() => setProviderSettingsOpen(false)} />
      ) : null}

      {newChatDialogOpen && !uiRequestDialog && !providerSettingsOpen && !infoDialog ? (
        <NewChatContextDialog
          {...contextSettings.dialogProps}
          voice={
            realtime.enabled
              ? {
                  value: realtime.voice,
                  defaultVoice: realtime.defaultVoice,
                  options: realtime.voices,
                }
              : undefined
          }
          onCommit={handleCommitNewChatContext}
        />
      ) : null}

      {sidebarSearchOpen &&
      !uiRequestDialog &&
      !newChatDialogOpen &&
      !providerSettingsOpen &&
      !infoDialog ? (
        <ConversationSearchDialog
          {...conversationSearch.dialogProps}
          cwdOptions={conversationCWDOptions}
          onSelectConversation={handleSelectSearchResult}
          returnFocusSelector={conversationSearchReturnFocusSelector}
        />
      ) : null}

      {dictation.busy && !higherPriorityDialogOpen ? (
        <DictationDialog {...dictation} onReturnFocus={restoreDictationFocus} />
      ) : null}

      {sidebarOverlayOpen && !modalOpen ? (
        <button
          aria-label="Hide sidebar overlay"
          className="absolute inset-0 z-40 bg-black/20 lg:hidden"
          onClick={handleSidebarToggle}
          type="button"
        />
      ) : null}

      <div
        aria-hidden={modalOpen || undefined}
        className={cn('h-full lg:flex', isResizingSidebar && 'select-none')}
        data-testid="chat-layout"
        inert={modalOpen || undefined}
      >
        {sidebarVisible ? (
          <section
            aria-hidden={workspaceOverlayOpen || undefined}
            aria-label="Conversations"
            {...(sidebarOverlayOpen ? { role: 'dialog', 'aria-modal': true } : {})}
            className="absolute inset-y-0 left-0 z-50 w-[min(85%,360px)] max-w-full shrink-0 lg:sticky lg:top-0 lg:relative lg:z-20 lg:h-full lg:w-[var(--sidebar-width)] lg:self-start"
            data-testid="chat-sidebar-shell"
            id="chat-sidebar"
            inert={workspaceOverlayOpen || undefined}
            ref={sidebarShellRef}
            tabIndex={sidebarOverlayOpen ? -1 : undefined}
            style={{ '--sidebar-width': `${sidebarWidth}px` } as React.CSSProperties}
          >
            <ChatSidebar
              activeConversationId={conversationId}
              authPrincipal={authPrincipal}
              conversations={conversations}
              loading={sidebarLoading}
              onDeleteConversation={handleDeleteConversation}
              onForkConversation={handleForkConversation}
              onHide={handleSidebarToggle}
              onNewChat={handleNewChat}
              onOpenAbout={() => setInfoDialog('about')}
              onOpenShortcuts={() => setInfoDialog('shortcuts')}
              onOpenProviderSettings={() => setProviderSettingsOpen(true)}
              onSearch={handleOpenSidebarSearch}
              onSelectConversation={handleOpenConversation}
              searchActive={sidebarSearchOpen}
            />
            <hr
              aria-controls="chat-sidebar"
              aria-label="Resize sidebar"
              aria-orientation="vertical"
              aria-valuemax={MAX_SIDEBAR_WIDTH}
              aria-valuemin={MIN_SIDEBAR_WIDTH}
              aria-valuenow={sidebarWidth}
              aria-valuetext={`${sidebarWidth} pixels`}
              className="sidebar-resize-edge absolute bottom-0 right-0 top-0 z-10 m-0 hidden h-auto translate-x-1/2 cursor-col-resize border-0 lg:block"
              data-testid="chat-sidebar-resizer"
              onKeyDown={handleSidebarResizeKeyDown}
              onMouseDown={handleSidebarResizeStart}
              tabIndex={0}
            />
          </section>
        ) : null}

        {!sidebarVisible ? (
          <>
            <ChatSidebarCollapsedRail
              inert={workspaceOverlayOpen}
              onNewChat={handleNewChat}
              onOpen={handleSidebarToggle}
              onSearch={handleOpenSidebarSearch}
              searchActive={sidebarSearchOpen}
            />

            <button
              aria-label="Show panel"
              className="sidebar-toggle-button sidebar-toggle-button-mobile"
              data-testid="sidebar-attached-toggle-mobile"
              inert={workspaceOverlayOpen || undefined}
              onClick={handleSidebarToggle}
              type="button"
            >
              <PanelLeft aria-hidden="true" className="h-4 w-4" strokeWidth={1.9} />
            </button>
          </>
        ) : null}

        <main
          aria-hidden={workspaceOverlayOpen || sidebarOverlayOpen || undefined}
          className="chat-main-panel relative flex h-full min-w-0 flex-1 flex-col overflow-hidden"
          inert={workspaceOverlayOpen || sidebarOverlayOpen || undefined}
        >
          <ConversationTabs
            activeId={tabKey}
            onClose={handleCloseTab}
            onConfigOpen={handleOpenTabConfig}
            onNew={handleNewChat}
            onReorder={conversationTabs.reorder}
            onSelect={handleSelectTab}
            tabs={conversationTabItems}
          />
          <div
            className="chat-main-scroll min-h-0 flex-1 overflow-y-auto"
            data-testid="chat-transcript-scroll"
            {...transcriptScrollProps}
          >
            {conversationLoading ? (
              <div className="flex min-h-full items-center justify-center px-4 pb-12 pt-20 sm:px-6 lg:py-12">
                <div className="surface-panel rounded-2xl px-6 py-5 text-sm text-kodelet-dark/70">
                  Loading conversation…
                </div>
              </div>
            ) : conversationError ? (
              <div className="px-3 py-8 sm:px-4 md:px-8">
                <div className="surface-panel max-w-3xl rounded-3xl border-kodelet-orange/20 px-6 py-5 text-kodelet-dark">
                  <p className="eyebrow-label text-kodelet-orange">Load error</p>
                  <p className="mt-3 text-sm leading-7">{conversationError}</p>
                  <button
                    className="panel-action-button panel-action-button-reload mt-3"
                    onClick={() => window.location.reload()}
                    type="button"
                  >
                    <RotateCw aria-hidden="true" className="h-3.5 w-3.5" strokeWidth={1.9} />
                    Reload
                  </button>
                </div>
              </div>
            ) : (
              <>
                <ChatTranscript isStreaming={currentConversationIsStreaming} messages={messages} />
                <ConversationStatistics
                  conversation={conversation}
                  conversationId={conversationId}
                  runner={currentRunner}
                  runnerId={currentRunnerID}
                  environmentProfile={currentEnvironmentProfile}
                />
                <PendingSteerList messages={pendingSteerMessages} />
                <PendingSteerList kind="commands" messages={pendingCommandMessages} />
              </>
            )}
          </div>

          <ExtensionWidgets placement="aboveComposer" widgets={extensionWidgets} />
          <ChatComposer
            {...attachmentProps}
            {...slashCommands.composerProps}
            addImageDisabled={!isRemoteConversation || steering}
            canStop={currentConversationIsStreaming}
            contextDisabled={currentConversationIsStreaming || steering}
            contextIsStatic={contextIsStatic}
            contextText={composerContextText}
            dictation={dictation}
            realtime={realtime}
            draft={draft}
            editorRef={composerRef}
            placeholder={composerPlaceholder}
            quickPick={composerQuickPick}
            showStop={currentConversationIsStreaming}
            stopActionLabel="Stop"
            streamError={streamError || workspaceExecutionMessage}
            submitActionLabel={submitActionLabel}
            submitDisabled={dictation.busy || steering || !canSubmit}
            textareaDisabled={steering || !isRemoteConversation}
            onContextOpen={openChatContext}
            onDraftChange={setDraft}
            onReload={streamError ? () => window.location.reload() : undefined}
            onStop={handleStop}
            onSubmit={handleSubmit}
          />
          <ExtensionWidgets placement="belowComposer" widgets={extensionWidgets} />
        </main>

        {workspaceToolsAvailable ? (
          <ChatWorkspacePanel
            layout={layout}
            resize={workspaceResize}
            terminalAvailable={workspaceTerminalAvailable}
            gitDiffAvailable={workspaceGitDiffAvailable}
            browserAvailable={workspaceBrowserAvailable}
            workspaceTarget={terminalTarget}
            workspaceTargetKey={sessionTargetKey}
            browserTarget={browserTarget}
            browserTargetKey={sessionTargetKey}
            gitDiff={gitDiff}
            gitDiffError={gitDiffError}
            gitDiffLoading={gitDiffLoading}
            onToggle={handleToggleWorkspacePanel}
            onSelectTerminal={handleSelectTerminalPanel}
            onSelectGitDiff={handleSelectGitDiffPanel}
            onSelectBrowser={() => setWorkspacePanelView('browser')}
            onRefreshGitDiff={() => {
              void fetchGitDiff();
            }}
          />
        ) : null}
      </div>
    </div>
  );
};

export default ChatPage;
