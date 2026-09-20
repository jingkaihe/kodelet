import { PanelLeft, RotateCw } from 'lucide-react';
import type React from 'react';
import {
  startTransition,
  useCallback,
  useEffect,
  useEffectEvent,
  useMemo,
  useRef,
  useState,
} from 'react';
import { useNavigate, useParams } from 'react-router';
import ChatComposer from '../components/chat/ChatComposer';
import ChatSidebar, {
  ChatSidebarCollapsedRail,
  ConversationSearchDialog,
} from '../components/chat/ChatSidebar';
import ChatTranscript from '../components/chat/ChatTranscript';
import ChatWorkspaceHeader from '../components/chat/ChatWorkspaceHeader';
import ChatWorkspacePanel from '../components/chat/ChatWorkspacePanel';
import ConversationStatistics from '../components/chat/ConversationStatistics';
import ExtensionWidgets from '../components/chat/ExtensionWidgets';
import NewChatContextDialog from '../components/chat/NewChatContextDialog';
import PendingSteerList from '../components/chat/PendingSteerList';
import ProviderSettingsDialog from '../components/chat/ProviderSettingsDialog';
import UIInputDialog from '../components/chat/UIInputDialog';
import { applyChatStreamEvent, conversationToChatMessages } from '../features/chat/state';
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
import { useExtensionWidgets } from '../features/chat/useExtensionWidgets';
import { useSlashCommands } from '../features/chat/useSlashCommands';
import { useWorkspaceResize } from '../features/chat/useWorkspaceResize';
import apiService from '../services/api';
import type {
  AuthPrincipal,
  BrowserTarget,
  ChatStreamEvent,
  Conversation,
  GitDiffResponse,
  PendingImageAttachment,
  Runner,
  UIConfirmRequestEvent,
  UIInputRequestEvent,
  UISelectRequestEvent,
  WorkspaceTarget,
} from '../types';
import { cn, showToast } from '../utils';

const normalizeConversation = (conversation: Conversation): Conversation => ({
  ...conversation,
  cwd:
    typeof conversation.cwd === 'string' && conversation.cwd.trim()
      ? conversation.cwd.trim()
      : undefined,
  profile:
    typeof conversation.profile === 'string' && conversation.profile.trim()
      ? conversation.profile.trim()
      : undefined,
  reasoningEffort:
    typeof conversation.reasoningEffort === 'string' && conversation.reasoningEffort.trim()
      ? conversation.reasoningEffort.trim().toLowerCase()
      : undefined,
  environmentProfile:
    typeof conversation.environmentProfile === 'string' && conversation.environmentProfile.trim()
      ? conversation.environmentProfile.trim()
      : undefined,
  messages: (conversation.messages || []).map((message) => ({
    role: message.role || 'user',
    content: message.content || '',
    toolCalls: message.toolCalls || message.tool_calls || [],
    thinkingText: message.thinkingText,
    thinkingTexts: message.thinkingTexts || [],
  })),
  pendingSteer: (conversation.pendingSteer || []).map((message) => ({
    role: message.role || 'user',
    content: message.content || '',
  })),
  toolResults: conversation.toolResults || {},
});

const mergeConversationUsage = (
  currentConversation: Conversation | null,
  usage: Conversation['usage']
): Conversation | null => {
  if (!currentConversation || !usage) {
    return currentConversation;
  }

  return {
    ...currentConversation,
    usage,
  };
};

const AUTO_SCROLL_BOTTOM_THRESHOLD = 80;
type UIRequestDialogState =
  | { mode: 'input'; request: UIInputRequestEvent }
  | { mode: 'confirm'; request: UIConfirmRequestEvent }
  | { mode: 'select'; request: UISelectRequestEvent };
const randomHex = (byteCount: number): string => {
  const bytes = new Uint8Array(byteCount);
  if (typeof crypto !== 'undefined' && crypto.getRandomValues) {
    crypto.getRandomValues(bytes);
  } else {
    for (let index = 0; index < bytes.length; index += 1) {
      bytes[index] = Math.floor(Math.random() * 256);
    }
  }

  return Array.from(bytes, (byte) => byte.toString(16).padStart(2, '0')).join('');
};

