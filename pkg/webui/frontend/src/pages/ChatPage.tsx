import React, {
  lazy,
  Suspense,
  startTransition,
  useCallback,
  useEffect,
  useMemo,
  useRef,
  useState,
} from 'react';
import { GitCompareArrows, PanelLeft, PanelRight, SquareTerminal } from 'lucide-react';
import { useNavigate, useParams } from 'react-router';
import ChatComposer from '../components/chat/ChatComposer';
import ChatSidebar, {
  ChatSidebarCollapsedRail,
  ConversationSearchDialog,
} from '../components/chat/ChatSidebar';
import ChatTranscript from '../components/chat/ChatTranscript';
import NewChatContextDialog from '../components/chat/NewChatContextDialog';
import PendingSteerList from '../components/chat/PendingSteerList';
import UIInputDialog from '../components/chat/UIInputDialog';
import { applyChatStreamEvent, conversationToChatMessages } from '../features/chat/state';
import apiService from '../services/api';
import type {
  AuthPrincipal,
  CWDHint,
  ChatSettings,
  ChatStreamEvent,
  ContentBlock,
  Conversation,
  GitDiffResponse,
  PendingImageAttachment,
  Runner,
  SlashCommandOption,
  UIConfirmRequestEvent,
  UIInputRequestEvent,
  UISelectRequestEvent,
} from '../types';
import {
  cn,
  debounce,
  formatCompactRelativeTime,
  formatContextWindow,
  formatCost,
  formatRunnerStatus,
  showToast,
  truncateMiddle,
} from '../utils';

const GitDiffModal = lazy(() => import('../components/workspace/GitDiffModal'));
const TerminalModal = lazy(() => import('../components/workspace/TerminalModal'));

const DEFAULT_REASONING_EFFORT = 'medium';

const reasoningSettingsFromChatSettings = (
  settings: Partial<ChatSettings>
): { effort: string; options: string[] } => {
  const effort =
    typeof settings.reasoningEffort === 'string' && settings.reasoningEffort.trim()
      ? settings.reasoningEffort.trim().toLowerCase()
      : DEFAULT_REASONING_EFFORT;
  const options = Array.from(
    new Set(
      (settings.reasoningEffortOptions || [])
        .map((option) => option.trim().toLowerCase())
        .filter(Boolean)
    )
  );

  if (!options.includes(effort)) {
    options.push(effort);
  }

  return { effort, options };
};

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

const getGreeting = (): string => {
  const hour = new Date().getHours();
  if (hour < 12) {
    return 'Good morning';
  }
  if (hour < 18) {
    return 'Good afternoon';
  }
  return 'Good evening';
};

const DEFAULT_SIDEBAR_WIDTH = 320;
const MIN_SIDEBAR_WIDTH = 260;
const MAX_SIDEBAR_WIDTH = 520;
const SIDEBAR_WIDTH_STORAGE_KEY = 'kodelet.chat.sidebar.width';
const SIDEBAR_VISIBLE_STORAGE_KEY = 'kodelet.chat.sidebar.visible';
const MOBILE_LAYOUT_MEDIA_QUERY = '(max-width: 1023px)';
const WORKSPACE_OVERLAY_MEDIA_QUERY = '(max-width: 1180px)';
const OVERLAY_FOCUSABLE_SELECTOR = [
  'button:not([disabled])',
  '[href]',
  'input:not([disabled])',
  'select:not([disabled])',
  'textarea:not([disabled])',
  "[contenteditable='true']",
  "[tabindex]:not([tabindex='-1'])",
].join(',');
const MAX_IMAGE_ATTACHMENTS = 10;
const MAX_IMAGE_BYTES = 5 * 1024 * 1024;
const SIDEBAR_CONVERSATION_LIMIT = 100;
const RECENT_WORKSPACE_LIMIT = 5;
const AUTO_SCROLL_BOTTOM_THRESHOLD = 80;
const SUPPORTED_IMAGE_TYPES = new Set(['image/png', 'image/jpeg', 'image/gif', 'image/webp']);
type UIRequestDialogState =
  | { mode: 'input'; request: UIInputRequestEvent }
  | { mode: 'confirm'; request: UIConfirmRequestEvent }
  | { mode: 'select'; request: UISelectRequestEvent };
type WorkspacePanelView = 'diff' | 'terminal';
const attachmentId = (): string =>
  typeof crypto !== 'undefined' && 'randomUUID' in crypto
    ? crypto.randomUUID()
    : `attachment-${Date.now()}-${Math.random().toString(36).slice(2, 8)}`;

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

const readFileAsDataUrl = (file: File): Promise<string> =>
  new Promise((resolve, reject) => {
    const reader = new FileReader();
    reader.onload = () => {
      if (typeof reader.result === 'string') {
        resolve(reader.result);
        return;
      }
      reject(new Error('Failed to read image data'));
    };
    reader.onerror = () => reject(reader.error || new Error('Failed to read image data'));
    reader.readAsDataURL(file);
  });

const fileToPendingAttachment = async (file: File): Promise<PendingImageAttachment> => {
  if (!SUPPORTED_IMAGE_TYPES.has(file.type)) {
    throw new Error('Only PNG, JPEG, GIF, and WebP images are supported');
  }

  if (file.size > MAX_IMAGE_BYTES) {
    throw new Error('Each image must be 5MB or smaller');
  }

  const dataUrl = await readFileAsDataUrl(file);
  const [, base64 = ''] = dataUrl.split(',', 2);

  return {
    id: attachmentId(),
    name: file.name || 'Pasted image',
    mediaType: file.type,
    data: base64,
    previewUrl: dataUrl,
    size: file.size,
  };
};

const buildUserContent = (
  prompt: string,
  attachments: PendingImageAttachment[]
): ContentBlock[] => [
  ...(prompt ? [{ type: 'text' as const, text: prompt }] : []),
  ...attachments.map((attachment) => ({
    type: 'image' as const,
    source: {
      data: attachment.data,
      media_type: attachment.mediaType,
    },
  })),
];

const isScrolledNearBottom = (element: HTMLElement): boolean =>
  element.scrollHeight - element.scrollTop - element.clientHeight <= AUTO_SCROLL_BOTTOM_THRESHOLD;

const clampSidebarWidth = (width: number): number =>
  Math.min(MAX_SIDEBAR_WIDTH, Math.max(MIN_SIDEBAR_WIDTH, width));

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

const getConversationTimestamp = (conversation: Conversation): number => {
  const timestamp =
    conversation.updatedAt ??
    conversation.updated_at ??
    conversation.createdAt ??
    conversation.created_at;

  return timestamp ? new Date(timestamp).getTime() : 0;
};

const getRecentWorkspaces = (conversations: Conversation[]): string[] => {
  const workspaces = new Set<string>();

  [...conversations]
    .sort((left, right) => getConversationTimestamp(right) - getConversationTimestamp(left))
    .some((conversation) => {
      const cwd = conversation.cwd?.trim();
      if (cwd) {
        workspaces.add(cwd);
      }

      return workspaces.size >= RECENT_WORKSPACE_LIMIT;
    });

  return Array.from(workspaces);
};

const getSlashCommandQuery = (draft: string): string | null => {
  const trimmedStart = draft.trimStart();
  if (!trimmedStart.startsWith('/')) {
    return null;
  }

  const withoutSlash = trimmedStart.slice(1);
  if (withoutSlash.includes(' ')) {
    return null;
  }

  return withoutSlash.toLowerCase();
};

const filterSlashCommands = (
  commands: SlashCommandOption[],
  draft: string
): SlashCommandOption[] => {
  const query = getSlashCommandQuery(draft);
  if (query === null) {
    return [];
  }

  return commands.filter((command) => {
    if (!query) {
      return true;
    }
    return (
      command.name.toLowerCase().includes(query) ||
      command.description.toLowerCase().includes(query)
    );
  });
};

const insertSlashCommand = (draft: string, commandName: string): string => {
  const leadingWhitespace = draft.match(/^\s*/)?.[0] || '';
  return `${leadingWhitespace}/${commandName} `;
};

const getDraftSlashCommand = (draft: string): string | null => {
  const trimmedStart = draft.trimStart();
  if (!trimmedStart.startsWith('/')) {
    return null;
  }

  const command = trimmedStart.slice(1).split(/\s+/, 1)[0];
  return command || null;
};

const getSlashCommandPlaceholder = (command: SlashCommandOption): string =>
  command.placeholder || `/${command.name}${command.hint ? ` ${command.hint}` : ''}`;

const isBlockingUIRequestEvent = (event: ChatStreamEvent): boolean =>
  event.kind === 'ui-input-request' ||
  event.kind === 'ui-confirm-request' ||
  event.kind === 'ui-select-request';

const upsertConversationSummary = (
  conversations: Conversation[],
  nextConversation: Conversation
): Conversation[] => {
  const merged = conversations.filter((conversation) => conversation.id !== nextConversation.id);
  merged.unshift(nextConversation);

  merged.sort((left, right) => {
    const leftTime = getConversationTimestamp(left);
    const rightTime = getConversationTimestamp(right);
    return rightTime - leftTime;
  });

  return merged;
};

const readStoredSidebarVisible = (): boolean => {
  if (typeof window === 'undefined') {
    return true;
  }

  return window.localStorage.getItem(SIDEBAR_VISIBLE_STORAGE_KEY) !== 'false';
};

const isMobileLayoutViewport = (): boolean =>
  typeof window !== 'undefined' &&
  typeof window.matchMedia === 'function' &&
  window.matchMedia(MOBILE_LAYOUT_MEDIA_QUERY).matches;

const useMediaQuery = (query: string): boolean => {
  const [matches, setMatches] = useState(
    () =>
      typeof window !== 'undefined' &&
      typeof window.matchMedia === 'function' &&
      window.matchMedia(query).matches
  );

  useEffect(() => {
    if (typeof window.matchMedia !== 'function') {
      return undefined;
    }

    const mediaQuery = window.matchMedia(query);
    const handleChange = (event: MediaQueryListEvent) => {
      setMatches(event.matches);
    };

    setMatches(mediaQuery.matches);
    mediaQuery.addEventListener('change', handleChange);
    return () => {
      mediaQuery.removeEventListener('change', handleChange);
    };
  }, [query]);

  return matches;
};

const readInitialSidebarVisible = (): boolean =>
  isMobileLayoutViewport() ? false : readStoredSidebarVisible();

