import React, {
	startTransition,
	useCallback,
	useEffect,
	useMemo,
	useRef,
	useState,
} from "react";
import { PanelLeftOpen } from "lucide-react";
import { useNavigate, useParams } from "react-router-dom";
import ChatComposer from "../components/chat/ChatComposer";
import ChatSidebar from "../components/chat/ChatSidebar";
import ChatTranscript from "../components/chat/ChatTranscript";
import NewChatContextDialog from "../components/chat/NewChatContextDialog";
import PendingSteerList from "../components/chat/PendingSteerList";
import GitDiffModal from "../components/workspace/GitDiffModal";
import TerminalModal from "../components/workspace/TerminalModal";
import {
	applyChatStreamEvent,
	conversationToChatMessages,
} from "../features/chat/state";
import apiService from "../services/api";
import type {
	CWDHint,
	ChatSettings,
	ChatStreamEvent,
	ContentBlock,
	Conversation,
	GitDiffResponse,
	PendingImageAttachment,
	SlashCommandOption,
} from "../types";
import {
	cn,
	debounce,
	formatCompactRelativeTime,
	formatContextWindow,
	formatCost,
	showToast,
	truncateMiddle,
} from "../utils";

const normalizeConversation = (conversation: Conversation): Conversation => ({
	...conversation,
	cwd:
		typeof conversation.cwd === "string" && conversation.cwd.trim()
			? conversation.cwd.trim()
			: undefined,
	profile:
		typeof conversation.profile === "string" && conversation.profile.trim()
			? conversation.profile.trim()
			: undefined,
	messages: (conversation.messages || []).map((message) => ({
		role: message.role || "user",
		content: message.content || "",
		toolCalls: message.toolCalls || message.tool_calls || [],
		thinkingText: message.thinkingText,
		thinkingTexts: message.thinkingTexts || [],
	})),
	pendingSteer: (conversation.pendingSteer || []).map((message) => ({
		role: message.role || "user",
		content: message.content || "",
	})),
	toolResults: conversation.toolResults || {},
});

const mergeConversationUsage = (
	currentConversation: Conversation | null,
	usage: Conversation["usage"],
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
		return "Good morning";
	}
	if (hour < 18) {
		return "Good afternoon";
	}
	return "Good evening";
};

const DEFAULT_SIDEBAR_WIDTH = 320;
const MIN_SIDEBAR_WIDTH = 260;
const MAX_SIDEBAR_WIDTH = 520;
const SIDEBAR_WIDTH_STORAGE_KEY = "kodelet.chat.sidebar.width";
const SIDEBAR_VISIBLE_STORAGE_KEY = "kodelet.chat.sidebar.visible";
const MAX_IMAGE_ATTACHMENTS = 10;
const MAX_IMAGE_BYTES = 5 * 1024 * 1024;
const SIDEBAR_CONVERSATION_LIMIT = 100;
const RECENT_WORKSPACE_LIMIT = 5;
const AUTO_SCROLL_BOTTOM_THRESHOLD = 80;
const SUPPORTED_IMAGE_TYPES = new Set([
	"image/png",
	"image/jpeg",
	"image/gif",
	"image/webp",
]);
const attachmentId = (): string =>
	typeof crypto !== "undefined" && "randomUUID" in crypto
		? crypto.randomUUID()
		: `attachment-${Date.now()}-${Math.random().toString(36).slice(2, 8)}`;

const readFileAsDataUrl = (file: File): Promise<string> =>
	new Promise((resolve, reject) => {
		const reader = new FileReader();
		reader.onload = () => {
			if (typeof reader.result === "string") {
				resolve(reader.result);
				return;
			}
			reject(new Error("Failed to read image data"));
		};
		reader.onerror = () =>
			reject(reader.error || new Error("Failed to read image data"));
		reader.readAsDataURL(file);
	});

const fileToPendingAttachment = async (
	file: File,
): Promise<PendingImageAttachment> => {
	if (!SUPPORTED_IMAGE_TYPES.has(file.type)) {
		throw new Error("Only PNG, JPEG, GIF, and WebP images are supported");
	}

	if (file.size > MAX_IMAGE_BYTES) {
		throw new Error("Each image must be 5MB or smaller");
	}

	const dataUrl = await readFileAsDataUrl(file);
	const [, base64 = ""] = dataUrl.split(",", 2);

	return {
		id: attachmentId(),
		name: file.name || "Pasted image",
		mediaType: file.type,
		data: base64,
		previewUrl: dataUrl,
		size: file.size,
	};
};

const buildUserContent = (
	prompt: string,
	attachments: PendingImageAttachment[],
): ContentBlock[] => [
	...(prompt ? [{ type: "text" as const, text: prompt }] : []),
	...attachments.map((attachment) => ({
		type: "image" as const,
		source: {
			data: attachment.data,
			media_type: attachment.mediaType,
		},
	})),
];

const clampSidebarWidth = (width: number): number =>
	Math.min(MAX_SIDEBAR_WIDTH, Math.max(MIN_SIDEBAR_WIDTH, width));

const isScrolledNearBottom = (element: HTMLElement): boolean =>
	element.scrollHeight - element.scrollTop - element.clientHeight <=
	AUTO_SCROLL_BOTTOM_THRESHOLD;

const buildConversationPreview = (
	prompt: string,
	attachments: PendingImageAttachment[],
): string => {
	const trimmedPrompt = prompt.trim();
	if (trimmedPrompt) {
		return trimmedPrompt;
	}

	if (attachments.length === 1) {
		return attachments[0].name || "Image attachment";
	}

	if (attachments.length > 1) {
		return `${attachments.length} image attachments`;
	}

	return "Untitled conversation";
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
		.sort(
			(left, right) =>
				getConversationTimestamp(right) - getConversationTimestamp(left),
		)
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
	if (!trimmedStart.startsWith("/")) {
		return null;
	}

	const withoutSlash = trimmedStart.slice(1);
	if (withoutSlash.includes(" ")) {
		return null;
	}

	return withoutSlash.toLowerCase();
};

const filterSlashCommands = (
	commands: SlashCommandOption[],
	draft: string,
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
	const leadingWhitespace = draft.match(/^\s*/)?.[0] || "";
	return `${leadingWhitespace}/${commandName} `;
};