const generateConversationId = (): string => {
  const timestamp = new Date()
    .toISOString()
    .replace(/[-:]/g, '')
    .replace(/\.\d{3}Z$/, '');
  return `${timestamp}-${randomHex(8)}`;
};

const isScrolledNearBottom = (element: HTMLElement): boolean =>
  element.scrollHeight - element.scrollTop - element.clientHeight <= AUTO_SCROLL_BOTTOM_THRESHOLD;

const buildConversationPreview = (
  prompt: string,
  attachments: PendingImageAttachment[]
): string => {
  const trimmedPrompt = prompt.trim();
  if (trimmedPrompt) {
    return trimmedPrompt;
  }

  if (attachments.length === 1) {
    return attachments[0].name || 'Image attachment';
  }

  if (attachments.length > 1) {
    return `${attachments.length} image attachments`;
  }

  return 'Untitled conversation';
};

const reconcileStreamCWDForDisplay = (
  canonicalCWD: string | undefined,
  currentCWD: string,
  isRemote: boolean
): string => {
  const canonical = canonicalCWD?.trim();
  const current = currentCWD.trim();
  if (!canonical) {
    return current;
  }

  // Conversation APIs compact paths under the control-plane home directory. Preserve that
  // display form when the live local stream confirms the equivalent absolute path.
  if (
    !isRemote &&
    (current === '~' || current.startsWith('~/')) &&
    (current === '~' || canonical.endsWith(current.slice(1)))
  ) {
    return current;
  }

  return canonical;
};

