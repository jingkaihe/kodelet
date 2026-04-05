import React, {
	startTransition,
	useEffect,
	useMemo,
	useRef,
	useState,
} from "react";
import { useNavigate, useParams } from "react-router-dom";
import ChatSidebar from "../components/chat/ChatSidebar";
import ChatTranscript from "../components/chat/ChatTranscript";
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
	PendingImageAttachment,
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
	})),
	toolResults: conversation.toolResults || {},
});

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

const upsertConversationSummary = (
	conversations: Conversation[],
	nextConversation: Conversation,
): Conversation[] => {
	const merged = conversations.filter(
		(conversation) => conversation.id !== nextConversation.id,
	);
	merged.unshift(nextConversation);

	merged.sort((left, right) => {
		const getConversationTime = (conversation: Conversation): number => {
			const timestamp =
				conversation.updatedAt ??
				conversation.updated_at ??
				conversation.createdAt ??
				conversation.created_at;

			return timestamp ? new Date(timestamp).getTime() : 0;
		};

		const leftTime = getConversationTime(left);
		const rightTime = getConversationTime(right);
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
	const [sidebarVisible, setSidebarVisible] = useState(
		readStoredSidebarVisible,
	);
	const [sidebarWidth, setSidebarWidth] = useState(readStoredSidebarWidth);
	const [isResizingSidebar, setIsResizingSidebar] = useState(false);
	const [newChatDialogOpen, setNewChatDialogOpen] = useState(false);
	const [statusTick, setStatusTick] = useState(0);
	const transcriptEndRef = useRef<HTMLDivElement | null>(null);
	const abortControllerRef = useRef<AbortController | null>(null);
	const resumeControllerRef = useRef<AbortController | null>(null);
	const sendStreamRef = useRef(0);
	const resumeStreamRef = useRef(0);
	const cwdSuggestionRequestRef = useRef(0);
	const cwdInputFocusedRef = useRef(false);
	const viewedConversationIdRef = useRef<string | null>(conversationId);
	const sidebarResizeStartRef = useRef<{
		startX: number;
		startWidth: number;
	} | null>(null);
	const fileInputRef = useRef<HTMLInputElement | null>(null);
	const cwdInputRef = useRef<HTMLInputElement | null>(null);
	const newChatDialogRef = useRef<HTMLDivElement | null>(null);
	const newChatProfileSelectRef = useRef<HTMLSelectElement | null>(null);

	const refreshConversations = async () => {
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
	};

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
	}, []);

	useEffect(() => {
		return () => {
			sendStreamRef.current += 1;
			resumeStreamRef.current += 1;
			abortControllerRef.current?.abort();
			resumeControllerRef.current?.abort();
		};
	}, []);

	useEffect(() => {
		viewedConversationIdRef.current = conversationId;
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
			newChatProfileSelectRef.current?.focus();
		}, 0);

		const handlePointerDown = (event: MouseEvent) => {
			if (newChatDialogRef.current?.contains(event.target as Node)) {
				return;
			}

			setNewChatProfileDraft(
				selectedProfile || chatSettings.currentProfile || "default",
			);
			setCwdQuery(selectedCWD || chatSettings.defaultCWD || "");
			setNewChatDialogOpen(false);
		};

		const handleKeyDown = (event: KeyboardEvent) => {
			if (event.key === "Escape") {
				setNewChatProfileDraft(
					selectedProfile || chatSettings.currentProfile || "default",
				);
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
			conversation?.id !== conversationId
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

					if (event.kind === "done" || event.kind === "error") {
						setSending(false);
					}

					if (event.kind === "error") {
						setStreamError(event.error || "Chat request failed");
					}

					if (event.kind === "tool-use" || event.kind === "tool-result") {
						setSteerAvailable(true);
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
	}, [conversation, conversationId, conversationLoading]);

	useEffect(() => {
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
		setCwdQuery(chatSettings.defaultCWD || "");
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
		if (conversationId) {
			cwdInputFocusedRef.current = false;
			setCwdSuggestionsOpen(false);
			setCwdSuggestionIndex(-1);
			return;
		}

		if (!cwdQuery.trim()) {
			cwdSuggestionRequestRef.current += 1;
			setCwdSuggestions([]);
			setCwdSuggestionsOpen(false);
			setCwdSuggestionIndex(-1);
			return;
		}

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

	const handleSubmit = async () => {
		const prompt = draft.trim();
		if ((!prompt && attachments.length === 0) || steering) {
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
				await apiService.steerConversation(activeConversationId, prompt);
				setDraft("");
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
		const attachmentsForSend = attachments;
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
						if (
							sendStreamRef.current !== streamInstance ||
							viewedConversationIdRef.current !== viewConversationIdAtStart ||
							(viewConversationIdAtStart !== null &&
								event.conversation_id &&
								event.conversation_id !== viewConversationIdAtStart)
						) {
							return;
						}

						if (event.kind === "conversation" && event.conversation_id) {
							const streamedId = event.conversation_id;
							streamedConversationId = streamedId;
							setActiveConversationId(streamedId);
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

						if (event.kind === "error") {
							streamedError = event.error || "Chat request failed";
							setStreamError(streamedError);
							return;
						}

						if (event.kind === "tool-use" || event.kind === "tool-result") {
							setSteerAvailable(true);
						}

						setMessages((currentMessages) =>
							applyChatStreamEvent(currentMessages, event),
						);
					},
				},
			);

			if (
				sendStreamRef.current !== streamInstance ||
				viewedConversationIdRef.current !== viewConversationIdAtStart
			) {
				return;
			}

			if (streamedError) {
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
				if (streamedConversationId !== conversationId) {
					startTransition(() => navigate(`/c/${streamedConversationId}`));
				}
			}

			await refreshConversations();
		} catch (error) {
			if (error instanceof DOMException && error.name === "AbortError") {
				return;
			}

			if (
				sendStreamRef.current !== streamInstance ||
				viewedConversationIdRef.current !== viewConversationIdAtStart
			) {
				return;
			}

			setAttachments(attachmentsForSend);
			const message =
				error instanceof Error ? error.message : "Failed to send message";
			setStreamError(message);
			showToast(message, "error");
		} finally {
			if (
				abortControllerRef.current === controller &&
				sendStreamRef.current === streamInstance &&
				viewedConversationIdRef.current === viewConversationIdAtStart
			) {
				abortControllerRef.current = null;
				setSending(false);
				setSteerAvailable(false);
			}
		}
	};

	const handleDraftKeyDown = (
		event: React.KeyboardEvent<HTMLTextAreaElement>,
	) => {
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

	const handleFileInputChange = async (
		event: React.ChangeEvent<HTMLInputElement>,
	) => {
		const files = Array.from(event.target.files || []);
		await appendAttachments(files);
		event.target.value = "";
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
		if (sending) {
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

		if (sending) {
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
		if (conversationId) {
			return conversation?.cwd || chatSettings.defaultCWD || "";
		}
		return selectedCWD || chatSettings.defaultCWD || "";
	}, [
		chatSettings.defaultCWD,
		conversation?.cwd,
		conversationId,
		selectedCWD,
	]);

	const applyCwdSuggestion = (path: string) => {
		setCwdQuery(path);
		setCwdSuggestionsOpen(false);
		setCwdSuggestionIndex(-1);
	};

	const handleCwdInputChange = (value: string) => {
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
			setCwdQuery(cwdQuery.trim());
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

	const canSubmit = draft.trim().length > 0 || attachments.length > 0;
	const hasActiveConversationTarget = Boolean(activeConversationId);
	const canStopActiveConversation = sending && hasActiveConversationTarget;
	const canStartNewChat = !sending || hasActiveConversationTarget;
	const canSteerActiveConversation =
		hasActiveConversationTarget && steerAvailable;
	const composerStatus = useMemo(() => {
		if (!conversation) {
			return sending ? "starting…" : "";
		}

		const parts: string[] = [];
		const contextWindow = formatContextWindow(conversation.usage);

		if (contextWindow) {
			parts.push(contextWindow);
		}

		parts.push(formatCost(conversation.usage));

		if (conversation.updatedAt) {
			parts.push(formatCompactRelativeTime(conversation.updatedAt));
		}

		return parts.join(" · ");
	}, [conversation, sending, statusTick]);

	const handleCloseNewChatDialog = () => {
		setNewChatProfileDraft(
			selectedProfile || chatSettings.currentProfile || "default",
		);
		setCwdQuery(selectedCWD || chatSettings.defaultCWD || "");
		setCwdSuggestions([]);
		setCwdSuggestionsOpen(false);
		setCwdSuggestionIndex(-1);
		setNewChatDialogOpen(false);
	};

	const newChatContextEditor = (
		<div className="new-chat-context-panel" data-testid="new-chat-context-panel">
			<div className="new-chat-dialog-grid">
				<label className="new-chat-field">
					<span className="composer-profile-label">Profile</span>
				<select
					aria-label="Profile"
					className="new-chat-field-control"
					data-testid="new-chat-profile-select"
					onChange={(event) => setNewChatProfileDraft(event.target.value)}
					ref={newChatProfileSelectRef}
					value={newChatProfileDraft}
					>
						{availableProfiles.map((profile) => (
							<option key={profile.name} value={profile.name}>
								{profile.name}
							</option>
						))}
					</select>
				</label>

				<label className="new-chat-field new-chat-field-wide">
					<span className="composer-profile-label">Directory</span>
					<input
						aria-autocomplete="list"
						aria-expanded={cwdSuggestionsOpen && cwdSuggestions.length > 0}
						aria-label="Working directory"
						autoCapitalize="off"
						autoComplete="off"
						autoCorrect="off"
						className="new-chat-field-control new-chat-field-control-mono"
						data-testid="cwd-input"
						onBlur={() => {
							cwdInputFocusedRef.current = false;
							window.setTimeout(() => {
								setCwdSuggestionsOpen(false);
								setCwdSuggestionIndex(-1);
							}, 120);
						}}
						onChange={(event) => handleCwdInputChange(event.target.value)}
						onFocus={() => {
							cwdInputFocusedRef.current = true;
							setCwdSuggestionsOpen(
								cwdQuery.trim().length > 0 && cwdSuggestions.length > 0,
							);
						}}
						onKeyDown={handleCwdInputKeyDown}
						placeholder={chatSettings.defaultCWD || "/path/to/project"}
						ref={cwdInputRef}
						spellCheck={false}
						type="text"
						value={cwdQuery}
					/>
					<p className="new-chat-field-hint">
						Type a full path or a nearby project name.
					</p>

					{cwdSuggestionsOpen && cwdSuggestions.length > 0 ? (
						<div
							className="composer-cwd-suggestions composer-cwd-suggestions-inline"
							data-testid="cwd-suggestions"
						>
							{cwdSuggestions.map((suggestion, index) => (
								<button
									className={cn(
										"composer-cwd-suggestion",
										index === cwdSuggestionIndex && "is-active",
									)}
									data-testid={`cwd-suggestion-${index}`}
									key={suggestion.path}
									onMouseDown={(event) => {
										event.preventDefault();
										applyCwdSuggestion(suggestion.path);
									}}
									type="button"
								>
									<span className="composer-cwd-suggestion-path">
										{suggestion.path}
									</span>
								</button>
							))}
						</div>
					) : null}
				</label>
			</div>
		</div>
	);

	const handleCommitNewChatContext = () => {
		setSelectedProfile(newChatProfileDraft || "default");
		setSelectedCWD(cwdQuery.trim());
		setCwdSuggestions([]);
		setCwdSuggestionsOpen(false);
		setCwdSuggestionIndex(-1);
		setNewChatDialogOpen(false);
	};

	return (
		<div className="h-[100dvh] bg-transparent">
			{newChatDialogOpen ? (
				<div className="new-chat-dialog-backdrop">
					<div
						aria-label="New chat settings"
						className="new-chat-dialog surface-panel"
						data-testid="new-chat-dialog"
						ref={newChatDialogRef}
						role="dialog"
					>
						<div className="new-chat-dialog-header">
							<p className="eyebrow-label text-kodelet-mid-gray">New chat</p>
							<h2 className="new-chat-dialog-title">Profile & directory</h2>
						</div>

						{newChatContextEditor}

						<div className="new-chat-dialog-actions">
							<button
								className="composer-capsule"
								onClick={handleCloseNewChatDialog}
								type="button"
							>
								Cancel
							</button>
							<button
								className="composer-capsule composer-capsule-accent"
								onClick={handleCommitNewChatContext}
								type="button"
							>
								Use these settings
							</button>
						</div>
					</div>
				</div>
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
						className="fixed inset-y-0 left-0 z-40 w-[min(85vw,360px)] max-w-full shrink-0 lg:sticky lg:top-0 lg:relative lg:z-auto lg:h-[100dvh] lg:self-start lg:w-[var(--sidebar-width)]"
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
								"sidebar-splitter absolute inset-y-0 right-0 z-10 hidden translate-x-1/2 cursor-col-resize items-center justify-center lg:flex",
								isResizingSidebar && "is-resizing",
							)}
							data-testid="chat-sidebar-resizer"
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
							<button
								aria-label="Show panel"
								className="sidebar-toggle-button sidebar-toggle-button-collapsed"
								data-testid="sidebar-attached-toggle"
								onClick={handleSidebarToggle}
								type="button"
							>
								<svg
									aria-hidden="true"
									className="h-4 w-4"
									fill="none"
									viewBox="0 0 24 24"
									xmlns="http://www.w3.org/2000/svg"
								>
									<path
										d="m9 6 6 6-6 6"
										stroke="currentColor"
										strokeLinecap="round"
										strokeLinejoin="round"
										strokeWidth="1.8"
									/>
								</svg>
							</button>
						</div>

						<button
							aria-label="Show panel"
							className="sidebar-toggle-button sidebar-toggle-button-mobile lg:hidden"
							data-testid="sidebar-attached-toggle-mobile"
							onClick={handleSidebarToggle}
							type="button"
						>
							<svg
								aria-hidden="true"
								className="h-4 w-4"
								fill="none"
								viewBox="0 0 24 24"
								xmlns="http://www.w3.org/2000/svg"
							>
								<path
									d="m9 6 6 6-6 6"
									stroke="currentColor"
									strokeLinecap="round"
									strokeLinejoin="round"
									strokeWidth="1.8"
								/>
							</svg>
						</button>
					</>
				) : null}

				<main className="relative flex h-[100dvh] min-w-0 flex-1 flex-col overflow-hidden">
					<div className="min-h-0 flex-1 overflow-y-auto">
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
								<div ref={transcriptEndRef} />
							</>
						)}
					</div>

					<div className="sticky bottom-0 z-10 shrink-0 border-t border-black/8 bg-[color:var(--kodelet-panel-soft)]/95 px-4 py-2.5 pb-[calc(0.55rem+env(safe-area-inset-bottom))] backdrop-blur-sm md:px-8 md:py-3">
						<div className="mx-auto w-full max-w-5xl px-4 md:px-8">
							{streamError ? (
								<div className="surface-panel mb-3 rounded-2xl border-kodelet-orange/20 px-4 py-3 text-sm text-kodelet-dark">
									{streamError}
								</div>
							) : null}

							<div
								className={cn(
									"surface-panel w-full rounded-[1.45rem] p-2",
									dragActive && "border-kodelet-blue/35 bg-kodelet-blue/5",
								)}
								onDragLeave={handleDragLeave}
								onDragOver={handleDragOver}
								onDrop={handleDrop}
							>
								<input
									accept="image/png,image/jpeg,image/gif,image/webp"
									className="hidden"
									data-testid="composer-image-input"
									multiple
									onChange={handleFileInputChange}
									ref={fileInputRef}
									type="file"
								/>

							{attachments.length > 0 ? (
									<div className="mb-2.5 flex flex-wrap gap-2.5 px-2.5 pt-1.5">
										{attachments.map((attachment) => (
											<div
												key={attachment.id}
												className="relative overflow-hidden rounded-2xl border border-black/8 bg-kodelet-light/80 p-2"
											>
												<img
													alt={attachment.name}
													className="h-20 w-20 rounded-xl object-cover"
													src={attachment.previewUrl}
												/>
												<button
													aria-label={`Remove ${attachment.name}`}
													className="absolute right-2 top-2 inline-flex h-6 w-6 items-center justify-center rounded-full border border-black/8 bg-white/92 text-xs font-heading font-semibold text-kodelet-dark"
													onClick={() => handleRemoveAttachment(attachment.id)}
													type="button"
												>
													×
												</button>
											</div>
										))}
									</div>
							) : null}

							<textarea
								className={cn(
									"composer-editor",
									composerExpanded && "composer-editor-expanded",
								)}
								data-testid="composer-textarea"
								disabled={steering}
								onChange={(event) => setDraft(event.target.value)}
								onKeyDown={handleDraftKeyDown}
									onPaste={handlePaste}
									placeholder={
										sending
											? !hasActiveConversationTarget
												? "Waiting for conversation to start…"
												: canSteerActiveConversation
													? "Steer the active conversation…"
													: "Steering becomes available if the agent starts another turn…"
											: "Ask kodelet anything..."
									}
									value={draft}
								/>

								<div className="border-t border-black/8 px-2.5 pt-2">
									<div className="composer-footer-row">
										<div className="composer-leading-actions">
											<button
												aria-label="Add image"
												className="composer-icon-button"
												disabled={sending || steering}
												onClick={() => fileInputRef.current?.click()}
												type="button"
											>
												<svg
													aria-hidden="true"
													className="h-4 w-4"
													fill="none"
													viewBox="0 0 24 24"
													xmlns="http://www.w3.org/2000/svg"
												>
													<path
														d="M12 16.5v-9"
														stroke="currentColor"
														strokeLinecap="round"
														strokeLinejoin="round"
														strokeWidth="1.7"
													/>
													<path
														d="M7.5 12 12 7.5 16.5 12"
														stroke="currentColor"
														strokeLinecap="round"
														strokeLinejoin="round"
														strokeWidth="1.7"
													/>
													<path
														d="M5.5 18.5h13"
														stroke="currentColor"
														strokeLinecap="round"
														strokeLinejoin="round"
														strokeWidth="1.7"
													/>
												</svg>
											</button>

											<button
												aria-label={composerExpanded ? "Restore composer" : "Expand composer"}
												aria-pressed={composerExpanded}
												className="composer-icon-button"
												data-testid="composer-expand-toggle"
												onClick={() => setComposerExpanded((currentValue) => !currentValue)}
												type="button"
											>
												<svg
													aria-hidden="true"
													className="h-4 w-4"
													fill="none"
													viewBox="0 0 24 24"
													xmlns="http://www.w3.org/2000/svg"
												>
													{composerExpanded ? (
														<>
															<path
																d="M8 3H5a2 2 0 0 0-2 2v3"
																stroke="currentColor"
																strokeLinecap="round"
																strokeLinejoin="round"
																strokeWidth="1.7"
															/>
															<path
																d="M16 3h3a2 2 0 0 1 2 2v3"
																stroke="currentColor"
																strokeLinecap="round"
																strokeLinejoin="round"
																strokeWidth="1.7"
															/>
															<path
																d="M8 21H5a2 2 0 0 1-2-2v-3"
																stroke="currentColor"
																strokeLinecap="round"
																strokeLinejoin="round"
																strokeWidth="1.7"
															/>
															<path
																d="M16 21h3a2 2 0 0 0 2-2v-3"
																stroke="currentColor"
																strokeLinecap="round"
																strokeLinejoin="round"
																strokeWidth="1.7"
															/>
														</>
													) : (
														<>
															<path
																d="M15 3h6v6"
																stroke="currentColor"
																strokeLinecap="round"
																strokeLinejoin="round"
																strokeWidth="1.7"
															/>
															<path
																d="M9 21H3v-6"
																stroke="currentColor"
																strokeLinecap="round"
																strokeLinejoin="round"
																strokeWidth="1.7"
															/>
															<path
																d="M21 3 14 10"
																stroke="currentColor"
																strokeLinecap="round"
																strokeLinejoin="round"
																strokeWidth="1.7"
															/>
															<path
																d="M3 21 10 14"
																stroke="currentColor"
																strokeLinecap="round"
																strokeLinejoin="round"
																strokeWidth="1.7"
															/>
														</>
													)}
												</svg>
											</button>
										</div>

										<div className="composer-context-cluster">
											{conversationId ? (
												<div
													className="composer-inline-context is-static"
													data-testid="composer-inline-context"
												>
													<span
														className="composer-inline-context-value"
														title={composerContextText}
													>
														{composerContextText}
													</span>
												</div>
											) : (
												<button
													className="composer-inline-context"
													disabled={sending || steering}
													onClick={() => {
														setNewChatProfileDraft(currentProfileLabel);
														setCwdQuery(selectedCWD || chatSettings.defaultCWD || "");
														setNewChatDialogOpen(true);
													}}
													type="button"
												>
													<span
														className="composer-inline-context-value"
														title={composerContextText}
													>
														{composerContextText}
													</span>
												</button>
											)}

											{composerStatus ? (
												<p className="composer-status-inline">{composerStatus}</p>
											) : null}
										</div>

										<div className="composer-status-actions">
											{sending ? (
												<button
													className="composer-capsule"
													disabled={!canStopActiveConversation}
													onClick={handleStop}
													type="button"
												>
													{canStopActiveConversation ? "Stop" : "Starting…"}
												</button>
											) : null}

											<button
												className={cn(
													"primary-pill-button",
													steering ||
														!canSubmit ||
														(sending && !canSteerActiveConversation)
														? "cursor-not-allowed bg-kodelet-mid-gray"
														: "bg-kodelet-dark hover:bg-black",
												)}
												disabled={
													steering ||
													!canSubmit ||
													(sending && !canSteerActiveConversation)
												}
												onClick={() => void handleSubmit()}
												type="button"
											>
												{steering ? "Queueing…" : sending ? "Steer" : "Send"}
											</button>
										</div>
									</div>
								</div>
							</div>
						</div>
					</div>
				</main>
			</div>
		</div>
	);
};

export default ChatPage;