const getDraftSlashCommand = (draft: string): string | null => {
	const trimmedStart = draft.trimStart();
	if (!trimmedStart.startsWith("/")) {
		return null;
	}

	const command = trimmedStart.slice(1).split(/\s+/, 1)[0];
	return command || null;
};

const getSlashCommandPlaceholder = (command: SlashCommandOption): string =>
	command.placeholder ||
	`/${command.name}${command.hint ? ` ${command.hint}` : ""}`;

const upsertConversationSummary = (
	conversations: Conversation[],
	nextConversation: Conversation,
): Conversation[] => {
	const merged = conversations.filter(
		(conversation) => conversation.id !== nextConversation.id,
	);
	merged.unshift(nextConversation);

	merged.sort((left, right) => {
		const leftTime = getConversationTimestamp(left);
		const rightTime = getConversationTimestamp(right);
		return rightTime - leftTime;
	});

	return merged;
};

const readStoredSidebarWidth = (): number => {
	if (typeof window === "undefined") {
		return DEFAULT_SIDEBAR_WIDTH;
	}

	const storedWidth = window.localStorage.getItem(SIDEBAR_WIDTH_STORAGE_KEY);
	if (storedWidth === null) {
		return DEFAULT_SIDEBAR_WIDTH;
	}

	const parsedWidth = Number(storedWidth);
	return Number.isFinite(parsedWidth)
		? clampSidebarWidth(parsedWidth)
		: DEFAULT_SIDEBAR_WIDTH;
};

const readStoredSidebarVisible = (): boolean => {
	if (typeof window === "undefined") {
		return true;
	}

	return window.localStorage.getItem(SIDEBAR_VISIBLE_STORAGE_KEY) !== "false";
};