const isBlockingUIRequestEvent = (event: ChatStreamEvent): boolean =>
  event.kind === 'ui-input-request' ||
  event.kind === 'ui-confirm-request' ||
  event.kind === 'ui-select-request' ||
  event.kind === 'ui-request-end';

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
  const [runners, setRunners] = useState<Runner[]>([]);
  const [draft, setDraft] = useState('');
  const [conversationLoading, setConversationLoading] = useState(false);
  const [conversationError, setConversationError] = useState<string | null>(null);
  const [streamError, setStreamError] = useState<string | null>(null);
  const [conversationStreamVersion, setConversationStreamVersion] = useState(0);
  const [steering, setSteering] = useState(false);
  const [locallyRunningConversationIds, setLocallyRunningConversationIds] = useState<string[]>([]);
  const { attachments, setAttachments, composerProps: attachmentProps } = useChatAttachments();
  const [gitDiffLoading, setGitDiffLoading] = useState(false);
  const [gitDiffError, setGitDiffError] = useState<string | null>(null);
  const [gitDiff, setGitDiff] = useState<GitDiffResponse | null>(null);
  const [providerSettingsOpen, setProviderSettingsOpen] = useState(false);
  const [uiRequestDialog, setUIRequestDialog] = useState<UIRequestDialogState | null>(null);
  const [uiInputSubmitting, setUIInputSubmitting] = useState(false);
  const loadedConversationId = conversation?.id ?? null;
  const transcriptEndRef = useRef<HTMLDivElement | null>(null);
  const shouldAutoScrollRef = useRef(true);
  const abortControllerRef = useRef<AbortController | null>(null);
  const sendControllersRef = useRef<Record<string, AbortController>>({});
  const runningSubscriptionControllersRef = useRef<Record<string, AbortController>>({});
  const resumeControllerRef = useRef<AbortController | null>(null);
  const resumeStreamRef = useRef(0);
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
  const optimisticRemoteConversationRef = useRef<{
    conversationId: string;
    runnerId: string;
    environmentProfile?: string;
    cwd?: string;
    confirmed: boolean;
  } | null>(null);
  const routerConversationIdRef = useRef<string | null>(conversationId);
  const {
    conversations,
    setConversations,
    total: conversationTotal,
    cwdOptions: conversationCWDOptions,
    setCWDOptions: setConversationCWDOptions,
    loading: sidebarLoading,
    refresh: refreshConversations,
    setRunning,
  } = useConversationList({ sendControllersRef, optimisticRemoteConversationRef });
  const conversationSearch = useConversationSearch({
    conversations,
    total: conversationTotal,
    loading: sidebarLoading,
    limit: SIDEBAR_CONVERSATION_LIMIT,
    setCWDOptions: setConversationCWDOptions,
  });
  const { isOpen: sidebarSearchOpen, close: handleCloseConversationSearch } = conversationSearch;
  const higherPriorityDialogOpen =
    uiRequestDialog !== null || newChatDialogOpen || providerSettingsOpen || sidebarSearchOpen;
  const layout = useChatLayout(higherPriorityDialogOpen);
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

      sendControllersRef.current[id] = controller;
    },
    []
  );

  const clearRunningConversationForController = useCallback(
    (id: string | null | undefined, controller: AbortController) => {
      if (!id) {
        return;
      }

      if (sendControllersRef.current[id] === controller) {
        delete sendControllersRef.current[id];
        clearRunningConversation(id);
      }
    },
    [clearRunningConversation]
  );

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
      resumeStreamRef.current += 1;
      abortControllerRef.current?.abort();
      Object.values(sendControllersRef.current).forEach((controller) => {
        controller.abort();
      });
      sendControllersRef.current = {};
      Object.values(runningSubscriptionControllersRef.current).forEach((controller) => {
        controller.abort();
      });
      runningSubscriptionControllersRef.current = {};
      resumeControllerRef.current?.abort();
    };
  }, []);

  const runningConversationIds = useMemo(() => {
    const runningIds = conversations
      .filter((listedConversation) => listedConversation.isRunning)
      .map((listedConversation) => listedConversation.id);

    return Array.from(new Set([...runningIds, ...locallyRunningConversationIds]));
  }, [conversations, locallyRunningConversationIds]);

  const onStreamUIInputRequest = useEffectEvent((event: ChatStreamEvent) =>
    handleUIInputRequest(event)
  );

  useEffect(() => {
    const runningIds = new Set(runningConversationIds);

    Object.entries(runningSubscriptionControllersRef.current).forEach(([runningId, controller]) => {
      if (
        !runningIds.has(runningId) ||
        runningId === conversationId ||
        sendControllersRef.current[runningId]
      ) {
        controller.abort();
        delete runningSubscriptionControllersRef.current[runningId];
      }
    });

    runningConversationIds.forEach((runningId) => {
      if (
        runningId === conversationId ||
        sendControllersRef.current[runningId] ||
        runningSubscriptionControllersRef.current[runningId]
      ) {
        return;
      }

      const controller = new AbortController();
      runningSubscriptionControllersRef.current[runningId] = controller;

      void apiService
        .streamConversation(runningId, {
          signal: controller.signal,
          onEvent: (event: ChatStreamEvent) => {
            if (event.conversation_id && event.conversation_id !== runningId) {
              return;
            }

            const eventForConversation = event.conversation_id
              ? event
              : { ...event, conversation_id: runningId };

            if (event.kind === 'conversation') {
              markConversationRunning(runningId);
              return;
            }

            if (isBlockingUIRequestEvent(event) && onStreamUIInputRequest(eventForConversation)) {
              return;
            }

            if (event.kind === 'done' || event.kind === 'error') {
              clearRunningConversation(runningId);
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
          if (runningSubscriptionControllersRef.current[runningId] === controller) {
            delete runningSubscriptionControllersRef.current[runningId];
          }

          if (!controller.signal.aborted && !sendControllersRef.current[runningId]) {
            clearRunningConversation(runningId);
          }
        });
    });
  }, [clearRunningConversation, conversationId, markConversationRunning, runningConversationIds]);

  const selectedConversationId = conversationId || activeConversationId;
  const activeRunningConversationId =
    selectedConversationId &&
    (runningConversationIds.includes(selectedConversationId) ||
      (conversation?.id === selectedConversationId && conversation.isRunning))
      ? selectedConversationId
      : null;
  const currentConversationIsStreaming = Boolean(activeRunningConversationId);

  useEffect(() => {
    viewedConversationIdRef.current = conversationId;
    routerConversationIdRef.current = conversationId;
    // Once this draft has its route, reserve a new identity for returning to new chat.
    setDraftConversationId((draftId) =>
      draftId === conversationId ? generateConversationId() : draftId
    );
  }, [conversationId]);

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
    shouldAutoScrollRef.current = true;

    resumeStreamRef.current += 1;
    setActiveConversationId(conversationId);
    resetExtensionWidgets();
    setSteering(false);
    setStreamError(null);

    resumeControllerRef.current?.abort();
    resumeControllerRef.current = null;

    if (!conversationId) {
      setConversation(null);
      setMessages([]);
      setConversationError(null);
      return;
    }

    setConversationLoading(true);
    setConversationError(null);

    void apiService
      .getConversation(conversationId)
      .then((data) => {
        const normalizedConversation = normalizeConversation(data);
        setActiveConversationId(normalizedConversation.id);
        setConversation(normalizedConversation);
        setMessages(conversationToChatMessages(normalizedConversation));
      })
      .catch((error: unknown) => {
        const message = error instanceof Error ? error.message : 'Failed to load conversation';
        setConversationError(message);
      })
      .finally(() => {
        setConversationLoading(false);
      });
  }, [conversationId, resetExtensionWidgets]);

  // biome-ignore lint/correctness/useExhaustiveDependencies(conversationStreamVersion): Completing a submitted stream must reattach the conversation watcher even when its ID is unchanged.
  useEffect(() => {
    if (
      !conversationId ||
      conversationLoading ||
      loadedConversationId !== conversationId ||
      sendControllersRef.current[conversationId]
    ) {
      return;
    }

    const streamInstance = resumeStreamRef.current + 1;
    resumeStreamRef.current = streamInstance;
    const controller = new AbortController();
    resumeControllerRef.current = controller;
    let sawEvent = false;
    let watchedTurn = 0;

    void apiService
      .streamConversation(conversationId, {
        signal: controller.signal,
        onEvent: (event: ChatStreamEvent) => {
          if (event.conversation_id && event.conversation_id !== conversationId) {
            return;
          }
          if (sendControllersRef.current[conversationId]) {
            return;
          }

          const eventForConversation = event.conversation_id
            ? event
            : { ...event, conversation_id: conversationId };

          if (isBlockingUIRequestEvent(event) && onStreamUIInputRequest(eventForConversation)) {
            sawEvent = true;
            return;
          }

          if (
            resumeStreamRef.current !== streamInstance ||
            viewedConversationIdRef.current !== conversationId
          ) {
            return;
          }

          sawEvent = true;
          if (handleExtensionWidgetEvent(event)) {
            return;
          }
          if (event.kind === 'conversation' && event.conversation_id) {
            watchedTurn += 1;
            setActiveConversationId(event.conversation_id);
            markConversationRunning(event.conversation_id);
            return;
          }

          if (event.kind === 'usage' && event.usage) {
            setConversation((currentConversation) =>
              mergeConversationUsage(currentConversation, event.usage)
            );
            return;
          }

          if (event.kind === 'done') {
            clearRunningConversation(conversationId);
            const completedTurn = watchedTurn;
            void apiService
              .getConversation(conversationId)
              .then((data) => {
                if (
                  resumeStreamRef.current !== streamInstance ||
                  viewedConversationIdRef.current !== conversationId ||
                  sendControllersRef.current[conversationId] ||
                  completedTurn !== watchedTurn
                ) {
                  return;
                }
                const normalizedConversation = normalizeConversation(data);
                setConversation(normalizedConversation);
                setMessages(conversationToChatMessages(normalizedConversation));
                void refreshConversations();
              })
              .catch((error) => {
                console.error('Failed to refresh completed conversation', error);
              });
          } else if (event.kind === 'error') {
            clearRunningConversation(conversationId);
          }

          if (event.kind === 'error') {
            setStreamError(event.error || 'Chat request failed');
          }

          if (event.kind === 'user-message') {
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
          resumeStreamRef.current !== streamInstance ||
          viewedConversationIdRef.current !== conversationId
        ) {
          return;
        }

        const message =
          error instanceof Error ? error.message : 'Failed to resume conversation stream';
        if (message === 'conversation is not actively streaming') {
          if (!sendControllersRef.current[conversationId]) {
            clearRunningConversation(conversationId);
          }
          return;
        }

        console.error('Failed to resume conversation stream', error);
        setStreamError(message);
      })
      .finally(() => {
        if (resumeControllerRef.current === controller) {
          resumeControllerRef.current = null;
        }

        if (
          resumeStreamRef.current !== streamInstance ||
          viewedConversationIdRef.current !== conversationId
        ) {
          return;
        }

        if (sawEvent && !sendControllersRef.current[conversationId]) {
          clearRunningConversation(conversationId);
        }
      });

    return () => {
      controller.abort();
      if (resumeControllerRef.current === controller) {
        resumeControllerRef.current = null;
      }
    };
  }, [
    clearRunningConversation,
    conversationId,
    conversationLoading,
    conversationStreamVersion,
    handleExtensionWidgetEvent,
    loadedConversationId,
    markConversationRunning,
    refreshConversations,
  ]);

  const handleTranscriptScroll = (event: React.UIEvent<HTMLDivElement>) => {
    shouldAutoScrollRef.current = isScrolledNearBottom(event.currentTarget);
  };

  // biome-ignore lint/correctness/useExhaustiveDependencies(messages): New transcript content triggers scrolling when the reader is following the bottom.
  // biome-ignore lint/correctness/useExhaustiveDependencies(currentConversationIsStreaming): Starting or stopping the streaming indicator changes the transcript height.
  useEffect(() => {
    if (!shouldAutoScrollRef.current) {
      return;
    }

    transcriptEndRef.current?.scrollIntoView({
      behavior: 'smooth',
      block: 'end',
    });
  }, [messages, currentConversationIsStreaming]);

  const handleNewChat = () => {
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
    handleSelectConversation(nextConversationId);
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

      if (
        targetConversationId === conversationId ||
        targetConversationId === activeConversationId ||
        runningConversationIds.includes(targetConversationId)
      ) {
        if (optimisticRemoteConversationRef.current?.conversationId === targetConversationId) {
          optimisticRemoteConversationRef.current = null;
        }
        const sendController = sendControllersRef.current[targetConversationId];
        if (sendController) {
          sendController.abort();
          delete sendControllersRef.current[targetConversationId];
          if (abortControllerRef.current === sendController) {
            abortControllerRef.current = null;
          }
        }
        resumeControllerRef.current?.abort();
        setConversation(null);
        setActiveConversationId(null);
        setMessages([]);
        setConversationError(null);
        setStreamError(null);
        clearRunningConversation(targetConversationId);
        startTransition(() => {
          navigate('/');
        });
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

    conversationPathOverrideRef.current = nextPath;
    viewedConversationIdRef.current = streamedId;
    routerConversationIdRef.current = streamedId;
    startTransition(() => {
      navigate(nextPath, { replace: true });
    });
  };

  const handleUIInputRequest = (event: ChatStreamEvent) => {
    if (event.kind === 'ui-request-end') {
      setUIRequestDialog((current) =>
        current &&
        current.request.id === event.ui_request_id &&
        current.request.conversationId === event.conversation_id
          ? null
          : current
      );
      return true;
    }
    if (event.kind === 'ui-notification' && event.ui_notify) {
      showToast(event.ui_notify.message, 'info', event.ui_notify.title);
      return true;
    }
    if (isBlockingUIRequestEvent(event)) {
      contextSettings.interruptDialog();
      handleCloseConversationSearch();
    }

    if (event.kind === 'ui-input-request' && event.ui_input) {
      setUIRequestDialog({
        mode: 'input',
        request: {
          ...event.ui_input,
          conversationId: event.conversation_id,
        },
      });
      setUIInputSubmitting(false);
      return true;
    }

    if (event.kind === 'ui-confirm-request' && event.ui_confirm) {
      setUIRequestDialog({
        mode: 'confirm',
        request: {
          ...event.ui_confirm,
          conversationId: event.conversation_id,
        },
      });
      setUIInputSubmitting(false);
      return true;
    }

    if (event.kind === 'ui-select-request' && event.ui_select) {
      setUIRequestDialog({
        mode: 'select',
        request: {
          ...event.ui_select,
          conversationId: event.conversation_id,
        },
      });
      setUIInputSubmitting(false);
      return true;
    }

    return false;
  };

  const respondToUIRequest = async (
    dialog: UIRequestDialogState,
    response: { status: 'submitted' | 'dismissed'; value?: string }
  ) => {
    const request = dialog.request;
    let payload = response;
    if (dialog.mode === 'confirm' && response.status === 'submitted') {
      payload = { ...response, value: 'true' };
    }
    if (dialog.mode === 'confirm' && response.status === 'dismissed') {
      payload = { ...response, value: 'false' };
    }

    const targetConversationId = request.conversationId || activeConversationId || conversationId;
    if (!targetConversationId) {
      showToast('Cannot answer extension prompt before conversation starts', 'error');
      return;
    }

    setUIInputSubmitting(true);
    try {
      await apiService.respondToUIInput(targetConversationId, request.id, payload);
      setUIRequestDialog((currentDialog) =>
        currentDialog?.request.id === request.id ? null : currentDialog
      );
    } catch (error) {
      const message = error instanceof Error ? error.message : 'Failed to answer extension prompt';
      showToast(message, 'error');
    } finally {
      setUIInputSubmitting(false);
    }
  };

  const handleSubmit = async () => {
    const prompt = draft.trim();
    const attachmentsForSend = attachments;
    if ((!prompt && attachments.length === 0) || steering) {
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

      setSteering(true);
      setStreamError(null);

      try {
        const queuedContent = buildUserContent(prompt, attachmentsForSend);
        await apiService.steerConversation(targetConversationId, prompt, queuedContent);
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
        setDraft('');
        setAttachments([]);
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

    setDraft('');
    setStreamError(null);
    const initialUserContent = buildUserContent(prompt, attachmentsForSend);
    setAttachments([]);
    setMessages((currentMessages) => [
      ...currentMessages,
      {
        role: 'user',
        content: initialUserContent,
      },
    ]);
    const targetConversationId = conversationId || draftConversationId;
    const isNewConversation = !conversationId;
    const existingOptimisticRemoteConversation =
      conversationId && optimisticRemoteConversationRef.current?.conversationId === conversationId
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
    abortControllerRef.current = controller;
    registerSendController(targetConversationId, controller);
    markConversationRunning(targetConversationId);
    const viewConversationIdAtStart = conversationId;
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
            conversationId && !existingOptimisticRemoteConversation ? undefined : selectedProfile,
          options:
            (conversationId && !existingOptimisticRemoteConversation) || !selectedModel
              ? undefined
              : { model: selectedModel },
          reasoningEffort:
            (conversationId && !existingOptimisticRemoteConversation) || !chatSettingsLoaded
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
            if (event.kind === 'conversation' && event.conversation_id) {
              const streamedId = event.conversation_id;
              const canonicalCWD = event.cwd?.trim();
              const effectiveCWD = reconcileStreamCWDForDisplay(
                canonicalCWD,
                currentCWDLabel,
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
                sendControllersRef.current[previousStreamedId] === controller
              ) {
                delete sendControllersRef.current[previousStreamedId];
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
              streamedError = event.error || 'Chat request failed';
              if (shouldUpdateCurrentView) {
                setStreamError(streamedError);
              }
              return;
            }

            if (event.kind === 'user-message') {
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
          const latestConversation = normalizeConversation(
            await apiService.getConversation(streamedConversationId)
          );
          setConversation(latestConversation);
          setMessages(conversationToChatMessages(latestConversation));
        } catch {
          // The streamed conversation is already authoritative enough to continue. A later
          // conversation load will reconcile the persisted snapshot.
        }
        if (streamedConversationId !== routerConversationIdRef.current) {
          startTransition(() => {
            navigate(`/c/${streamedConversationId}`, { replace: true });
          });
        }
      }

      await refreshConversations();
    } catch (error) {
      if (error instanceof DOMException && error.name === 'AbortError') {
        clearRunningConversationForController(streamedConversationId, controller);
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
        setAttachments(attachmentsForSend);
        setStreamError(message);
        showToast(message, 'error');
      }
    } finally {
      if (abortControllerRef.current === controller) {
        abortControllerRef.current = null;
      }
      clearRunningConversationForController(streamedConversationId, controller);
      setConversationStreamVersion((currentVersion) => currentVersion + 1);
    }
  };

  const handleStop = () => {
    const conversationToStop = activeRunningConversationId;
    if (!conversationToStop) {
      return;
    }

    const sendController = sendControllersRef.current[conversationToStop];
    if (sendController) {
      sendController.abort();
      delete sendControllersRef.current[conversationToStop];
      if (abortControllerRef.current === sendController) {
        abortControllerRef.current = null;
      }
    } else {
      resumeControllerRef.current?.abort();
    }
    setSteering(false);
    clearRunningConversation(conversationToStop);
    setUIRequestDialog(null);
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
  // Before affinity is persisted, workspace tools can only address the runner's startup directory.
  const runnerDirectoryAvailable =
    currentCWDLabel === currentRunner?.workspace.path ||
    Boolean(remoteWorkspaceConversationID && currentRunner?.workspaceCwd);
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

  const workspaceResize = useWorkspaceResize(
    layout,
    workspaceToolsAvailable,
    higherPriorityDialogOpen
  );

  const workspaceTarget = useMemo<WorkspaceTarget>(
    () => ({
      kind: 'runner',
      runnerId: currentRunnerID,
      conversationId: remoteWorkspaceConversationID,
    }),
    [currentRunnerID, remoteWorkspaceConversationID]
  );
  const workspaceTargetKey = `runner:${currentRunnerID}:conversation:${remoteWorkspaceConversationID || ''}:cwd:${currentCWDLabel}:generation:${currentRunner?.generation || 0}`;
  workspaceTargetKeyRef.current = workspaceTargetKey;

  const browserConversationId = conversationId || draftConversationId;
  const browserTarget = useMemo<BrowserTarget>(
    () => ({ runnerId: currentRunnerID, conversationId: browserConversationId }),
    [currentRunnerID, browserConversationId]
  );
  const browserTargetKey = `runner:${currentRunnerID}:conversation:${browserConversationId}:cwd:${currentCWDLabel}:generation:${currentRunner?.generation || 0}`;

  const slashCommands = useSlashCommands({
    draft,
    setDraft,
    disabled: currentConversationIsStreaming || steering,
    discoveryAvailable: Boolean(
      isRemoteConversation && runnerWorkspaceAvailable && currentRunner?.workspaceDiscovery
    ),
    runnerId: currentRunnerID,
    generation: currentRunner?.generation,
    conversationId: remoteWorkspaceConversationID,
    cwd: currentCWDLabel,
    environmentProfile: currentEnvironmentProfile,
    profile: discoveryProfile,
    onSubmit: handleSubmit,
  });

  useEffect(() => {
    if (
      !workspaceToolsAvailable ||
      (workspacePanelView === 'terminal' && !workspaceTerminalAvailable) ||
      (workspacePanelView === 'browser' && !workspaceBrowserAvailable) ||
      (workspacePanelView === 'diff' && !workspaceGitDiffAvailable)
    ) {
      setWorkspacePanelView(null);
    }
  }, [
    setWorkspacePanelView,
    workspaceBrowserAvailable,
    workspaceGitDiffAvailable,
    workspacePanelView,
    workspaceTerminalAvailable,
    workspaceToolsAvailable,
  ]);

  const composerModelLabel = currentModelLabel
    ? [currentProfileLabel, currentModelLabel].filter(Boolean).join('/')
    : '';
  const composerContextText =
    [composerModelLabel, currentReasoningEffortLabel].filter(Boolean).join(' · ') ||
    (conversationId ? 'Saved settings' : 'Model settings');
  const contextIsStatic = Boolean(conversationId) && !optimisticConversationContextEditable;
  const openChatContext = () => contextSettings.openDialog(currentProfileLabel);

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
      ? 'Steer the active conversation…'
      : slashCommands.placeholder || 'Ask kodelet anything...';
  const workspaceExecutionMessage = !isRemoteConversation
    ? conversationId
      ? 'This conversation uses the disabled control-plane workspace and is read-only.'
      : 'The control-plane workspace is disabled. Select a workspace runner to start a chat.'
    : null;
  const submitActionLabel = steering
    ? 'Queueing…'
    : currentConversationIsStreaming
      ? 'Steer'
      : 'Send';
  const pendingSteerMessages = conversation?.pendingSteer || [];

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
      setWorkspacePanelView('terminal');
    }
  };

  const handleCommitNewChatContext = () => {
    const context = contextSettings.commitDialog();
    if (!context) return;
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
          submitting={uiInputSubmitting}
          onCancel={() => {
            void respondToUIRequest(uiRequestDialog, { status: 'dismissed' });
          }}
          onSubmit={(value) => {
            void respondToUIRequest(uiRequestDialog, {
              status: 'submitted',
              value,
            });
          }}
        />
      ) : null}

      {providerSettingsOpen && !uiRequestDialog && !newChatDialogOpen && !sidebarSearchOpen ? (
        <ProviderSettingsDialog onClose={() => setProviderSettingsOpen(false)} />
      ) : null}

      {newChatDialogOpen && !uiRequestDialog && !providerSettingsOpen ? (
        <NewChatContextDialog
          {...contextSettings.dialogProps}
          onCommit={handleCommitNewChatContext}
        />
      ) : null}

      {sidebarSearchOpen && !uiRequestDialog && !newChatDialogOpen && !providerSettingsOpen ? (
        <ConversationSearchDialog
          {...conversationSearch.dialogProps}
          cwdOptions={conversationCWDOptions}
          onSelectConversation={handleSelectSearchResult}
          returnFocusSelector={conversationSearchReturnFocusSelector}
        />
      ) : null}

      {sidebarOverlayOpen ? (
        <button
          aria-label="Hide sidebar overlay"
          className="absolute inset-0 z-40 bg-black/20 lg:hidden"
          onClick={handleSidebarToggle}
          type="button"
        />
      ) : null}

      <div
        aria-hidden={higherPriorityDialogOpen || undefined}
        className={cn('h-full lg:flex', isResizingSidebar && 'select-none')}
        data-testid="chat-layout"
        inert={higherPriorityDialogOpen || undefined}
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
              onOpenProviderSettings={() => setProviderSettingsOpen(true)}
              onSearch={handleOpenSidebarSearch}
              onSelectConversation={handleSelectConversation}
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
              className="sidebar-toggle-button sidebar-toggle-button-mobile lg:hidden"
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
          <ChatWorkspaceHeader
            cwd={
              conversationId
                ? workspaceConversation?.cwd || currentRunner?.workspace.path || ''
                : currentRunnerID
                  ? currentCWDLabel
                  : ''
            }
            loading={Boolean(conversationId && !workspaceConversation && !conversationError)}
            disabled={currentConversationIsStreaming || steering}
            onWorkspaceOpen={contextIsStatic ? undefined : openChatContext}
          />
          <div
            className="chat-main-scroll min-h-0 flex-1 overflow-y-auto"
            data-testid="chat-transcript-scroll"
            onScroll={handleTranscriptScroll}
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
                <div ref={transcriptEndRef} />
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
            draft={draft}
            placeholder={composerPlaceholder}
            quickPick={composerQuickPick}
            showStop={currentConversationIsStreaming}
            stopActionLabel="Stop"
            streamError={streamError || workspaceExecutionMessage}
            submitActionLabel={submitActionLabel}
            submitDisabled={steering || !canSubmit}
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
            workspaceTarget={workspaceTarget}
            workspaceTargetKey={workspaceTargetKey}
            browserTarget={browserTarget}
            browserTargetKey={browserTargetKey}
            cwdLabel={currentRunner?.workspace.path || ''}
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