const readStoredSidebarWidth = (): number => {
  if (typeof window === 'undefined') {
    return DEFAULT_SIDEBAR_WIDTH;
  }

  const storedWidth = window.localStorage.getItem(SIDEBAR_WIDTH_STORAGE_KEY);
  if (storedWidth === null) {
    return DEFAULT_SIDEBAR_WIDTH;
  }

  const parsedWidth = Number(storedWidth);
  return Number.isFinite(parsedWidth) ? clampSidebarWidth(parsedWidth) : DEFAULT_SIDEBAR_WIDTH;
};

const ChatPage: React.FC = () => {
  const navigate = useNavigate();
  const { id } = useParams<{ id: string }>();
  const conversationId = id || null;
  const [conversations, setConversations] = useState<Conversation[]>([]);
  const [conversationTotal, setConversationTotal] = useState(0);
  const [conversationSearchTerm, setConversationSearchTerm] = useState('');
  const [conversationSearchResults, setConversationSearchResults] = useState<Conversation[]>([]);
  const [conversationSearchError, setConversationSearchError] = useState<string | null>(null);
  const [conversationSearchHasMore, setConversationSearchHasMore] = useState(false);
  const [conversationSearchLoading, setConversationSearchLoading] = useState(false);
  const [conversationSearchLoadingMore, setConversationSearchLoadingMore] = useState(false);
  const [conversationSearchOffset, setConversationSearchOffset] = useState(0);
  const [conversationSearchTotal, setConversationSearchTotal] = useState(0);
  const [sidebarSearchOpen, setSidebarSearchOpen] = useState(false);
  const [conversationCWDFilter, setConversationCWDFilter] = useState('');
  const [conversationCWDOptions, setConversationCWDOptions] = useState<string[]>([]);
  const [conversation, setConversation] = useState<Conversation | null>(null);
  const [messages, setMessages] = useState(() => conversationToChatMessages(null));
  const [authPrincipal, setAuthPrincipal] = useState<AuthPrincipal | null>(null);
  const [activeConversationId, setActiveConversationId] = useState<string | null>(conversationId);
  const [chatSettings, setChatSettings] = useState<ChatSettings>({
    profiles: [],
    reasoningEffort: DEFAULT_REASONING_EFFORT,
    reasoningEffortOptions: [DEFAULT_REASONING_EFFORT],
  });
  const [chatSettingsLoaded, setChatSettingsLoaded] = useState(false);
  const [selectedProfile, setSelectedProfile] = useState('default');
  const [newChatProfileDraft, setNewChatProfileDraft] = useState('default');
  const [selectedReasoningEffort, setSelectedReasoningEffort] = useState(DEFAULT_REASONING_EFFORT);
  const [selectedReasoningEffortOptions, setSelectedReasoningEffortOptions] = useState<string[]>([
    DEFAULT_REASONING_EFFORT,
  ]);
  const [selectedReasoningEffortExplicit, setSelectedReasoningEffortExplicit] = useState(false);
  const [newChatReasoningEffortDraft, setNewChatReasoningEffortDraft] =
    useState(DEFAULT_REASONING_EFFORT);
  const [newChatReasoningEffortOptions, setNewChatReasoningEffortOptions] = useState<string[]>([
    DEFAULT_REASONING_EFFORT,
  ]);
  const [newChatReasoningEffortExplicit, setNewChatReasoningEffortExplicit] = useState(false);
  const [reasoningSettingsLoading, setReasoningSettingsLoading] = useState(false);
  const [selectedCWD, setSelectedCWD] = useState('');
  const [runners, setRunners] = useState<Runner[]>([]);
  const [selectedRunnerID, setSelectedRunnerID] = useState('');
  const [newChatRunnerDraft, setNewChatRunnerDraft] = useState('');
  const [selectedEnvironmentProfile, setSelectedEnvironmentProfile] = useState('');
  const [newChatEnvironmentProfileDraft, setNewChatEnvironmentProfileDraft] = useState('');
  const [cwdQuery, setCwdQuery] = useState('');
  const [cwdSuggestions, setCwdSuggestions] = useState<CWDHint[]>([]);
  const [cwdSuggestionsOpen, setCwdSuggestionsOpen] = useState(false);
  const [cwdSuggestionIndex, setCwdSuggestionIndex] = useState(-1);
  const [draft, setDraft] = useState('');
  const [slashCommands, setSlashCommands] = useState<SlashCommandOption[]>([]);
  const [slashCommandIndex, setSlashCommandIndex] = useState(-1);
  const [slashSuggestionsDismissedDraft, setSlashSuggestionsDismissedDraft] = useState<
    string | null
  >(null);
  const [sidebarLoading, setSidebarLoading] = useState(true);
  const [conversationLoading, setConversationLoading] = useState(false);
  const [conversationError, setConversationError] = useState<string | null>(null);
  const [streamError, setStreamError] = useState<string | null>(null);
  const [steering, setSteering] = useState(false);
  const [startingNewConversation, setStartingNewConversation] = useState(false);
  const [locallyRunningConversationIds, setLocallyRunningConversationIds] = useState<string[]>([]);
  const [attachments, setAttachments] = useState<PendingImageAttachment[]>([]);
  const [dragActive, setDragActive] = useState(false);
  const [gitDiffLoading, setGitDiffLoading] = useState(false);
  const [gitDiffError, setGitDiffError] = useState<string | null>(null);
  const [gitDiff, setGitDiff] = useState<GitDiffResponse | null>(null);
  const [workspacePanelView, setWorkspacePanelView] = useState<WorkspacePanelView | null>(null);
  const mobileLayout = useMediaQuery(MOBILE_LAYOUT_MEDIA_QUERY);
  const workspaceOverlayLayout = useMediaQuery(WORKSPACE_OVERLAY_MEDIA_QUERY);
  const [sidebarVisible, setSidebarVisible] = useState(readInitialSidebarVisible);
  const [sidebarWidth, setSidebarWidth] = useState(readStoredSidebarWidth);
  const [isResizingSidebar, setIsResizingSidebar] = useState(false);
  const [newChatDialogOpen, setNewChatDialogOpen] = useState(false);
  const [uiRequestDialog, setUIRequestDialog] = useState<UIRequestDialogState | null>(null);
  const [uiInputSubmitting, setUIInputSubmitting] = useState(false);
  const [statusTick, setStatusTick] = useState(0);
  const controlPlaneWorkspaceEnabled = chatSettings.controlPlaneWorkspaceEnabled !== false;
  const loadedConversationId = conversation?.id ?? null;
  const transcriptEndRef = useRef<HTMLDivElement | null>(null);
  const shouldAutoScrollRef = useRef(true);
  const abortControllerRef = useRef<AbortController | null>(null);
  const sendControllersRef = useRef<Record<string, AbortController>>({});
  const runningSubscriptionControllersRef = useRef<Record<string, AbortController>>({});
  const resumeControllerRef = useRef<AbortController | null>(null);
  const resumeStreamRef = useRef(0);
  const reasoningSettingsRequestRef = useRef(0);
  const cwdSuggestionRequestRef = useRef(0);
  const conversationListRequestRef = useRef(0);
  const conversationSearchRequestRef = useRef(0);
  const conversationSearchTermRef = useRef('');
  const conversationCWDFilterRef = useRef('');
  const cwdInputFocusedRef = useRef(false);
  const cwdSuggestionSkipQueryRef = useRef<string | null>(null);
  const viewedConversationIdRef = useRef<string | null>(conversationId);
  const conversationPathOverrideRef = useRef<string | null>(null);
  const routerConversationIdRef = useRef<string | null>(conversationId);
  const sidebarResizeStartRef = useRef<{
    startX: number;
    startWidth: number;
  } | null>(null);
  const desktopSidebarVisibleRef = useRef(readStoredSidebarVisible());
  const restoringDesktopSidebarRef = useRef(false);
  const sidebarWidthRef = useRef(sidebarWidth);
  const sidebarShellRef = useRef<HTMLDivElement | null>(null);
  const sidebarReturnFocusRef = useRef<HTMLElement | null>(null);
  const workspaceToolsRef = useRef<HTMLElement | null>(null);
  const cwdInputRef = useRef<HTMLInputElement | null>(null);
  const newChatDialogRef = useRef<HTMLDivElement | null>(null);
  const newChatReturnFocusRef = useRef<HTMLElement | null>(null);
  const closeMobileSidebar = useCallback(() => {
    if (mobileLayout) {
      setSidebarVisible(false);
    }
  }, [mobileLayout]);
  const workspacePanelOpen = workspacePanelView !== null;
  const sidebarOverlayOpen = mobileLayout && sidebarVisible;
  const workspaceOverlayOpen = workspaceOverlayLayout && workspacePanelOpen;
  const higherPriorityDialogOpen =
    uiRequestDialog !== null || newChatDialogOpen || sidebarSearchOpen;

  const setConversationRunning = useCallback(
    (id: string | null | undefined, isRunning: boolean) => {
      if (!id) {
        return;
      }

      setLocallyRunningConversationIds((currentIds) => {
        if (isRunning) {
          return currentIds.includes(id) ? currentIds : [...currentIds, id];
        }

        return currentIds.filter((currentId) => currentId !== id);
      });

      setConversations((currentConversations) =>
        currentConversations.map((currentConversation) =>
          currentConversation.id === id
            ? { ...currentConversation, isRunning }
            : currentConversation
        )
      );
      setConversation((currentConversation) =>
        currentConversation?.id === id ? { ...currentConversation, isRunning } : currentConversation
      );
    },
    []
  );

  const markConversationRunning = useCallback(
    (id: string | null | undefined) => {
      if (!id) {
        return;
      }

      setConversationRunning(id, true);
    },
    [setConversationRunning]
  );

  const clearRunningConversation = useCallback(
    (id: string | null | undefined) => {
      if (!id) {
        return;
      }

      setConversationRunning(id, false);
    },
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

  const refreshConversations = useCallback(async () => {
    const requestId = conversationListRequestRef.current + 1;
    conversationListRequestRef.current = requestId;

    setSidebarLoading(true);
    try {
      const response = await apiService.getConversations({
        limit: SIDEBAR_CONVERSATION_LIMIT,
        sortBy: 'updated',
        sortOrder: 'desc',
      });
      if (conversationListRequestRef.current !== requestId) {
        return;
      }

      const nextConversations = response.conversations || [];
      setConversations(nextConversations);
      setConversationTotal(response.total ?? nextConversations.length);
      const responseCWDs = (response.cwds?.length
        ? response.cwds
        : nextConversations.map((nextConversation) => nextConversation.cwd)
      )
        .map((cwd) => cwd?.trim())
        .filter((cwd): cwd is string => Boolean(cwd));
      setConversationCWDOptions(Array.from(new Set(responseCWDs)));
    } catch (error) {
      if (conversationListRequestRef.current === requestId) {
        console.error('Failed to load conversations', error);
      }
    } finally {
      if (conversationListRequestRef.current === requestId) {
        setSidebarLoading(false);
      }
    }
  }, []);

  const refreshConversationSearch = useCallback(async (offset = 0) => {
    const requestId = conversationSearchRequestRef.current + 1;
    conversationSearchRequestRef.current = requestId;
    const searchTerm = conversationSearchTermRef.current.trim();
    const cwdFilter = conversationCWDFilterRef.current.trim();
    const loadingMore = offset > 0;

    setConversationSearchError(null);
    if (loadingMore) {
      setConversationSearchLoadingMore(true);
    } else {
      setConversationSearchLoading(true);
    }
    try {
      const response = await apiService.getConversations({
        searchTerm,
        cwd: cwdFilter,
        limit: SIDEBAR_CONVERSATION_LIMIT,
        offset: offset || undefined,
        sortBy: 'updated',
        sortOrder: 'desc',
      });
      if (conversationSearchRequestRef.current !== requestId) {
        return;
      }

      const nextConversations = response.conversations || [];
      if (loadingMore) {
        setConversationSearchResults((currentResults) => {
          const seen = new Set(currentResults.map((conversation) => conversation.id));
          return [
            ...currentResults,
            ...nextConversations.filter((conversation) => {
              if (seen.has(conversation.id)) {
                return false;
              }
              seen.add(conversation.id);
              return true;
            }),
          ];
        });
      } else {
        setConversationSearchResults(nextConversations);
      }
      const nextTotal = response.total ?? offset + nextConversations.length;
      setConversationSearchOffset(offset + nextConversations.length);
      setConversationSearchTotal(nextTotal);
      setConversationSearchHasMore(
        response.hasMore ?? offset + nextConversations.length < nextTotal
      );
      const responseCWDs = (response.cwds?.length
        ? response.cwds
        : nextConversations.map((nextConversation) => nextConversation.cwd)
      )
        .map((cwd) => cwd?.trim())
        .filter((cwd): cwd is string => Boolean(cwd));
      setConversationCWDOptions((currentOptions) => {
        const seen = new Set<string>();
        return [...currentOptions, cwdFilter, ...responseCWDs].filter((cwd) => {
          if (!cwd || seen.has(cwd)) {
            return false;
          }
          seen.add(cwd);
          return true;
        });
      });
    } catch (error) {
      if (conversationSearchRequestRef.current === requestId) {
        console.error('Failed to search conversations', error);
        if (!loadingMore) {
          setConversationSearchResults([]);
          setConversationSearchHasMore(false);
          setConversationSearchOffset(0);
          setConversationSearchTotal(0);
        }
        setConversationSearchError(
          error instanceof Error ? error.message : 'Failed to search conversations'
        );
      }
    } finally {
      if (conversationSearchRequestRef.current === requestId) {
        if (loadingMore) {
          setConversationSearchLoadingMore(false);
        } else {
          setConversationSearchLoading(false);
        }
      }
    }
  }, []);

  const requestConversationFilterRefresh = useMemo(
    () =>
      debounce(() => {
        void refreshConversationSearch();
      }, 200),
    [refreshConversationSearch]
  );

  useEffect(() => {
    return () => {
      requestConversationFilterRefresh.cancel();
      conversationSearchRequestRef.current += 1;
    };
  }, [requestConversationFilterRefresh]);

  const handleConversationSearchTermChange = useCallback(
    (searchTerm: string) => {
      setConversationSearchTerm(searchTerm);
      conversationSearchTermRef.current = searchTerm;
      conversationSearchRequestRef.current += 1;
      setConversationSearchError(null);
      setConversationSearchHasMore(false);
      setConversationSearchLoadingMore(false);
      setConversationSearchOffset(0);
      requestConversationFilterRefresh.cancel();
      if (!searchTerm.trim() && !conversationCWDFilterRef.current.trim()) {
        setConversationSearchResults(conversations);
        setConversationSearchLoading(sidebarLoading);
        setConversationSearchTotal(conversationTotal);
        return;
      }
      setConversationSearchResults([]);
      setConversationSearchTotal(0);
      setConversationSearchLoading(true);
      requestConversationFilterRefresh();
    },
    [
      conversationTotal,
      conversations,
      requestConversationFilterRefresh,
      sidebarLoading,
    ]
  );

  const handleConversationCWDFilterChange = useCallback(
    (cwd: string) => {
      setConversationCWDFilter(cwd);
      conversationCWDFilterRef.current = cwd;
      conversationSearchRequestRef.current += 1;
      setConversationSearchError(null);
      setConversationSearchHasMore(false);
      setConversationSearchLoadingMore(false);
      setConversationSearchOffset(0);
      requestConversationFilterRefresh.cancel();
      if (!cwd.trim() && !conversationSearchTermRef.current.trim()) {
        setConversationSearchResults(conversations);
        setConversationSearchLoading(sidebarLoading);
        setConversationSearchTotal(conversationTotal);
        return;
      }
      setConversationSearchResults([]);
      setConversationSearchTotal(0);
      setConversationSearchLoading(true);
      void refreshConversationSearch();
    },
    [
      conversationTotal,
      conversations,
      refreshConversationSearch,
      requestConversationFilterRefresh,
      sidebarLoading,
    ]
  );

  const handleCloseConversationSearch = useCallback(() => {
    setSidebarSearchOpen(false);
    requestConversationFilterRefresh.cancel();
    conversationSearchRequestRef.current += 1;
    setConversationSearchTerm('');
    conversationSearchTermRef.current = '';
    setConversationCWDFilter('');
    conversationCWDFilterRef.current = '';
    setConversationSearchResults([]);
    setConversationSearchError(null);
    setConversationSearchHasMore(false);
    setConversationSearchLoading(false);
    setConversationSearchLoadingMore(false);
    setConversationSearchOffset(0);
    setConversationSearchTotal(0);
  }, [requestConversationFilterRefresh]);

  useEffect(() => {
    if (!sidebarSearchOpen || conversationSearchTerm.trim() || conversationCWDFilter.trim()) {
      return;
    }

    setConversationSearchResults(conversations);
    setConversationSearchError(null);
    setConversationSearchHasMore(false);
    setConversationSearchLoading(sidebarLoading);
    setConversationSearchLoadingMore(false);
    setConversationSearchOffset(conversations.length);
    setConversationSearchTotal(conversationTotal);
  }, [
    conversations,
    conversationCWDFilter,
    conversationSearchTerm,
    conversationTotal,
    sidebarLoading,
    sidebarSearchOpen,
  ]);

  const refreshRunners = useCallback(async () => {
    try {
      const response = await apiService.getRunners();
      setRunners(response.runners || []);
    } catch (error) {
      console.error('Failed to load runners', error);
    }
  }, []);

  useEffect(() => {
    void refreshConversations();
    void refreshRunners();

    void apiService
      .getAuthPrincipal()
      .then(setAuthPrincipal)
      .catch((error) => {
        console.error('Failed to load authenticated principal', error);
        setAuthPrincipal(null);
      });

    void apiService
      .getChatSettings()
      .then((settings) => {
        const reasoningSettings = reasoningSettingsFromChatSettings(settings);
        const workspaceEnabled = settings.controlPlaneWorkspaceEnabled !== false;
        setChatSettings(settings);
        setChatSettingsLoaded(true);
        setSelectedProfile(settings.currentProfile || 'default');
        setNewChatProfileDraft(settings.currentProfile || 'default');
        setSelectedReasoningEffort(reasoningSettings.effort);
        setSelectedReasoningEffortOptions(reasoningSettings.options);
        setSelectedReasoningEffortExplicit(false);
        setNewChatReasoningEffortDraft(reasoningSettings.effort);
        setNewChatReasoningEffortOptions(reasoningSettings.options);
        setNewChatReasoningEffortExplicit(false);
        setReasoningSettingsLoading(false);
        setSelectedCWD(workspaceEnabled ? settings.defaultCWD || '' : '');
        setCwdQuery('');
      })
      .catch((error) => {
        console.error('Failed to load chat settings', error);
        setChatSettingsLoaded(false);
      });

    const runnerRefresh = window.setInterval(() => {
      void refreshRunners();
    }, 5000);

    return () => window.clearInterval(runnerRefresh);
  }, [refreshConversations, refreshRunners]);

  useEffect(() => {
    return () => {
      resumeStreamRef.current += 1;
      reasoningSettingsRequestRef.current += 1;
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

            if (isBlockingUIRequestEvent(event) && handleUIInputRequest(eventForConversation)) {
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
  const currentConversationIsStarting = startingNewConversation && !selectedConversationId;
  const currentConversationIsStreaming =
    Boolean(activeRunningConversationId) || currentConversationIsStarting;

  const slashCommandSuggestions = useMemo(
    () => filterSlashCommands(slashCommands, draft),
    [draft, slashCommands]
  );
  const slashCommandSuggestionsOpen =
    !currentConversationIsStreaming &&
    !steering &&
    slashSuggestionsDismissedDraft !== draft &&
    slashCommandSuggestions.length > 0;
  const activeSlashCommand = useMemo(() => {
    const selectedSuggestion = slashCommandSuggestionsOpen
      ? slashCommandSuggestions[slashCommandIndex]
      : undefined;
    if (selectedSuggestion) {
      return selectedSuggestion;
    }

    const draftCommand = getDraftSlashCommand(draft);
    if (!draftCommand) {
      return null;
    }

    return slashCommands.find((command) => command.name === draftCommand) || null;
  }, [
    draft,
    slashCommands,
    slashCommandIndex,
    slashCommandSuggestions,
    slashCommandSuggestionsOpen,
  ]);
  useEffect(() => {
    setSlashCommandIndex(-1);
    setSlashSuggestionsDismissedDraft((dismissedDraft) =>
      dismissedDraft && dismissedDraft !== draft ? null : dismissedDraft
    );
  }, [draft, slashCommands]);

  useEffect(() => {
    viewedConversationIdRef.current = conversationId;
    routerConversationIdRef.current = conversationId;
  }, [conversationId]);

  useEffect(() => {
    if (!conversationId) {
      return;
    }

    cwdSuggestionRequestRef.current += 1;
  }, [conversationId]);

  useEffect(() => {
    if (!newChatDialogOpen) {
      return undefined;
    }
    const previousFocus =
      document.activeElement instanceof HTMLElement ? document.activeElement : null;
    const returnFocus = newChatReturnFocusRef.current || previousFocus;

    const focusInput = window.setTimeout(() => {
      const input = cwdInputRef.current;
      if (!input) {
        return;
      }

      input.focus();
      const valueLength = input.value.length;
      input.setSelectionRange(valueLength, valueLength);
    }, 0);

    const handlePointerDown = (event: MouseEvent) => {
      const dialog = newChatDialogRef.current;
      if (!dialog) {
        return;
      }

      const eventPath = typeof event.composedPath === 'function' ? event.composedPath() : [];
      if (eventPath.includes(dialog) || dialog.contains(event.target as Node | null)) {
        return;
      }

      setNewChatProfileDraft(selectedProfile || chatSettings.currentProfile || 'default');
      cwdSuggestionSkipQueryRef.current = null;
      requestCwdSuggestions.cancel();
      cwdSuggestionRequestRef.current += 1;
      setCwdQuery(selectedCWD || chatSettings.defaultCWD || '');
      setNewChatDialogOpen(false);
    };

    const handleKeyDown = (event: KeyboardEvent) => {
      if (event.key === 'Escape') {
        event.preventDefault();
        event.stopPropagation();
        setNewChatProfileDraft(selectedProfile || chatSettings.currentProfile || 'default');
        cwdSuggestionSkipQueryRef.current = null;
        requestCwdSuggestions.cancel();
        cwdSuggestionRequestRef.current += 1;
        setCwdQuery(selectedCWD || chatSettings.defaultCWD || '');
        setNewChatDialogOpen(false);
        return;
      }
      if (event.key !== 'Tab' || !newChatDialogRef.current) {
        return;
      }

      const focusableElements = Array.from(
        newChatDialogRef.current.querySelectorAll<HTMLElement>(OVERLAY_FOCUSABLE_SELECTOR)
      ).filter((element) => !element.hasAttribute('disabled'));
      if (focusableElements.length === 0) {
        event.preventDefault();
        newChatDialogRef.current.focus();
        return;
      }

      const firstElement = focusableElements[0];
      const lastElement = focusableElements[focusableElements.length - 1];
      if (event.shiftKey && document.activeElement === firstElement) {
        event.preventDefault();
        lastElement.focus();
        return;
      }
      if (!event.shiftKey && document.activeElement === lastElement) {
        event.preventDefault();
        firstElement.focus();
      }
    };

    window.addEventListener('mousedown', handlePointerDown);
    window.addEventListener('keydown', handleKeyDown, true);

    return () => {
      window.clearTimeout(focusInput);
      window.removeEventListener('mousedown', handlePointerDown);
      window.removeEventListener('keydown', handleKeyDown, true);
      window.setTimeout(() => {
        if (returnFocus?.isConnected) {
          returnFocus.focus();
          return;
        }
        document.querySelector<HTMLElement>('button.composer-inline-context')?.focus();
      }, 0);
    };
  }, [newChatDialogOpen]);

  useEffect(() => {
    return () => {
      attachments.forEach((attachment) => {
        if (attachment.previewUrl.startsWith('blob:')) {
          URL.revokeObjectURL(attachment.previewUrl);
        }
      });
    };
  }, [attachments]);

  useEffect(() => {
    if (mobileLayout) {
      setSidebarVisible(false);
      return;
    }

    restoringDesktopSidebarRef.current = true;
    setSidebarVisible(desktopSidebarVisibleRef.current);
  }, [mobileLayout]);

  useEffect(() => {
    if (mobileLayout) {
      return;
    }

    if (restoringDesktopSidebarRef.current) {
      if (sidebarVisible === desktopSidebarVisibleRef.current) {
        restoringDesktopSidebarRef.current = false;
      }
      return;
    }

    desktopSidebarVisibleRef.current = sidebarVisible;
    window.localStorage.setItem(SIDEBAR_VISIBLE_STORAGE_KEY, String(sidebarVisible));
  }, [mobileLayout, sidebarVisible]);

  useEffect(() => {
    if (higherPriorityDialogOpen) {
      return undefined;
    }

    const overlay = workspaceOverlayOpen
      ? workspaceToolsRef.current
      : sidebarOverlayOpen
        ? sidebarShellRef.current
        : null;
    if (!overlay) {
      return undefined;
    }

    const previousFocus =
      document.activeElement instanceof HTMLElement ? document.activeElement : null;
    const returnFocus = sidebarOverlayOpen ? sidebarReturnFocusRef.current : previousFocus;
    const focusOverlay = window.setTimeout(() => {
      const initialFocus = overlay.querySelector<HTMLElement>(
        workspaceOverlayOpen
          ? '[data-testid="workspace-tools-toggle"]'
          : '[data-testid="sidebar-hide-button"]'
      );
      if (initialFocus && !initialFocus.hasAttribute('disabled')) {
        initialFocus.focus();
        return;
      }
      overlay.focus();
    }, 0);

    const handleKeyDown = (event: KeyboardEvent) => {
      if (event.key === 'Escape' && sidebarOverlayOpen) {
        event.preventDefault();
        setSidebarVisible(false);
        return;
      }
      if (event.key !== 'Tab') {
        return;
      }

      const focusableElements = Array.from(
        overlay.querySelectorAll<HTMLElement>(OVERLAY_FOCUSABLE_SELECTOR)
      ).filter((element) => !element.hasAttribute('disabled') && !element.closest('[inert]'));
      if (focusableElements.length === 0) {
        event.preventDefault();
        overlay.focus();
        return;
      }

      const firstElement = focusableElements[0];
      const lastElement = focusableElements[focusableElements.length - 1];
      const activeElement = document.activeElement;
      if (!activeElement || !overlay.contains(activeElement)) {
        event.preventDefault();
        (event.shiftKey ? lastElement : firstElement).focus();
        return;
      }
      if (event.shiftKey && activeElement === firstElement) {
        event.preventDefault();
        lastElement.focus();
        return;
      }
      if (!event.shiftKey && activeElement === lastElement) {
        event.preventDefault();
        firstElement.focus();
      }
    };

    window.addEventListener('keydown', handleKeyDown, true);
    return () => {
      window.clearTimeout(focusOverlay);
      window.removeEventListener('keydown', handleKeyDown, true);
      window.setTimeout(() => {
        if (returnFocus?.isConnected) {
          returnFocus.focus();
          return;
        }
        if (sidebarOverlayOpen) {
          document
            .querySelector<HTMLElement>('[data-testid="sidebar-attached-toggle-mobile"]')
            ?.focus();
        }
      }, 0);
    };
  }, [higherPriorityDialogOpen, sidebarOverlayOpen, workspaceOverlayOpen]);

  useEffect(() => {
    if (workspacePanelView !== 'terminal' || higherPriorityDialogOpen || sidebarOverlayOpen) {
      return undefined;
    }

    const workspace = workspaceToolsRef.current;
    if (!workspace) {
      return undefined;
    }

    const handleTerminalExitKey = (event: KeyboardEvent) => {
      if (event.key !== 'F6') {
        return;
      }

      const terminalHost = workspace.querySelector<HTMLElement>('.workspace-terminal-host');
      const activeElement = document.activeElement;
      if (
        !terminalHost ||
        !(activeElement instanceof HTMLElement) ||
        !terminalHost.contains(activeElement)
      ) {
        return;
      }

      const target = workspace.querySelector<HTMLElement>(
        event.shiftKey
          ? '[data-testid="workspace-tools-diff-tab"]'
          : '[data-testid="workspace-tools-toggle"]'
      );
      if (!target) {
        return;
      }

      event.preventDefault();
      event.stopPropagation();
      target.focus();
    };

    window.addEventListener('keydown', handleTerminalExitKey, true);
    return () => {
      window.removeEventListener('keydown', handleTerminalExitKey, true);
    };
  }, [higherPriorityDialogOpen, sidebarOverlayOpen, workspacePanelView]);

  useEffect(() => {
    window.localStorage.setItem(SIDEBAR_WIDTH_STORAGE_KEY, String(sidebarWidth));
  }, [sidebarWidth]);

  useEffect(() => {
    if (!isResizingSidebar) {
      return undefined;
    }

    const previousUserSelect = document.body.style.userSelect;
    const previousCursor = document.body.style.cursor;
    document.body.style.userSelect = 'none';
    document.body.style.cursor = 'col-resize';

    const handleMouseMove = (event: MouseEvent) => {
      const resizeStart = sidebarResizeStartRef.current;
      if (!resizeStart) {
        return;
      }

      const nextWidth = clampSidebarWidth(
        resizeStart.startWidth + (event.clientX - resizeStart.startX)
      );
      sidebarWidthRef.current = nextWidth;
      sidebarShellRef.current?.style.setProperty('--sidebar-width', `${nextWidth}px`);
    };

    const stopResizing = () => {
      sidebarResizeStartRef.current = null;
      setSidebarWidth(sidebarWidthRef.current);
      setIsResizingSidebar(false);
    };

    window.addEventListener('mousemove', handleMouseMove);
    window.addEventListener('mouseup', stopResizing);

    return () => {
      document.body.style.userSelect = previousUserSelect;
      document.body.style.cursor = previousCursor;
      window.removeEventListener('mousemove', handleMouseMove);
      window.removeEventListener('mouseup', stopResizing);
    };
  }, [isResizingSidebar]);

  useEffect(() => {
    const interval = window.setInterval(() => {
      setStatusTick((current) => current + 1);
    }, 30000);

    return () => {
      window.clearInterval(interval);
    };
  }, []);

  useEffect(() => {
    if (conversationId && conversationPathOverrideRef.current === `/c/${conversationId}`) {
      return;
    }
    conversationPathOverrideRef.current = null;
    shouldAutoScrollRef.current = true;

    resumeStreamRef.current += 1;
    setActiveConversationId(conversationId);
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
  }, [conversationId]);

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

          if (isBlockingUIRequestEvent(event) && handleUIInputRequest(eventForConversation)) {
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

          if (handleUIInputRequest(eventForConversation)) {
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
    loadedConversationId,
    markConversationRunning,
    refreshConversations,
  ]);

  const handleTranscriptScroll = (event: React.UIEvent<HTMLDivElement>) => {
    shouldAutoScrollRef.current = isScrolledNearBottom(event.currentTarget);
  };

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
    if (currentConversationIsStarting) {
      return;
    }
    closeMobileSidebar();
    newChatReturnFocusRef.current =
      document.activeElement instanceof HTMLElement ? document.activeElement : null;

    setConversation(null);
    setActiveConversationId(null);
    setMessages([]);
    setConversationError(null);
    setStreamError(null);
    setSelectedProfile(chatSettings.currentProfile || 'default');
    setNewChatProfileDraft(chatSettings.currentProfile || 'default');
    const reasoningSettings = reasoningSettingsFromChatSettings(chatSettings);
    setSelectedReasoningEffort(reasoningSettings.effort);
    setSelectedReasoningEffortOptions(reasoningSettings.options);
    setSelectedReasoningEffortExplicit(false);
    setNewChatReasoningEffortDraft(reasoningSettings.effort);
    setNewChatReasoningEffortOptions(reasoningSettings.options);
    setNewChatReasoningEffortExplicit(false);
    setSelectedRunnerID('');
    setNewChatRunnerDraft('');
    setSelectedEnvironmentProfile('');
    setNewChatEnvironmentProfileDraft('');
    reasoningSettingsRequestRef.current += 1;
    setReasoningSettingsLoading(false);
    const defaultCWD = controlPlaneWorkspaceEnabled ? chatSettings.defaultCWD || '' : '';
    setSelectedCWD(defaultCWD);
    cwdSuggestionSkipQueryRef.current = defaultCWD;
    requestCwdSuggestions.cancel();
    cwdSuggestionRequestRef.current += 1;
    setCwdQuery(defaultCWD);
    cwdInputFocusedRef.current = false;
    setCwdSuggestions([]);
    setCwdSuggestionsOpen(false);
    setCwdSuggestionIndex(-1);
    startTransition(() => {
      navigate('/');
    });
    setNewChatDialogOpen(true);
  };

  const requestCwdSuggestions = useMemo(
    () =>
      debounce((query: string) => {
        const requestId = cwdSuggestionRequestRef.current + 1;
        cwdSuggestionRequestRef.current = requestId;

        void apiService
          .getCWDHints(query)
          .then((response) => {
            if (cwdSuggestionRequestRef.current !== requestId || viewedConversationIdRef.current) {
              return;
            }

            setCwdSuggestions(response.hints || []);
            setCwdSuggestionsOpen(cwdInputFocusedRef.current && (response.hints || []).length > 0);
            setCwdSuggestionIndex(-1);
          })
          .catch((error) => {
            if (cwdSuggestionRequestRef.current !== requestId || viewedConversationIdRef.current) {
              return;
            }

            console.error('Failed to load cwd suggestions', error);
            setCwdSuggestions([]);
            setCwdSuggestionsOpen(false);
          });
      }, 150),
    []
  );

  useEffect(() => {
    return () => {
      requestCwdSuggestions.cancel();
    };
  }, [requestCwdSuggestions]);

  useEffect(() => {
    if (conversationId || !controlPlaneWorkspaceEnabled) {
      requestCwdSuggestions.cancel();
      cwdInputFocusedRef.current = false;
      setCwdSuggestions([]);
      setCwdSuggestionsOpen(false);
      setCwdSuggestionIndex(-1);
      return;
    }

    if (!cwdQuery.trim()) {
      cwdSuggestionSkipQueryRef.current = null;
      requestCwdSuggestions.cancel();
      cwdSuggestionRequestRef.current += 1;
      setCwdSuggestions([]);
      setCwdSuggestionsOpen(false);
      setCwdSuggestionIndex(-1);
      return;
    }

    if (cwdSuggestionSkipQueryRef.current === cwdQuery) {
      requestCwdSuggestions.cancel();
      cwdSuggestionRequestRef.current += 1;
      setCwdSuggestions([]);
      setCwdSuggestionsOpen(false);
      setCwdSuggestionIndex(-1);
      return;
    }
    cwdSuggestionSkipQueryRef.current = null;

    requestCwdSuggestions(cwdQuery);
  }, [controlPlaneWorkspaceEnabled, conversationId, cwdQuery, requestCwdSuggestions]);

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
    const selectedConversation = conversationSearchResults.find(
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

  const handleSidebarToggle = () => {
    if (workspaceOverlayLayout && !sidebarVisible) {
      setWorkspacePanelView(null);
    }
    if (!sidebarVisible) {
      const activeElement =
        document.activeElement instanceof HTMLElement && document.activeElement !== document.body
          ? document.activeElement
          : null;
      sidebarReturnFocusRef.current =
        activeElement ||
        document.querySelector<HTMLElement>('[data-testid="sidebar-attached-toggle-mobile"]') ||
        document.querySelector<HTMLElement>('[data-testid="sidebar-attached-toggle"]');
    }
    setSidebarVisible(!sidebarVisible);
  };

  const handleOpenSidebarSearch = () => {
    closeMobileSidebar();
    setConversationSearchResults(conversations);
    setConversationSearchError(null);
    setConversationSearchHasMore(false);
    setConversationSearchLoading(false);
    setConversationSearchLoadingMore(false);
    setConversationSearchOffset(conversations.length);
    setConversationSearchTotal(conversationTotal);
    setSidebarSearchOpen(true);
  };

  const handleLoadMoreConversationSearch = () => {
    if (
      conversationSearchLoading ||
      conversationSearchLoadingMore ||
      !conversationSearchHasMore ||
      (!conversationSearchTermRef.current.trim() && !conversationCWDFilterRef.current.trim())
    ) {
      return;
    }

    void refreshConversationSearch(conversationSearchOffset);
  };

  const handleSidebarResizeStart = (event: React.MouseEvent<HTMLElement>) => {
    event.preventDefault();
    sidebarWidthRef.current = sidebarWidth;
    sidebarResizeStartRef.current = {
      startX: event.clientX,
      startWidth: sidebarWidth,
    };
    setIsResizingSidebar(true);
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
    if (event.kind === 'ui-notification' && event.ui_notify) {
      showToast(event.ui_notify.message, 'info', event.ui_notify.title);
      return true;
    }
    if (isBlockingUIRequestEvent(event)) {
      reasoningSettingsRequestRef.current += 1;
      requestCwdSuggestions.cancel();
      cwdSuggestionRequestRef.current += 1;
      handleCloseConversationSearch();
      setNewChatDialogOpen(false);
      setCwdSuggestionsOpen(false);
      setCwdSuggestionIndex(-1);
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
    const steeringSubmission = currentConversationIsStreaming && canSteerActiveConversation;
    const attachmentsForSubmit = attachments;
    if ((!prompt && attachments.length === 0) || steering) {
      return;
    }
    if (steeringSubmission && !prompt) {
      showToast('Steering requires a text message', 'error');
      return;
    }

    if (currentConversationIsStreaming) {
      const targetConversationId = activeRunningConversationId;
      if (!canSteerActiveConversation) {
        return;
      }

      if (!targetConversationId) {
        return;
      }

      setSteering(true);
      setStreamError(null);

      try {
        const queuedContent = buildUserContent(prompt, attachmentsForSubmit);
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
    const attachmentsForSend = attachmentsForSubmit;
    const initialUserContent = buildUserContent(prompt, attachmentsForSend);
    setAttachments([]);
    setMessages((currentMessages) => [
      ...currentMessages,
      {
        role: 'user',
        content: initialUserContent,
      },
    ]);
    const targetConversationId = conversationId || generateConversationId();
    const isNewConversation = !conversationId;
    setStartingNewConversation(false);
    if (isNewConversation) {
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
        runnerId: selectedRunnerID || undefined,
        environmentProfile: selectedRunnerID ? selectedEnvironmentProfile || undefined : undefined,
        runner: selectedRunnerID ? currentRunner : undefined,
        profile: selectedProfile,
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

    try {
      await apiService.streamChat(
        {
          message: prompt,
          content: initialUserContent,
          conversationId: targetConversationId,
          runnerId: conversationId ? undefined : selectedRunnerID || undefined,
          environmentProfile:
            conversationId || !selectedRunnerID
              ? undefined
              : selectedEnvironmentProfile || undefined,
          profile: conversationId ? undefined : selectedProfile,
          reasoningEffort:
            conversationId || !chatSettingsLoaded ? undefined : selectedReasoningEffort,
          clientCapabilities: {
            interactiveUI: true,
            persistentSurfaces: false,
          },
          cwd: conversationId || selectedRunnerID ? undefined : currentCWDLabel || undefined,
        },
        {
          signal: controller.signal,
          onEvent: (event: ChatStreamEvent) => {
            if (event.kind === 'conversation' && event.conversation_id) {
              const streamedId = event.conversation_id;
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
              if (shouldAdoptStreamedConversation) {
                setActiveConversationId(streamedId);
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
              setStartingNewConversation(false);
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
                      cwd: currentCWDLabel,
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

      if (
        streamedConversationId &&
        (viewedConversationIdRef.current === streamedConversationId ||
          finishedOnStartedConversation)
      ) {
        const latestConversation = normalizeConversation(
          await apiService.getConversation(streamedConversationId)
        );
        setConversation(latestConversation);
        setMessages(conversationToChatMessages(latestConversation));
        if (streamedConversationId !== routerConversationIdRef.current) {
          conversationPathOverrideRef.current = null;
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
      if (!streamedConversationId) {
        setStartingNewConversation(false);
      }
      clearRunningConversationForController(streamedConversationId, controller);
    }
  };

  const handleSelectSlashCommand = (commandName: string) => {
    setDraft((currentDraft) => insertSlashCommand(currentDraft, commandName));
    setSlashCommandIndex(-1);
    setSlashSuggestionsDismissedDraft(null);
  };

  const handleDraftKeyDown = (event: React.KeyboardEvent<HTMLTextAreaElement>) => {
    if (slashCommandSuggestionsOpen && slashCommandSuggestions.length > 0) {
      if (event.key === 'ArrowDown') {
        event.preventDefault();
        setSlashCommandIndex((current) =>
          current >= slashCommandSuggestions.length - 1 ? -1 : current + 1
        );
        return;
      }

      if (event.key === 'ArrowUp') {
        event.preventDefault();
        setSlashCommandIndex((current) =>
          current < 0 ? slashCommandSuggestions.length - 1 : current <= 0 ? -1 : current - 1
        );
        return;
      }

      if (event.key === 'Tab' || event.key === 'Enter') {
        event.preventDefault();
        const command =
          slashCommandSuggestions[slashCommandIndex >= 0 ? slashCommandIndex : 0] ||
          slashCommandSuggestions[0];
        if (command) {
          handleSelectSlashCommand(command.name);
        }
        return;
      }

      if (event.key === 'Escape') {
        event.preventDefault();
        setSlashCommandIndex(-1);
        setSlashSuggestionsDismissedDraft(draft);
        return;
      }
    }

    if (event.key === 'Enter' && event.shiftKey) {
      event.preventDefault();
      void handleSubmit();
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
    setStartingNewConversation(false);
    clearRunningConversation(conversationToStop);
    setUIRequestDialog(null);
    void apiService.stopConversation(conversationToStop).catch((error) => {
      console.error('Failed to stop conversation', error);
    });
    showToast('Stopped the active conversation', 'info');
  };

  const appendAttachments = async (files: File[]) => {
    if (files.length === 0) {
      return;
    }

    const remainingSlots = Math.max(MAX_IMAGE_ATTACHMENTS - attachments.length, 0);
    if (remainingSlots === 0) {
      showToast(`You can attach up to ${MAX_IMAGE_ATTACHMENTS} images`, 'error');
      return;
    }

    try {
      const nextAttachments = await Promise.all(
        files.slice(0, remainingSlots).map(fileToPendingAttachment)
      );
      setAttachments((currentAttachments) => [...currentAttachments, ...nextAttachments]);
    } catch (error) {
      const message = error instanceof Error ? error.message : 'Failed to add image';
      showToast(message, 'error');
    }
  };

  const handleRemoveAttachment = (attachmentIdToRemove: string) => {
    setAttachments((currentAttachments) =>
      currentAttachments.filter((attachment) => attachment.id !== attachmentIdToRemove)
    );
  };

  const handlePaste = async (event: React.ClipboardEvent<HTMLTextAreaElement>) => {
    const items = Array.from(event.clipboardData?.items || []);
    const imageFiles = items
      .filter((item) => item.kind === 'file' && item.type.startsWith('image/'))
      .map((item) => item.getAsFile())
      .filter((file): file is File => file !== null);

    if (imageFiles.length === 0) {
      return;
    }

    event.preventDefault();
    await appendAttachments(imageFiles);
  };

  const handleDragOver = (event: React.DragEvent<HTMLDivElement>) => {
    if (currentConversationIsStreaming && !canSteerActiveConversation) {
      return;
    }

    if (Array.from(event.dataTransfer.items || []).some((item) => item.kind === 'file')) {
      event.preventDefault();
      setDragActive(true);
    }
  };

  const handleDragLeave = (event: React.DragEvent<HTMLDivElement>) => {
    if (!event.currentTarget.contains(event.relatedTarget as Node | null)) {
      setDragActive(false);
    }
  };

  const handleDrop = async (event: React.DragEvent<HTMLDivElement>) => {
    event.preventDefault();
    setDragActive(false);

    if (currentConversationIsStreaming && !canSteerActiveConversation) {
      return;
    }

    const files = Array.from(event.dataTransfer.files || []).filter((file) =>
      file.type.startsWith('image/')
    );
    await appendAttachments(files);
  };

  const heading = useMemo(() => {
    if (conversation?.summary) {
      return conversation.summary;
    }
    return getGreeting();
  }, [conversation?.summary]);

  const currentProfileLabel = useMemo(() => {
    if (conversationId) {
      return conversation?.profile || 'default';
    }
    return selectedProfile || 'default';
  }, [conversation?.profile, conversationId, selectedProfile]);

  const currentReasoningEffortLabel = useMemo(() => {
    if (conversationId) {
      return conversation?.reasoningEffort || '';
    }
    return selectedReasoningEffort;
  }, [conversation?.reasoningEffort, conversationId, selectedReasoningEffort]);

  const currentRunnerID = conversationId ? conversation?.runnerId || '' : selectedRunnerID;
  const currentRunner = useMemo(
    () => runners.find((runner) => runner.id === currentRunnerID) || conversation?.runner,
    [conversation?.runner, currentRunnerID, runners]
  );
  const isRemoteConversation = Boolean(currentRunnerID);
  const executionEnvironmentAvailable = controlPlaneWorkspaceEnabled || isRemoteConversation;
  const currentEnvironmentProfile = conversationId
    ? conversation?.environmentProfile || ''
    : selectedEnvironmentProfile;

  const currentCWDLabel = useMemo(() => {
    if (currentRunnerID) {
      return currentRunner?.workspace.path || conversation?.cwd || 'Remote runner';
    }
    const isStartedConversationAwaitingLoad =
      Boolean(conversationId) &&
      loadedConversationId !== conversationId &&
      conversationPathOverrideRef.current === `/c/${conversationId}`;

    if (isStartedConversationAwaitingLoad) {
      return selectedCWD || chatSettings.defaultCWD || '';
    }

    if (conversationId) {
      return conversation?.cwd || chatSettings.defaultCWD || '';
    }
    return selectedCWD || chatSettings.defaultCWD || '';
  }, [
    chatSettings.defaultCWD,
    conversation?.cwd,
    conversationId,
    currentRunner?.workspace.path,
    currentRunnerID,
    loadedConversationId,
    selectedCWD,
  ]);

  useEffect(() => {
    if (isRemoteConversation || !controlPlaneWorkspaceEnabled) {
      setSlashCommands([]);
      return undefined;
    }
    let cancelled = false;

    void apiService
      .getSlashCommands(currentCWDLabel || undefined)
      .then((response) => {
        if (!cancelled) {
          setSlashCommands(response.commands || []);
        }
      })
      .catch((error) => {
        if (!cancelled) {
          console.error('Failed to load slash commands', error);
        }
      });

    return () => {
      cancelled = true;
    };
  }, [controlPlaneWorkspaceEnabled, currentCWDLabel, isRemoteConversation]);

  useEffect(() => {
    if (isRemoteConversation || !controlPlaneWorkspaceEnabled) {
      setWorkspacePanelView(null);
    }
  }, [controlPlaneWorkspaceEnabled, isRemoteConversation]);

  const applyCwdSuggestion = (path: string) => {
    cwdSuggestionSkipQueryRef.current = path;
    requestCwdSuggestions.cancel();
    cwdSuggestionRequestRef.current += 1;
    setCwdQuery(path);
    setCwdSuggestions([]);
    setCwdSuggestionsOpen(false);
    setCwdSuggestionIndex(-1);
  };

  const handleRecentWorkspaceSelect = (path: string) => {
    applyCwdSuggestion(path);
    cwdInputRef.current?.focus();
  };

  const handleCwdInputChange = (value: string) => {
    cwdSuggestionSkipQueryRef.current = null;
    setCwdQuery(value);
    setCwdSuggestionsOpen(false);
    setCwdSuggestionIndex(-1);
  };

  const handleCwdInputKeyDown = (event: React.KeyboardEvent<HTMLInputElement>) => {
    if (cwdSuggestionsOpen && cwdSuggestions.length > 0 && event.key === 'ArrowDown') {
      event.preventDefault();
      setCwdSuggestionIndex((current) => (current >= cwdSuggestions.length - 1 ? 0 : current + 1));
      return;
    }

    if (cwdSuggestionsOpen && cwdSuggestions.length > 0 && event.key === 'ArrowUp') {
      event.preventDefault();
      setCwdSuggestionIndex((current) => (current <= 0 ? cwdSuggestions.length - 1 : current - 1));
      return;
    }

    if (!event.shiftKey && event.key === 'Tab' && cwdSuggestionsOpen && cwdSuggestions.length > 0) {
      event.preventDefault();
      const suggestion = cwdSuggestions[cwdSuggestionIndex >= 0 ? cwdSuggestionIndex : 0];
      if (suggestion) {
        applyCwdSuggestion(suggestion.path);
      }
      return;
    }

    if (
      event.key === 'Enter' &&
      cwdSuggestionsOpen &&
      cwdSuggestions.length > 0 &&
      cwdSuggestionIndex >= 0
    ) {
      event.preventDefault();
      applyCwdSuggestion(cwdSuggestions[cwdSuggestionIndex].path);
      return;
    }

    if (event.key === 'Enter') {
      event.preventDefault();
      const trimmedQuery = cwdQuery.trim();
      cwdSuggestionSkipQueryRef.current = trimmedQuery;
      requestCwdSuggestions.cancel();
      cwdSuggestionRequestRef.current += 1;
      setCwdQuery(trimmedQuery);
      setCwdSuggestions([]);
      setCwdSuggestionsOpen(false);
      setCwdSuggestionIndex(-1);
      return;
    }

    if (event.key === 'Escape') {
      if (cwdSuggestionsOpen) {
        setCwdSuggestionsOpen(false);
        setCwdSuggestionIndex(-1);
        return;
      }
    }
  };

  const handleNewChatProfileDraftChange = (profileName: string) => {
    const previousEffort = newChatReasoningEffortDraft;
    const previousEffortWasExplicit = newChatReasoningEffortExplicit;
    const requestId = reasoningSettingsRequestRef.current + 1;
    reasoningSettingsRequestRef.current = requestId;

    setNewChatProfileDraft(profileName);
    setReasoningSettingsLoading(true);

    void apiService
      .getChatSettings(profileName)
      .then((settings) => {
        if (reasoningSettingsRequestRef.current !== requestId) {
          return;
        }

        const reasoningSettings = reasoningSettingsFromChatSettings(settings);
        const preserveExplicitEffort =
          previousEffortWasExplicit && reasoningSettings.options.includes(previousEffort);

        setNewChatReasoningEffortOptions(reasoningSettings.options);
        setNewChatReasoningEffortDraft(
          preserveExplicitEffort ? previousEffort : reasoningSettings.effort
        );
        setNewChatReasoningEffortExplicit(preserveExplicitEffort);
        setReasoningSettingsLoading(false);
      })
      .catch((error) => {
        if (reasoningSettingsRequestRef.current !== requestId) {
          return;
        }

        console.error('Failed to load profile reasoning settings', error);
        setNewChatProfileDraft(selectedProfile || chatSettings.currentProfile || 'default');
        setNewChatReasoningEffortDraft(selectedReasoningEffort);
        setNewChatReasoningEffortOptions(selectedReasoningEffortOptions);
        setNewChatReasoningEffortExplicit(selectedReasoningEffortExplicit);
        setReasoningSettingsLoading(false);
      });
  };

  const availableProfiles = useMemo(() => {
    const configuredProfiles = chatSettings.profiles || [];
    if (configuredProfiles.some((profile) => profile.name === currentProfileLabel)) {
      return configuredProfiles;
    }

    return [
      ...configuredProfiles,
      {
        name: currentProfileLabel,
        scope: conversationId ? 'conversation' : 'selected',
      },
    ];
  }, [chatSettings.profiles, conversationId, currentProfileLabel]);

  const composerContextText = useMemo(() => {
    const directoryLabel = !executionEnvironmentAvailable
      ? 'Workspace runner required'
      : currentCWDLabel
        ? truncateMiddle(currentCWDLabel, 46)
        : 'Default directory';
    const contextParts = [currentProfileLabel];
    if (currentReasoningEffortLabel) {
      contextParts.push(`effort:${currentReasoningEffortLabel}`);
    }
    contextParts.push(directoryLabel);

    return contextParts.join(' · ');
  }, [
    currentCWDLabel,
    currentProfileLabel,
    currentReasoningEffortLabel,
    executionEnvironmentAvailable,
  ]);

  const recentWorkspaces = useMemo(() => getRecentWorkspaces(conversations), [conversations]);

  const hasActiveConversationTarget = Boolean(activeRunningConversationId);
  const canSteerActiveConversation = hasActiveConversationTarget;
  const isSteeringMode = currentConversationIsStreaming && canSteerActiveConversation;
  const canSubmit =
    executionEnvironmentAvailable &&
    (isSteeringMode ? draft.trim().length > 0 : draft.trim().length > 0 || attachments.length > 0);
  const canStopActiveConversation =
    currentConversationIsStreaming && Boolean(activeRunningConversationId);
  const canStartNewChat = !currentConversationIsStarting;
  const composerPlaceholder = !executionEnvironmentAvailable
    ? conversationId
      ? 'This local conversation is read-only'
      : 'Select a workspace runner to start'
    : currentConversationIsStreaming
      ? !activeRunningConversationId
        ? 'Waiting for conversation to start…'
        : canSteerActiveConversation
          ? 'Steer the active conversation…'
          : 'Add your guidance here...'
      : activeSlashCommand
        ? getSlashCommandPlaceholder(activeSlashCommand)
        : 'Ask kodelet anything...';
  const workspaceExecutionMessage = !executionEnvironmentAvailable
    ? conversationId
      ? 'This conversation uses the disabled control-plane workspace and is read-only.'
      : 'The control-plane workspace is disabled. Select a workspace runner to start a chat.'
    : null;
  const composerSlashUsageHint =
    !currentConversationIsStreaming && !steering && activeSlashCommand
      ? getSlashCommandPlaceholder(activeSlashCommand)
      : '';
  const submitActionLabel = steering
    ? 'Queueing…'
    : currentConversationIsStreaming
      ? 'Steer'
      : 'Send';
  const stopActionLabel = canStopActiveConversation ? 'Stop' : 'Starting…';
  const composerMetaText = useMemo(() => {
    const parts: string[] = [];
    if (currentRunnerID) {
      const runnerName =
        currentRunner?.displayName || currentRunner?.workspace.name || currentRunnerID;
      parts.push(`runner:${runnerName} (${formatRunnerStatus(currentRunner)})`);
      if (currentEnvironmentProfile) {
        parts.push(`env:${currentEnvironmentProfile}`);
      }
    }
    if (!conversation) {
      return parts.join(', ');
    }

    const contextWindow = formatContextWindow(conversation.usage);

    if (contextWindow) {
      parts.push(contextWindow);
    }

    const inputTokens = conversation.usage?.inputTokens || 0;
    const outputTokens = conversation.usage?.outputTokens || 0;
    const cacheReadTokens = conversation.usage?.cacheReadInputTokens || 0;
    const cacheWriteTokens = conversation.usage?.cacheCreationInputTokens || 0;
    const tokenParts: string[] = [];

    if (inputTokens > 0) {
      tokenParts.push(
        `in ${Intl.NumberFormat('en-US', {
          notation: inputTokens >= 1000 ? 'compact' : 'standard',
          maximumFractionDigits: inputTokens >= 1000 ? 1 : 0,
        }).format(inputTokens)}`
      );
    }

    if (outputTokens > 0) {
      tokenParts.push(
        `out ${Intl.NumberFormat('en-US', {
          notation: outputTokens >= 1000 ? 'compact' : 'standard',
          maximumFractionDigits: outputTokens >= 1000 ? 1 : 0,
        }).format(outputTokens)}`
      );
    }

    if (cacheReadTokens > 0) {
      tokenParts.push(
        `cr ${Intl.NumberFormat('en-US', {
          notation: cacheReadTokens >= 1000 ? 'compact' : 'standard',
          maximumFractionDigits: cacheReadTokens >= 1000 ? 1 : 0,
        }).format(cacheReadTokens)}`
      );
    }

    if (cacheWriteTokens > 0) {
      tokenParts.push(
        `cw ${Intl.NumberFormat('en-US', {
          notation: cacheWriteTokens >= 1000 ? 'compact' : 'standard',
          maximumFractionDigits: cacheWriteTokens >= 1000 ? 1 : 0,
        }).format(cacheWriteTokens)}`
      );
    }

    if (tokenParts.length > 0) {
      parts.push(tokenParts.join(', '));
    }

    parts.push(formatCost(conversation.usage));

    if (conversation.updatedAt) {
      parts.push(formatCompactRelativeTime(conversation.updatedAt));
    }

    return parts.join(', ');
  }, [conversation, currentEnvironmentProfile, currentRunner, currentRunnerID, statusTick]);
  const pendingSteerMessages = conversation?.pendingSteer || [];

  const handleCloseNewChatDialog = () => {
    reasoningSettingsRequestRef.current += 1;
    setReasoningSettingsLoading(false);
    setNewChatProfileDraft(selectedProfile || chatSettings.currentProfile || 'default');
    setNewChatReasoningEffortDraft(selectedReasoningEffort);
    setNewChatReasoningEffortOptions(selectedReasoningEffortOptions);
    setNewChatReasoningEffortExplicit(selectedReasoningEffortExplicit);
    setNewChatRunnerDraft(selectedRunnerID);
    setNewChatEnvironmentProfileDraft(selectedEnvironmentProfile);
    cwdSuggestionSkipQueryRef.current = null;
    requestCwdSuggestions.cancel();
    cwdSuggestionRequestRef.current += 1;
    setCwdQuery(selectedCWD || chatSettings.defaultCWD || '');
    setCwdSuggestions([]);
    setCwdSuggestionsOpen(false);
    setCwdSuggestionIndex(-1);
    setNewChatDialogOpen(false);
  };

  const fetchGitDiff = async () => {
    setGitDiffLoading(true);
    setGitDiffError(null);

    try {
      const response = await apiService.getGitDiff(currentCWDLabel || undefined);
      setGitDiff(response);
    } catch (error) {
      const message = error instanceof Error ? error.message : 'Failed to load git diff';
      setGitDiffError(message);
      setGitDiff(null);
    } finally {
      setGitDiffLoading(false);
    }
  };

  const handleToggleWorkspacePanel = () => {
    if (workspacePanelView === null) {
      if (workspaceOverlayLayout) {
        setSidebarVisible(false);
      }
      setWorkspacePanelView('terminal');
      return;
    }

    setWorkspacePanelView(null);
  };

  const handleSelectGitDiffPanel = () => {
    if (workspacePanelView === 'diff') {
      return;
    }

    setWorkspacePanelView('diff');
    void fetchGitDiff();
  };

  const handleSelectTerminalPanel = () => {
    setWorkspacePanelView('terminal');
  };

  const handleCommitNewChatContext = () => {
    if (
      reasoningSettingsLoading ||
      !chatSettingsLoaded ||
      (!controlPlaneWorkspaceEnabled && !newChatRunnerDraft)
    ) {
      return;
    }

    setSelectedProfile(newChatProfileDraft || 'default');
    setSelectedReasoningEffort(newChatReasoningEffortDraft);
    setSelectedReasoningEffortOptions(newChatReasoningEffortOptions);
    setSelectedReasoningEffortExplicit(newChatReasoningEffortExplicit);
    setSelectedRunnerID(newChatRunnerDraft);
    setSelectedEnvironmentProfile(newChatRunnerDraft ? newChatEnvironmentProfileDraft.trim() : '');
    setSelectedCWD(newChatRunnerDraft ? '' : cwdQuery.trim());
    cwdSuggestionSkipQueryRef.current = null;
    requestCwdSuggestions.cancel();
    cwdSuggestionRequestRef.current += 1;
    setCwdSuggestions([]);
    setCwdSuggestionsOpen(false);
    setCwdSuggestionIndex(-1);
    setNewChatDialogOpen(false);
  };

  const workspacePanelCWDLabel = currentCWDLabel || chatSettings.defaultCWD || '';
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

      {newChatDialogOpen && !uiRequestDialog ? (
        <NewChatContextDialog
          availableProfiles={availableProfiles}
          cwdInputRef={cwdInputRef}
          cwdQuery={cwdQuery}
          cwdSuggestionIndex={cwdSuggestionIndex}
          cwdSuggestions={cwdSuggestions}
          cwdSuggestionsOpen={cwdSuggestionsOpen}
          controlPlaneWorkspaceEnabled={controlPlaneWorkspaceEnabled}
          defaultCWD={chatSettings.defaultCWD}
          profileDraft={newChatProfileDraft}
          reasoningEffortDraft={newChatReasoningEffortDraft}
          reasoningEffortLoading={reasoningSettingsLoading || !chatSettingsLoaded}
          reasoningEffortOptions={newChatReasoningEffortOptions}
          recentWorkspaces={recentWorkspaces}
          runners={runners}
          runnerIdDraft={newChatRunnerDraft}
          environmentProfileDraft={newChatEnvironmentProfileDraft}
          ref={newChatDialogRef}
          onCancel={handleCloseNewChatDialog}
          onCommit={handleCommitNewChatContext}
          onCwdInputBlur={() => {
            cwdInputFocusedRef.current = false;
            window.setTimeout(() => {
              setCwdSuggestionsOpen(false);
              setCwdSuggestionIndex(-1);
            }, 120);
          }}
          onCwdInputChange={handleCwdInputChange}
          onCwdInputFocus={() => {
            cwdInputFocusedRef.current = true;
            setCwdSuggestionsOpen(cwdQuery.trim().length > 0 && cwdSuggestions.length > 0);
          }}
          onCwdInputKeyDown={handleCwdInputKeyDown}
          onProfileDraftChange={handleNewChatProfileDraftChange}
          onReasoningEffortDraftChange={(reasoningEffort) => {
            setNewChatReasoningEffortDraft(reasoningEffort);
            setNewChatReasoningEffortExplicit(true);
          }}
          onRecentWorkspaceSelect={handleRecentWorkspaceSelect}
          onRunnerDraftChange={(runnerId) => {
            setNewChatRunnerDraft(runnerId);
            if (!runnerId) {
              setNewChatEnvironmentProfileDraft('');
            }
            if (runnerId) {
              requestCwdSuggestions.cancel();
              setCwdSuggestions([]);
              setCwdSuggestionsOpen(false);
              setCwdSuggestionIndex(-1);
            }
          }}
          onEnvironmentProfileDraftChange={setNewChatEnvironmentProfileDraft}
          onSelectCwdSuggestion={applyCwdSuggestion}
        />
      ) : null}

      {sidebarSearchOpen && !uiRequestDialog && !newChatDialogOpen ? (
        <ConversationSearchDialog
          conversations={conversationSearchResults}
          cwdFilter={conversationCWDFilter}
          cwdOptions={conversationCWDOptions}
          error={conversationSearchError}
          hasMore={conversationSearchHasMore}
          loading={conversationSearchLoading}
          loadingMore={conversationSearchLoadingMore}
          onClose={handleCloseConversationSearch}
          onCwdFilterChange={handleConversationCWDFilterChange}
          onLoadMore={handleLoadMoreConversationSearch}
          onSearchTermChange={handleConversationSearchTermChange}
          onSelectConversation={handleSelectSearchResult}
          returnFocusSelector={conversationSearchReturnFocusSelector}
          searchTerm={conversationSearchTerm}
          total={conversationSearchTotal}
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
          <div
            aria-hidden={workspaceOverlayOpen || undefined}
            aria-label={sidebarOverlayOpen ? 'Conversations' : undefined}
            aria-modal={sidebarOverlayOpen || undefined}
            className="absolute inset-y-0 left-0 z-50 w-[min(85%,360px)] max-w-full shrink-0 lg:sticky lg:top-0 lg:relative lg:z-20 lg:h-full lg:w-[var(--sidebar-width)] lg:self-start"
            data-testid="chat-sidebar-shell"
            inert={workspaceOverlayOpen || undefined}
            ref={sidebarShellRef}
            role={sidebarOverlayOpen ? 'dialog' : undefined}
            tabIndex={sidebarOverlayOpen ? -1 : undefined}
            style={{ '--sidebar-width': `${sidebarWidth}px` } as React.CSSProperties}
          >
            <ChatSidebar
              activeConversationId={conversationId}
              authPrincipal={authPrincipal}
              conversations={conversations}
              disabled={!canStartNewChat}
              loading={sidebarLoading}
              onDeleteConversation={handleDeleteConversation}
              onForkConversation={handleForkConversation}
              onHide={handleSidebarToggle}
              onNewChat={handleNewChat}
              onSearch={handleOpenSidebarSearch}
              onSelectConversation={handleSelectConversation}
              searchActive={sidebarSearchOpen}
            />
            <div
              aria-label="Resize sidebar"
              aria-orientation="vertical"
              className="sidebar-resize-edge absolute bottom-0 right-0 top-0 z-10 hidden translate-x-1/2 cursor-col-resize lg:block"
              data-testid="chat-sidebar-resizer"
              onMouseDown={handleSidebarResizeStart}
              role="separator"
              tabIndex={-1}
            />
          </div>
        ) : null}

        {!sidebarVisible ? (
          <>
            <ChatSidebarCollapsedRail
              disabled={!canStartNewChat}
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
              <div className="px-3 pb-8 pt-16 sm:px-4 md:px-8 lg:py-8">
                <div className="surface-panel max-w-3xl rounded-3xl border-kodelet-orange/20 px-6 py-5 text-kodelet-dark">
                  <p className="eyebrow-label text-kodelet-orange">Load error</p>
                  <p className="mt-3 text-sm leading-7">{conversationError}</p>
                </div>
              </div>
            ) : (
              <>
                <ChatTranscript
                  emptyStateTitle={heading}
                  isStreaming={currentConversationIsStreaming}
                  messages={messages}
                />
                {composerMetaText ? (
                  <div className="transcript-meta-strip-shell">
                    <div className="mx-auto w-full max-w-5xl px-3 sm:px-4 md:px-8">
                      <p
                        className="transcript-meta-strip"
                        data-testid="transcript-meta-strip"
                        title={composerMetaText}
                      >
                        {composerMetaText}
                      </p>
                    </div>
                  </div>
                ) : null}
                <PendingSteerList messages={pendingSteerMessages} />
                <div ref={transcriptEndRef} />
              </>
            )}
          </div>

          <ChatComposer
            addImageDisabled={
              !executionEnvironmentAvailable ||
              (currentConversationIsStreaming && !canSteerActiveConversation) ||
              steering
            }
            attachments={attachments}
            canStop={canStopActiveConversation}
            contextDisabled={currentConversationIsStreaming || steering}
            contextIsStatic={Boolean(conversationId)}
            contextText={composerContextText}
            dragActive={dragActive}
            draft={draft}
            placeholder={composerPlaceholder}
            showStop={currentConversationIsStreaming}
            slashCommandIndex={slashCommandIndex}
            slashCommandSuggestions={slashCommandSuggestions}
            slashCommandSuggestionsOpen={slashCommandSuggestionsOpen}
            slashUsageHint={composerSlashUsageHint}
            stopActionLabel={stopActionLabel}
            streamError={streamError || workspaceExecutionMessage}
            submitActionLabel={submitActionLabel}
            submitDisabled={
              steering ||
              !executionEnvironmentAvailable ||
              !canSubmit ||
              (currentConversationIsStreaming && !canSteerActiveConversation)
            }
            textareaDisabled={steering || !executionEnvironmentAvailable}
            onAttachImages={appendAttachments}
            onContextOpen={() => {
              newChatReturnFocusRef.current =
                document.activeElement instanceof HTMLElement ? document.activeElement : null;
              setNewChatProfileDraft(currentProfileLabel);
              setNewChatReasoningEffortDraft(selectedReasoningEffort);
              setNewChatReasoningEffortOptions(selectedReasoningEffortOptions);
              setNewChatReasoningEffortExplicit(selectedReasoningEffortExplicit);
              reasoningSettingsRequestRef.current += 1;
              setReasoningSettingsLoading(false);
              setNewChatRunnerDraft(selectedRunnerID);
              setNewChatEnvironmentProfileDraft(selectedEnvironmentProfile);
              setCwdQuery(
                controlPlaneWorkspaceEnabled ? selectedCWD || chatSettings.defaultCWD || '' : ''
              );
              setNewChatDialogOpen(true);
            }}
            onDragLeave={handleDragLeave}
            onDragOver={handleDragOver}
            onDrop={handleDrop}
            onDraftChange={setDraft}
            onDraftKeyDown={handleDraftKeyDown}
            onPaste={handlePaste}
            onRemoveAttachment={handleRemoveAttachment}
            onSelectSlashCommand={handleSelectSlashCommand}
            onStop={handleStop}
            onSubmit={handleSubmit}
          />
        </main>

        {controlPlaneWorkspaceEnabled && !isRemoteConversation ? (
          <aside
            aria-label="Workspace tools"
            aria-modal={workspaceOverlayOpen || undefined}
            className={cn(
              'workspace-tools-shell',
              workspacePanelOpen && 'is-open',
              sidebarVisible && 'is-obscured'
            )}
            data-testid="workspace-tools-shell"
            inert={sidebarOverlayOpen || undefined}
            ref={workspaceToolsRef}
            role={workspaceOverlayOpen ? 'dialog' : undefined}
            tabIndex={workspaceOverlayOpen ? -1 : undefined}
          >
            {workspacePanelOpen ? (
              <div className="workspace-tools-dock" data-testid="workspace-tools-dock">
                <div className="workspace-tools-tabs" role="tablist" aria-label="Workspace views">
                  <button
                    aria-label="Show terminal"
                    aria-selected={workspacePanelView === 'terminal'}
                    className={cn(
                      'workspace-tools-tab',
                      workspacePanelView === 'terminal' && 'is-active'
                    )}
                    data-testid="workspace-tools-terminal-tab"
                    onClick={handleSelectTerminalPanel}
                    role="tab"
                    type="button"
                  >
                    <SquareTerminal aria-hidden="true" className="h-4 w-4" strokeWidth={1.9} />
                    <span>Terminal</span>
                  </button>

                  <button
                    aria-label="Show changes"
                    aria-selected={workspacePanelView === 'diff'}
                    className={cn(
                      'workspace-tools-tab',
                      workspacePanelView === 'diff' && 'is-active'
                    )}
                    data-testid="workspace-tools-diff-tab"
                    onClick={handleSelectGitDiffPanel}
                    role="tab"
                    type="button"
                  >
                    <GitCompareArrows aria-hidden="true" className="h-4 w-4" strokeWidth={1.9} />
                    <span>Changes</span>
                  </button>
                </div>

                <div className="workspace-tools-content">
                  <Suspense
                    fallback={
                      <div className="workspace-modal-placeholder" role="status">
                        Loading workspace tool…
                      </div>
                    }
                  >
                    {workspacePanelView === 'terminal' ? (
                      <TerminalModal
                        cwdLabel={workspacePanelCWDLabel}
                        open
                        onClose={handleToggleWorkspacePanel}
                      />
                    ) : (
                      <GitDiffModal
                        error={gitDiffError}
                        gitDiff={gitDiff}
                        loading={gitDiffLoading}
                        open
                        onRefresh={() => {
                          void fetchGitDiff();
                        }}
                      />
                    )}
                  </Suspense>
                </div>
              </div>
            ) : null}

            <div className="workspace-tools-rail" data-testid="workspace-tools-rail">
              <button
                aria-label={workspacePanelOpen ? 'Hide workspace panel' : 'Show workspace panel'}
                aria-pressed={workspacePanelOpen}
                className="sidebar-toggle-button workspace-tools-toggle"
                data-testid="workspace-tools-toggle"
                onClick={handleToggleWorkspacePanel}
                type="button"
              >
                <PanelRight aria-hidden="true" className="h-4 w-4" strokeWidth={1.9} />
              </button>
            </div>
          </aside>
        ) : null}
      </div>
    </div>
  );
};

export default ChatPage;