const ChatPage: React.FC = () => {
	const navigate = useNavigate();
	const { id } = useParams<{ id: string }>();
	const conversationId = id || null;
	const [conversations, setConversations] = useState<Conversation[]>([]);
	const [conversation, setConversation] = useState<Conversation | null>(null);
	const [messages, setMessages] = useState(() =>
		conversationToChatMessages(null),
	);
	const [activeConversationId, setActiveConversationId] = useState<
		string | null
	>(conversationId);
	const [chatSettings, setChatSettings] = useState<ChatSettings>({
		profiles: [],
	});
	const [selectedProfile, setSelectedProfile] = useState("default");
	const [newChatProfileDraft, setNewChatProfileDraft] = useState("default");
	const [selectedCWD, setSelectedCWD] = useState("");
	const [cwdQuery, setCwdQuery] = useState("");
	const [cwdSuggestions, setCwdSuggestions] = useState<CWDHint[]>([]);
	const [cwdSuggestionsOpen, setCwdSuggestionsOpen] = useState(false);
	const [cwdSuggestionIndex, setCwdSuggestionIndex] = useState(-1);
	const [draft, setDraft] = useState("");
	const [slashCommands, setSlashCommands] = useState<SlashCommandOption[]>([]);
	const [slashCommandIndex, setSlashCommandIndex] = useState(-1);
	const [slashSuggestionsDismissedDraft, setSlashSuggestionsDismissedDraft] =
		useState<string | null>(null);
	const [sidebarLoading, setSidebarLoading] = useState(true);
	const [conversationLoading, setConversationLoading] = useState(false);
	const [conversationError, setConversationError] = useState<string | null>(
		null,
	);
	const [streamError, setStreamError] = useState<string | null>(null);
	const [sending, setSending] = useState(false);
	const [steering, setSteering] = useState(false);
	const [steerAvailable, setSteerAvailable] = useState(false);
	const [attachments, setAttachments] = useState<PendingImageAttachment[]>([]);
	const [dragActive, setDragActive] = useState(false);
	const [composerExpanded, setComposerExpanded] = useState(false);
	const [gitDiffOpen, setGitDiffOpen] = useState(false);
	const [gitDiffLoading, setGitDiffLoading] = useState(false);
	const [gitDiffError, setGitDiffError] = useState<string | null>(null);
	const [gitDiff, setGitDiff] = useState<GitDiffResponse | null>(null);
	const [terminalOpen, setTerminalOpen] = useState(false);
	const [sidebarVisible, setSidebarVisible] = useState(
		readStoredSidebarVisible,
	);
	const [sidebarWidth, setSidebarWidth] = useState(readStoredSidebarWidth);
	const [isResizingSidebar, setIsResizingSidebar] = useState(false);
	const [newChatDialogOpen, setNewChatDialogOpen] = useState(false);
	const [statusTick, setStatusTick] = useState(0);
	const loadedConversationId = conversation?.id ?? null;
	const transcriptEndRef = useRef<HTMLDivElement | null>(null);
	const shouldAutoScrollRef = useRef(true);
	const abortControllerRef = useRef<AbortController | null>(null);
	const resumeControllerRef = useRef<AbortController | null>(null);
	const sendStreamRef = useRef(0);
	const resumeStreamRef = useRef(0);
	const cwdSuggestionRequestRef = useRef(0);
	const cwdInputFocusedRef = useRef(false);
	const cwdSuggestionSkipQueryRef = useRef<string | null>(null);
	const viewedConversationIdRef = useRef<string | null>(conversationId);
	const conversationPathOverrideRef = useRef<string | null>(null);
	const routerConversationIdRef = useRef<string | null>(conversationId);
	const sidebarResizeStartRef = useRef<{
		startX: number;
		startWidth: number;
	} | null>(null);
	const cwdInputRef = useRef<HTMLInputElement | null>(null);
	const newChatDialogRef = useRef<HTMLDivElement | null>(null);

	const refreshConversations = useCallback(async () => {
		setSidebarLoading(true);
		try {
			const response = await apiService.getConversations({
				limit: SIDEBAR_CONVERSATION_LIMIT,
				sortBy: "updated",
				sortOrder: "desc",
			});
			setConversations(response.conversations || []);
		} catch (error) {
			console.error("Failed to load conversations", error);
		} finally {
			setSidebarLoading(false);
		}
	}, []);

	useEffect(() => {
		void refreshConversations();

		void apiService
			.getChatSettings()
			.then((settings) => {
				setChatSettings(settings);
				setSelectedProfile(settings.currentProfile || "default");
				setNewChatProfileDraft(settings.currentProfile || "default");
				setSelectedCWD(settings.defaultCWD || "");
				setCwdQuery("");
			})
			.catch((error) => {
				console.error("Failed to load chat settings", error);
			});

	}, [refreshConversations]);

	useEffect(() => {
		return () => {
			sendStreamRef.current += 1;
			resumeStreamRef.current += 1;
			abortControllerRef.current?.abort();
			resumeControllerRef.current?.abort();
		};
	}, []);

	const slashCommandSuggestions = useMemo(
		() => filterSlashCommands(slashCommands, draft),
		[draft, slashCommands],
	);
	const slashCommandSuggestionsOpen =
		!sending &&
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

		return (
			slashCommands.find((command) => command.name === draftCommand) || null
		);
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
			dismissedDraft && dismissedDraft !== draft ? null : dismissedDraft,
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

			const eventPath =
				typeof event.composedPath === "function" ? event.composedPath() : [];
			if (
				eventPath.includes(dialog) ||
				dialog.contains(event.target as Node | null)
			) {
				return;
			}

			setNewChatProfileDraft(
				selectedProfile || chatSettings.currentProfile || "default",
			);
			cwdSuggestionSkipQueryRef.current = null;
			requestCwdSuggestions.cancel();
			cwdSuggestionRequestRef.current += 1;
			setCwdQuery(selectedCWD || chatSettings.defaultCWD || "");
			setNewChatDialogOpen(false);
		};

		const handleKeyDown = (event: KeyboardEvent) => {
			if (event.key === "Escape") {
				setNewChatProfileDraft(
					selectedProfile || chatSettings.currentProfile || "default",
				);
				cwdSuggestionSkipQueryRef.current = null;
				requestCwdSuggestions.cancel();
				cwdSuggestionRequestRef.current += 1;
				setCwdQuery(selectedCWD || chatSettings.defaultCWD || "");
				setNewChatDialogOpen(false);
			}
		};

		window.addEventListener("mousedown", handlePointerDown);
		window.addEventListener("keydown", handleKeyDown);

		return () => {
			window.clearTimeout(focusInput);
			window.removeEventListener("mousedown", handlePointerDown);
			window.removeEventListener("keydown", handleKeyDown);
		};
	}, [newChatDialogOpen]);

	useEffect(() => {
		return () => {
			attachments.forEach((attachment) => {
				if (attachment.previewUrl.startsWith("blob:")) {
					URL.revokeObjectURL(attachment.previewUrl);
				}
			});
		};
	}, [attachments]);

	useEffect(() => {
		window.localStorage.setItem(
			SIDEBAR_VISIBLE_STORAGE_KEY,
			String(sidebarVisible),
		);
	}, [sidebarVisible]);

	useEffect(() => {
		window.localStorage.setItem(
			SIDEBAR_WIDTH_STORAGE_KEY,
			String(sidebarWidth),
		);
	}, [sidebarWidth]);

	useEffect(() => {
		const interval = window.setInterval(() => {
			setStatusTick((current) => current + 1);
		}, 30000);

		return () => {
			window.clearInterval(interval);
		};
	}, []);

	useEffect(() => {
		if (!isResizingSidebar) {
			return undefined;
		}

		const previousUserSelect = document.body.style.userSelect;
		const previousCursor = document.body.style.cursor;
		document.body.style.userSelect = "none";
		document.body.style.cursor = "col-resize";

		const handleMouseMove = (event: MouseEvent) => {
			const resizeStart = sidebarResizeStartRef.current;
			if (!resizeStart) {
				return;
			}

			const nextWidth = clampSidebarWidth(
				resizeStart.startWidth + (event.clientX - resizeStart.startX),
			);
			setSidebarWidth(nextWidth);
		};

		const stopResizing = () => {
			sidebarResizeStartRef.current = null;
			setIsResizingSidebar(false);
		};

		window.addEventListener("mousemove", handleMouseMove);
		window.addEventListener("mouseup", stopResizing);

		return () => {
			document.body.style.userSelect = previousUserSelect;
			document.body.style.cursor = previousCursor;
			window.removeEventListener("mousemove", handleMouseMove);
			window.removeEventListener("mouseup", stopResizing);
		};
	}, [isResizingSidebar]);

	useEffect(() => {
		if (
			conversationId &&
			conversationPathOverrideRef.current === `/c/${conversationId}`
		) {
			return;
		}
		conversationPathOverrideRef.current = null;
		shouldAutoScrollRef.current = true;

		sendStreamRef.current += 1;
		abortControllerRef.current?.abort();
		abortControllerRef.current = null;

		resumeStreamRef.current += 1;
		setActiveConversationId(conversationId);
		setSending(false);
		setSteering(false);
		setSteerAvailable(false);
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
				const message =
					error instanceof Error
						? error.message
						: "Failed to load conversation";
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
			loadedConversationId !== conversationId
		) {
			return;
		}

		const streamInstance = resumeStreamRef.current + 1;
		resumeStreamRef.current = streamInstance;
		const controller = new AbortController();
		resumeControllerRef.current = controller;
		let sawEvent = false;

		void apiService
			.streamConversation(conversationId, {
				signal: controller.signal,
				onEvent: (event: ChatStreamEvent) => {
					if (
						resumeStreamRef.current !== streamInstance ||
						viewedConversationIdRef.current !== conversationId ||
						(event.conversation_id && event.conversation_id !== conversationId)
					) {
						return;
					}

					sawEvent = true;
					if (event.kind === "conversation" && event.conversation_id) {
						setActiveConversationId(event.conversation_id);
						setSending(true);
						return;
					}

					if (event.kind === "usage" && event.usage) {
						setConversation((currentConversation) =>
							mergeConversationUsage(currentConversation, event.usage),
						);
						return;
					}

					if (event.kind === "done" || event.kind === "error") {
						setSending(false);
					}

					if (event.kind === "error") {
						setStreamError(event.error || "Chat request failed");
					}

					if (event.kind === "tool-use" || event.kind === "tool-result") {
						setSteerAvailable(true);
					}

					if (event.kind === "user-message") {
						setConversation((currentConversation) =>
							currentConversation
								? { ...currentConversation, pendingSteer: [] }
								: currentConversation,
						);
					}

					setMessages((currentMessages) =>
						applyChatStreamEvent(currentMessages, event),
					);
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
					error instanceof Error
						? error.message
						: "Failed to resume conversation stream";
				if (message !== "conversation is not actively streaming") {
					console.error("Failed to resume conversation stream", error);
				}
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

				if (sawEvent) {
					setSending(false);
					setSteerAvailable(false);
				}
			});

		return () => {
			controller.abort();
			if (resumeControllerRef.current === controller) {
				resumeControllerRef.current = null;
			}
		};
	}, [conversationId, conversationLoading, loadedConversationId]);

	const handleTranscriptScroll = (event: React.UIEvent<HTMLDivElement>) => {
		shouldAutoScrollRef.current = isScrolledNearBottom(event.currentTarget);
	};

	useEffect(() => {
		if (!shouldAutoScrollRef.current) {
			return;
		}

		transcriptEndRef.current?.scrollIntoView({
			behavior: "smooth",
			block: "end",
		});
	}, [messages, sending]);

	const handleNewChat = () => {
		if (sending && !activeConversationId) {
			return;
		}

		setConversation(null);
		setActiveConversationId(null);
		setMessages([]);
		setConversationError(null);
		setStreamError(null);
		setSelectedProfile(chatSettings.currentProfile || "default");
		setNewChatProfileDraft(chatSettings.currentProfile || "default");
		setSelectedCWD(chatSettings.defaultCWD || "");
		const defaultCWD = chatSettings.defaultCWD || "";
		cwdSuggestionSkipQueryRef.current = defaultCWD;
		requestCwdSuggestions.cancel();
		cwdSuggestionRequestRef.current += 1;
		setCwdQuery(defaultCWD);
		cwdInputFocusedRef.current = false;
		setCwdSuggestions([]);
		setCwdSuggestionsOpen(false);
		setCwdSuggestionIndex(-1);
		startTransition(() => navigate("/"));
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
						if (
							cwdSuggestionRequestRef.current !== requestId ||
							viewedConversationIdRef.current
						) {
							return;
						}

						setCwdSuggestions(response.hints || []);
						setCwdSuggestionsOpen(
							cwdInputFocusedRef.current && (response.hints || []).length > 0,
						);
						setCwdSuggestionIndex(-1);
					})
					.catch((error) => {
						if (
							cwdSuggestionRequestRef.current !== requestId ||
							viewedConversationIdRef.current
						) {
							return;
						}

						console.error("Failed to load cwd suggestions", error);
						setCwdSuggestions([]);
						setCwdSuggestionsOpen(false);
					});
			}, 150),
		[],
	);

	useEffect(() => {
		return () => {
			requestCwdSuggestions.cancel();
		};
	}, [requestCwdSuggestions]);

	useEffect(() => {
		if (conversationId) {
			requestCwdSuggestions.cancel();
			cwdInputFocusedRef.current = false;
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
	}, [conversationId, cwdQuery, requestCwdSuggestions]);

	const handleSelectConversation = (nextConversationId: string) => {
		if (nextConversationId === conversationId) {
			return;
		}

		setStreamError(null);
		startTransition(() => navigate(`/c/${nextConversationId}`));
	};

	const handleForkConversation = async (sourceConversationId: string) => {
		try {
			const response = await apiService.forkConversation(sourceConversationId);
			await refreshConversations();
			showToast("Conversation copied", "success");
			startTransition(() => navigate(`/c/${response.conversation_id}`));
		} catch (error) {
			const message =
				error instanceof Error ? error.message : "Failed to copy conversation";
			showToast(message, "error");
		}
	};

	const handleDeleteConversation = async (targetConversationId: string) => {
		if (
			sending &&
			(targetConversationId === activeConversationId ||
				targetConversationId === conversationId)
		) {
			showToast("Stop the active conversation before deleting it", "info");
			return;
		}

		try {
			await apiService.deleteConversation(targetConversationId);

			if (
				targetConversationId === conversationId ||
				targetConversationId === activeConversationId
			) {
				abortControllerRef.current?.abort();
				resumeControllerRef.current?.abort();
				setConversation(null);
				setActiveConversationId(null);
				setMessages([]);
				setConversationError(null);
				setStreamError(null);
				setSending(false);
				setSteerAvailable(false);
				startTransition(() => navigate("/"));
			}

			await refreshConversations();
			showToast("Conversation deleted", "neutral");
		} catch (error) {
			const message =
				error instanceof Error
					? error.message
					: "Failed to delete conversation";
			showToast(message, "error");
		}
	};

	const handleSidebarToggle = () => {
		setSidebarVisible((currentValue) => !currentValue);
	};

	const handleSidebarResizeStart = (event: React.MouseEvent<HTMLElement>) => {
		event.preventDefault();
		sidebarResizeStartRef.current = {
			startX: event.clientX,
			startWidth: sidebarWidth,
		};
		setIsResizingSidebar(true);
	};

	const handleSidebarResizeDoubleClick = () => {
		setSidebarVisible(false);
	};

	const updatePathForStartedConversation = (streamedId: string) => {
		const nextPath = `/c/${streamedId}`;

		conversationPathOverrideRef.current = nextPath;
		viewedConversationIdRef.current = streamedId;
		routerConversationIdRef.current = streamedId;
		startTransition(() => navigate(nextPath, { replace: true }));
	};

	const handleSubmit = async () => {
		const prompt = draft.trim();
		const steeringSubmission = sending && canSteerActiveConversation;
		const attachmentsForSubmit = attachments;
		if ((!prompt && attachments.length === 0) || steering) {
			return;
		}
		if (steeringSubmission && !prompt) {
			showToast("Steering requires a text message", "error");
			return;
		}

		if (sending) {
			if (!canSteerActiveConversation) {
				return;
			}

			if (!activeConversationId) {
				return;
			}

			setSteering(true);
			setStreamError(null);

			try {
				const queuedContent = buildUserContent(prompt, attachmentsForSubmit);
				await apiService.steerConversation(
					activeConversationId,
					prompt,
					queuedContent,
				);
				setConversation((currentConversation) =>
					currentConversation
						? {
								...currentConversation,
								pendingSteer: [
									...(currentConversation.pendingSteer || []),
									{ role: "user", content: queuedContent },
								],
							}
						: currentConversation,
				);
				setDraft("");
				setAttachments([]);
				showToast("Steering queued for the active conversation", "success");
			} catch (error) {
				const message =
					error instanceof Error
						? error.message
						: "Failed to steer conversation";
				setStreamError(message);
				showToast(message, "error");
			} finally {
				setSteering(false);
			}

			return;
		}

		setDraft("");
		setStreamError(null);
		const attachmentsForSend = attachmentsForSubmit;
		setAttachments([]);
		setMessages((currentMessages) => [
			...currentMessages,
			{
				role: "user",
				content: buildUserContent(prompt, attachmentsForSend),
			},
		]);
		setSending(true);
		setSteerAvailable(false);

		const controller = new AbortController();
		abortControllerRef.current = controller;
		const streamInstance = sendStreamRef.current + 1;
		sendStreamRef.current = streamInstance;
		const viewConversationIdAtStart = conversationId;
		const userPreview = buildConversationPreview(prompt, attachmentsForSend);

		let streamedConversationId = conversationId;
		let streamedError: string | null = null;

		try {
			await apiService.streamChat(
				{
					message: prompt,
					content: buildUserContent(prompt, attachmentsForSend),
					conversationId: conversationId || undefined,
					profile: conversationId ? undefined : selectedProfile,
					cwd: conversationId ? undefined : currentCWDLabel || undefined,
				},
				{
					signal: controller.signal,
					onEvent: (event: ChatStreamEvent) => {
						const isStillCurrentStream =
							sendStreamRef.current === streamInstance &&
							(viewedConversationIdRef.current === viewConversationIdAtStart ||
								(!viewConversationIdAtStart &&
									streamedConversationId &&
									viewedConversationIdRef.current ===
										streamedConversationId)) &&
							(viewConversationIdAtStart === null ||
								!event.conversation_id ||
								event.conversation_id === viewConversationIdAtStart);

						if (!isStillCurrentStream) {
							return;
						}

						if (event.kind === "conversation" && event.conversation_id) {
							const streamedId = event.conversation_id;
							const shouldUpdatePath =
								!viewConversationIdAtStart &&
								streamedId !== streamedConversationId;
							streamedConversationId = streamedId;
							setActiveConversationId(streamedId);
							if (shouldUpdatePath) {
								updatePathForStartedConversation(streamedId);
							}
							if (!viewConversationIdAtStart) {
								const now = new Date().toISOString();
								setConversations((currentConversations) =>
									upsertConversationSummary(currentConversations, {
										id: streamedId,
										createdAt: now,
										updatedAt: now,
										messageCount: 1,
										summary: userPreview,
										preview: userPreview,
										cwd: currentCWDLabel,
										profile: selectedProfile,
									}),
								);
							}
						}

						if (event.kind === "usage" && event.usage) {
							setConversation((currentConversation) =>
								mergeConversationUsage(currentConversation, event.usage),
							);
							return;
						}

						if (event.kind === "error") {
							streamedError = event.error || "Chat request failed";
							setStreamError(streamedError);
							return;
						}

						if (event.kind === "tool-use" || event.kind === "tool-result") {
							setSteerAvailable(true);
						}

						if (event.kind === "user-message") {
							setConversation((currentConversation) =>
								currentConversation
									? { ...currentConversation, pendingSteer: [] }
									: currentConversation,
							);
						}

						setMessages((currentMessages) =>
							applyChatStreamEvent(currentMessages, event),
						);
					},
				},
			);

			const finishedOnStartedConversation = Boolean(
				!viewConversationIdAtStart &&
					streamedConversationId &&
					viewedConversationIdRef.current === streamedConversationId,
			);

			if (
				sendStreamRef.current !== streamInstance ||
				(viewedConversationIdRef.current !== viewConversationIdAtStart &&
					!finishedOnStartedConversation)
			) {
				return;
			}

			if (streamedError) {
				conversationPathOverrideRef.current = null;
				showToast(streamedError, "error");
				await refreshConversations();
				return;
			}

			if (streamedConversationId) {
				const latestConversation = normalizeConversation(
					await apiService.getConversation(streamedConversationId),
				);
				setConversation(latestConversation);
				setMessages(conversationToChatMessages(latestConversation));
				if (streamedConversationId !== routerConversationIdRef.current) {
					conversationPathOverrideRef.current = null;
					startTransition(() =>
						navigate(`/c/${streamedConversationId}`, { replace: true }),
					);
				}
			}

			await refreshConversations();
		} catch (error) {
			if (error instanceof DOMException && error.name === "AbortError") {
				return;
			}

			const failedOnStartedConversation = Boolean(
				!viewConversationIdAtStart &&
					streamedConversationId &&
					viewedConversationIdRef.current === streamedConversationId,
			);

			if (
				sendStreamRef.current !== streamInstance ||
				(viewedConversationIdRef.current !== viewConversationIdAtStart &&
					!failedOnStartedConversation)
			) {
				return;
			}

			conversationPathOverrideRef.current = null;
			setAttachments(attachmentsForSend);
			const message =
				error instanceof Error ? error.message : "Failed to send message";
			setStreamError(message);
			showToast(message, "error");
		} finally {
			const finishedOnCurrentConversation =
				viewedConversationIdRef.current === viewConversationIdAtStart ||
				Boolean(
					!viewConversationIdAtStart &&
						streamedConversationId &&
						viewedConversationIdRef.current === streamedConversationId,
				);

			if (
				abortControllerRef.current === controller &&
				sendStreamRef.current === streamInstance &&
				finishedOnCurrentConversation
			) {
				abortControllerRef.current = null;
				setSending(false);
				setSteerAvailable(false);
			}
		}
	};

	const handleSelectSlashCommand = (commandName: string) => {
		setDraft((currentDraft) => insertSlashCommand(currentDraft, commandName));
		setSlashCommandIndex(-1);
		setSlashSuggestionsDismissedDraft(null);
	};

	const handleDraftKeyDown = (
		event: React.KeyboardEvent<HTMLTextAreaElement>,
	) => {
		if (slashCommandSuggestionsOpen && slashCommandSuggestions.length > 0) {
			if (event.key === "ArrowDown") {
				event.preventDefault();
				setSlashCommandIndex((current) =>
					current >= slashCommandSuggestions.length - 1 ? -1 : current + 1,
				);
				return;
			}

			if (event.key === "ArrowUp") {
				event.preventDefault();
				setSlashCommandIndex((current) =>
					current < 0 ? slashCommandSuggestions.length - 1 : current <= 0 ? -1 : current - 1,
				);
				return;
			}

			if (event.key === "Tab" || event.key === "Enter") {
				event.preventDefault();
				const command =
					slashCommandSuggestions[
						slashCommandIndex >= 0 ? slashCommandIndex : 0
					] || slashCommandSuggestions[0];
				if (command) {
					handleSelectSlashCommand(command.name);
				}
				return;
			}

			if (event.key === "Escape") {
				event.preventDefault();
				setSlashCommandIndex(-1);
				setSlashSuggestionsDismissedDraft(draft);
				return;
			}
		}

		if (event.key === "Enter" && event.shiftKey) {
			event.preventDefault();
			void handleSubmit();
		}
	};

	const handleStop = () => {
		const conversationToStop = activeConversationId;
		if (!conversationToStop) {
			return;
		}

		abortControllerRef.current?.abort();
		setSteering(false);
		setSteerAvailable(false);
		void apiService.stopConversation(conversationToStop).catch((error) => {
			console.error("Failed to stop conversation", error);
		});
		showToast("Stopped the active conversation", "info");
	};

	const appendAttachments = async (files: File[]) => {
		if (files.length === 0) {
			return;
		}

		const remainingSlots = Math.max(
			MAX_IMAGE_ATTACHMENTS - attachments.length,
			0,
		);
		if (remainingSlots === 0) {
			showToast(
				`You can attach up to ${MAX_IMAGE_ATTACHMENTS} images`,
				"error",
			);
			return;
		}

		try {
			const nextAttachments = await Promise.all(
				files.slice(0, remainingSlots).map(fileToPendingAttachment),
			);
			setAttachments((currentAttachments) => [
				...currentAttachments,
				...nextAttachments,
			]);
		} catch (error) {
			const message =
				error instanceof Error ? error.message : "Failed to add image";
			showToast(message, "error");
		}
	};

	const handleRemoveAttachment = (attachmentIdToRemove: string) => {
		setAttachments((currentAttachments) =>
			currentAttachments.filter(
				(attachment) => attachment.id !== attachmentIdToRemove,
			),
		);
	};

	const handlePaste = async (
		event: React.ClipboardEvent<HTMLTextAreaElement>,
	) => {
		const items = Array.from(event.clipboardData?.items || []);
		const imageFiles = items
			.filter((item) => item.kind === "file" && item.type.startsWith("image/"))
			.map((item) => item.getAsFile())
			.filter((file): file is File => file !== null);

		if (imageFiles.length === 0) {
			return;
		}

		event.preventDefault();
		await appendAttachments(imageFiles);
	};

	const handleDragOver = (event: React.DragEvent<HTMLDivElement>) => {
		if (sending && !canSteerActiveConversation) {
			return;
		}

		if (
			Array.from(event.dataTransfer.items || []).some(
				(item) => item.kind === "file",
			)
		) {
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

		if (sending && !canSteerActiveConversation) {
			return;
		}

		const files = Array.from(event.dataTransfer.files || []).filter((file) =>
			file.type.startsWith("image/"),
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
			return conversation?.profile || "default";
		}
		return selectedProfile || "default";
	}, [conversation?.profile, conversationId, selectedProfile]);

	const currentCWDLabel = useMemo(() => {
		const isStartedConversationAwaitingLoad =
			Boolean(conversationId) &&
			loadedConversationId !== conversationId &&
			conversationPathOverrideRef.current === `/c/${conversationId}`;

		if (isStartedConversationAwaitingLoad) {
			return selectedCWD || chatSettings.defaultCWD || "";
		}

		if (conversationId) {
			return conversation?.cwd || chatSettings.defaultCWD || "";
		}
		return selectedCWD || chatSettings.defaultCWD || "";
	}, [
		chatSettings.defaultCWD,
		conversation?.cwd,
		conversationId,
		loadedConversationId,
		selectedCWD,
	]);

	useEffect(() => {
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
					console.error("Failed to load slash commands", error);
				}
			});

		return () => {
			cancelled = true;
		};
	}, [currentCWDLabel]);

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

	const handleCwdInputKeyDown = (
		event: React.KeyboardEvent<HTMLInputElement>,
	) => {
		if (
			cwdSuggestionsOpen &&
			cwdSuggestions.length > 0 &&
			event.key === "ArrowDown"
		) {
			event.preventDefault();
			setCwdSuggestionIndex((current) =>
				current >= cwdSuggestions.length - 1 ? 0 : current + 1,
			);
			return;
		}

		if (
			cwdSuggestionsOpen &&
			cwdSuggestions.length > 0 &&
			event.key === "ArrowUp"
		) {
			event.preventDefault();
			setCwdSuggestionIndex((current) =>
				current <= 0 ? cwdSuggestions.length - 1 : current - 1,
			);
			return;
		}

		if (
			!event.shiftKey &&
			event.key === "Tab" &&
			cwdSuggestionsOpen &&
			cwdSuggestions.length > 0
		) {
			event.preventDefault();
			const suggestion =
				cwdSuggestions[cwdSuggestionIndex >= 0 ? cwdSuggestionIndex : 0];
			if (suggestion) {
				applyCwdSuggestion(suggestion.path);
			}
			return;
		}

		if (
			event.key === "Enter" &&
			cwdSuggestionsOpen &&
			cwdSuggestions.length > 0 &&
			cwdSuggestionIndex >= 0
		) {
			event.preventDefault();
			applyCwdSuggestion(cwdSuggestions[cwdSuggestionIndex].path);
			return;
		}

		if (event.key === "Enter") {
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

		if (event.key === "Escape") {
			if (cwdSuggestionsOpen) {
				setCwdSuggestionsOpen(false);
				setCwdSuggestionIndex(-1);
				return;
			}
		}
	};

	const availableProfiles = useMemo(() => {
		const configuredProfiles = chatSettings.profiles || [];
		if (
			configuredProfiles.some((profile) => profile.name === currentProfileLabel)
		) {
			return configuredProfiles;
		}

		return [
			...configuredProfiles,
			{
				name: currentProfileLabel,
				scope: conversationId ? "conversation" : "selected",
			},
		];
	}, [chatSettings.profiles, conversationId, currentProfileLabel]);

	const composerContextText = useMemo(() => {
		const directoryLabel = currentCWDLabel
			? truncateMiddle(currentCWDLabel, 46)
			: "Default directory";

		return `${currentProfileLabel} · ${directoryLabel}`;
	}, [currentCWDLabel, currentProfileLabel]);

	const recentWorkspaces = useMemo(
		() => getRecentWorkspaces(conversations),
		[conversations],
	);

	const hasActiveConversationTarget = Boolean(activeConversationId);
	const canSteerActiveConversation =
		hasActiveConversationTarget && steerAvailable;
	const isSteeringMode = sending && canSteerActiveConversation;
	const canSubmit = isSteeringMode
		? draft.trim().length > 0
		: draft.trim().length > 0 || attachments.length > 0;
	const canStopActiveConversation = sending && hasActiveConversationTarget;
	const canStartNewChat = !sending || hasActiveConversationTarget;
	const composerPlaceholder = sending
		? !hasActiveConversationTarget
			? "Waiting for conversation to start…"
			: canSteerActiveConversation
				? "Steer the active conversation…"
				: "Add your guidance here..."
		: activeSlashCommand
			? getSlashCommandPlaceholder(activeSlashCommand)
			: "Ask kodelet anything...";
	const composerSlashUsageHint =
		!sending && !steering && activeSlashCommand
			? getSlashCommandPlaceholder(activeSlashCommand)
			: "";
	const submitActionLabel = steering ? "Queueing…" : sending ? "Steer" : "Send";
	const stopActionLabel = canStopActiveConversation ? "Stop" : "Starting…";
	const composerMetaText = useMemo(() => {
		if (!conversation) {
			return "";
		}

		const parts: string[] = [];
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
				`in ${Intl.NumberFormat("en-US", {
					notation: inputTokens >= 1000 ? "compact" : "standard",
					maximumFractionDigits: inputTokens >= 1000 ? 1 : 0,
				}).format(inputTokens)}`,
			);
		}

		if (outputTokens > 0) {
			tokenParts.push(
				`out ${Intl.NumberFormat("en-US", {
					notation: outputTokens >= 1000 ? "compact" : "standard",
					maximumFractionDigits: outputTokens >= 1000 ? 1 : 0,
				}).format(outputTokens)}`,
			);
		}

		if (cacheReadTokens > 0) {
			tokenParts.push(
				`cr ${Intl.NumberFormat("en-US", {
					notation: cacheReadTokens >= 1000 ? "compact" : "standard",
					maximumFractionDigits: cacheReadTokens >= 1000 ? 1 : 0,
				}).format(cacheReadTokens)}`,
			);
		}

		if (cacheWriteTokens > 0) {
			tokenParts.push(
				`cw ${Intl.NumberFormat("en-US", {
					notation: cacheWriteTokens >= 1000 ? "compact" : "standard",
					maximumFractionDigits: cacheWriteTokens >= 1000 ? 1 : 0,
				}).format(cacheWriteTokens)}`,
			);
		}

		if (tokenParts.length > 0) {
			parts.push(tokenParts.join(", "));
		}

		parts.push(formatCost(conversation.usage));

		if (conversation.updatedAt) {
			parts.push(formatCompactRelativeTime(conversation.updatedAt));
		}

		return parts.join(", ");
	}, [conversation, statusTick]);
	const pendingSteerMessages = conversation?.pendingSteer || [];

	const handleCloseNewChatDialog = () => {
		setNewChatProfileDraft(
			selectedProfile || chatSettings.currentProfile || "default",
		);
		cwdSuggestionSkipQueryRef.current = null;
		requestCwdSuggestions.cancel();
		cwdSuggestionRequestRef.current += 1;
		setCwdQuery(selectedCWD || chatSettings.defaultCWD || "");
		setCwdSuggestions([]);
		setCwdSuggestionsOpen(false);
		setCwdSuggestionIndex(-1);
		setNewChatDialogOpen(false);
	};

	const fetchGitDiff = async () => {
		setGitDiffLoading(true);
		setGitDiffError(null);

		try {
			const response = await apiService.getGitDiff(
				currentCWDLabel || undefined,
			);
			setGitDiff(response);
		} catch (error) {
			const message =
				error instanceof Error ? error.message : "Failed to load git diff";
			setGitDiffError(message);
			setGitDiff(null);
		} finally {
			setGitDiffLoading(false);
		}
	};

	const handleOpenGitDiff = () => {
		setGitDiffOpen(true);
		void fetchGitDiff();
	};

	const handleCommitNewChatContext = () => {
		setSelectedProfile(newChatProfileDraft || "default");
		setSelectedCWD(cwdQuery.trim());
		cwdSuggestionSkipQueryRef.current = null;
		requestCwdSuggestions.cancel();
		cwdSuggestionRequestRef.current += 1;
		setCwdSuggestions([]);
		setCwdSuggestionsOpen(false);
		setCwdSuggestionIndex(-1);
		setNewChatDialogOpen(false);
	};

	return (
		<div className="h-[100dvh] bg-transparent">
			<GitDiffModal
				cwdLabel={
					currentCWDLabel || chatSettings.defaultCWD || "Default directory"
				}
				error={gitDiffError}
				gitDiff={gitDiff}
				loading={gitDiffLoading}
				onClose={() => setGitDiffOpen(false)}
				open={gitDiffOpen}
				onRefresh={() => {
					void fetchGitDiff();
				}}
			/>
			<TerminalModal
				cwdLabel={
					currentCWDLabel || chatSettings.defaultCWD || "Default directory"
				}
				open={terminalOpen}
				onClose={() => setTerminalOpen(false)}
			/>

			{newChatDialogOpen ? (
				<NewChatContextDialog
					availableProfiles={availableProfiles}
					cwdInputRef={cwdInputRef}
					cwdQuery={cwdQuery}
					cwdSuggestionIndex={cwdSuggestionIndex}
					cwdSuggestions={cwdSuggestions}
					cwdSuggestionsOpen={cwdSuggestionsOpen}
					defaultCWD={chatSettings.defaultCWD}
					profileDraft={newChatProfileDraft}
					recentWorkspaces={recentWorkspaces}
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
						setCwdSuggestionsOpen(
							cwdQuery.trim().length > 0 && cwdSuggestions.length > 0,
						);
					}}
					onCwdInputKeyDown={handleCwdInputKeyDown}
					onProfileDraftChange={setNewChatProfileDraft}
					onRecentWorkspaceSelect={handleRecentWorkspaceSelect}
					onSelectCwdSuggestion={applyCwdSuggestion}
				/>
			) : null}

			{sidebarVisible ? (
				<button
					aria-label="Hide sidebar overlay"
					className="fixed inset-0 z-30 bg-black/20 lg:hidden"
					onClick={handleSidebarToggle}
					type="button"
				/>
			) : null}

			<div
				className={cn("h-[100dvh] lg:flex", isResizingSidebar && "select-none")}
			>
				{sidebarVisible ? (
					<div
						className="fixed inset-y-0 left-0 z-40 w-[min(85vw,360px)] max-w-full shrink-0 lg:sticky lg:top-0 lg:relative lg:z-20 lg:h-[100dvh] lg:self-start lg:w-[var(--sidebar-width)]"
						data-testid="chat-sidebar-shell"
						style={
							{ "--sidebar-width": `${sidebarWidth}px` } as React.CSSProperties
						}
					>
						<ChatSidebar
							activeConversationId={conversationId}
							conversations={conversations}
							disabled={!canStartNewChat}
							loading={sidebarLoading}
							runningConversationId={sending ? activeConversationId : null}
							onDeleteConversation={handleDeleteConversation}
							onForkConversation={handleForkConversation}
							onHide={handleSidebarToggle}
							onNewChat={handleNewChat}
							onSelectConversation={handleSelectConversation}
						/>

						<div
							aria-label="Resize sidebar"
							aria-orientation="vertical"
							className={cn(
								"sidebar-splitter absolute bottom-0 right-0 top-0 z-10 hidden translate-x-1/2 cursor-col-resize items-center justify-center lg:flex",
								isResizingSidebar && "is-resizing",
							)}
							data-testid="chat-sidebar-resizer"
							onDoubleClick={handleSidebarResizeDoubleClick}
							onMouseDown={handleSidebarResizeStart}
							role="separator"
							tabIndex={-1}
						>
							<span className="sidebar-splitter-rail" />
							<span className="sidebar-splitter-grip" />
						</div>
					</div>
				) : null}

				{!sidebarVisible ? (
					<>
						<div
							className="sidebar-collapsed-rail hidden lg:sticky lg:top-0 lg:flex lg:h-[100dvh] lg:self-start"
							data-testid="sidebar-collapsed-rail"
						>
							<div className="sidebar-collapsed-actions">
								<button
									aria-label="Show panel"
									className="sidebar-toggle-button sidebar-toggle-button-collapsed"
									data-testid="sidebar-attached-toggle"
									onClick={handleSidebarToggle}
									type="button"
								>
									<PanelLeftOpen
										aria-hidden="true"
										className="h-4 w-4"
										strokeWidth={1.9}
									/>
								</button>
							</div>
						</div>

						<button
							aria-label="Show panel"
							className="sidebar-toggle-button sidebar-toggle-button-mobile lg:hidden"
							data-testid="sidebar-attached-toggle-mobile"
							onClick={handleSidebarToggle}
							type="button"
						>
							<PanelLeftOpen
								aria-hidden="true"
								className="h-4 w-4"
								strokeWidth={1.9}
							/>
						</button>
					</>
				) : null}

				<main className="chat-main-panel relative flex h-[100dvh] min-w-0 flex-1 flex-col overflow-hidden">
					<div
						className="chat-main-scroll min-h-0 flex-1 overflow-y-auto"
						data-testid="chat-transcript-scroll"
						onScroll={handleTranscriptScroll}
					>
						{conversationLoading ? (
							<div className="flex min-h-full items-center justify-center px-6 py-12">
								<div className="surface-panel rounded-2xl px-6 py-5 text-sm text-kodelet-dark/70">
									Loading conversation…
								</div>
							</div>
						) : conversationError ? (
							<div className="px-4 py-8 md:px-8">
								<div className="surface-panel max-w-3xl rounded-3xl border-kodelet-orange/20 px-6 py-5 text-kodelet-dark">
									<p className="eyebrow-label text-kodelet-orange">
										Load error
									</p>
									<p className="mt-3 text-sm leading-7">{conversationError}</p>
								</div>
							</div>
						) : (
							<>
								<ChatTranscript
									emptyStateTitle={heading}
									isStreaming={sending}
									messages={messages}
								/>
								{composerMetaText ? (
									<div className="transcript-meta-strip-shell">
										<div className="mx-auto w-full max-w-5xl px-4 md:px-8">
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
							(sending && !canSteerActiveConversation) || steering
						}
						attachments={attachments}
						canStop={canStopActiveConversation}
						contextDisabled={sending || steering}
						contextIsStatic={Boolean(conversationId)}
						contextText={composerContextText}
						dragActive={dragActive}
						draft={draft}
						expanded={composerExpanded}
						placeholder={composerPlaceholder}
						showStop={sending}
						slashCommandIndex={slashCommandIndex}
						slashCommandSuggestions={slashCommandSuggestions}
						slashCommandSuggestionsOpen={slashCommandSuggestionsOpen}
						slashUsageHint={composerSlashUsageHint}
						stopActionLabel={stopActionLabel}
						streamError={streamError}
						submitActionLabel={submitActionLabel}
						submitDisabled={
							steering || !canSubmit || (sending && !canSteerActiveConversation)
						}
						textareaDisabled={steering}
						onAttachImages={appendAttachments}
						onContextOpen={() => {
							setNewChatProfileDraft(currentProfileLabel);
							setCwdQuery(selectedCWD || chatSettings.defaultCWD || "");
							setNewChatDialogOpen(true);
						}}
						onDragLeave={handleDragLeave}
						onDragOver={handleDragOver}
						onDrop={handleDrop}
						onDraftChange={setDraft}
						onDraftKeyDown={handleDraftKeyDown}
						onGitDiffOpen={handleOpenGitDiff}
						onPaste={handlePaste}
						onRemoveAttachment={handleRemoveAttachment}
						onSelectSlashCommand={handleSelectSlashCommand}
						onStop={handleStop}
						onSubmit={handleSubmit}
						onTerminalOpen={() => setTerminalOpen(true)}
						onToggleExpanded={() =>
							setComposerExpanded((currentValue) => !currentValue)
						}
					/>
				</main>
			</div>
		</div>
	);
};

export default ChatPage;
