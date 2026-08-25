import React from "react";
import {
	ChevronDown,
	ChevronRight,
	LogOut,
	PanelLeft,
	Search,
	Settings2,
	SquarePen,
	X,
} from "lucide-react";
import type { AuthPrincipal, Conversation } from "../../types";
import { cn, truncateText } from "../../utils";
import Spinner from "../Spinner";

const DEFAULT_VISIBLE_CONVERSATIONS_PER_GROUP = 10;
const VISIBLE_CONVERSATIONS_STEP = 10;
const SEARCH_DIALOG_FOCUSABLE_SELECTOR = [
	"button:not([disabled])",
	"input:not([disabled])",
	"select:not([disabled])",
	"[href]",
	"[tabindex]:not([tabindex='-1'])",
].join(",");

interface ChatSidebarProps {
	authPrincipal?: AuthPrincipal | null;
	conversations: Conversation[];
	activeConversationId: string | null;
	loading: boolean;
	disabled?: boolean;
	onHide?: () => void;
	onNewChat: () => void;
	onOpenProviderSettings?: () => void;
	onSearch: () => void;
	onSelectConversation: (conversationId: string) => void;
	onForkConversation: (conversationId: string) => void;
	onDeleteConversation: (conversationId: string) => void;
	searchActive?: boolean;
}

interface ChatSidebarCollapsedRailProps {
	disabled?: boolean;
	inert?: boolean;
	searchActive?: boolean;
	onNewChat: () => void;
	onOpen: () => void;
	onSearch: () => void;
}

interface ConversationSearchDialogProps {
	conversations: Conversation[];
	cwdFilter: string;
	cwdOptions: string[];
	error?: string | null;
	hasMore?: boolean;
	loading: boolean;
	loadingMore?: boolean;
	returnFocusSelector?: string;
	searchTerm: string;
	total?: number;
	onClose: () => void;
	onCwdFilterChange: (cwd: string) => void;
	onLoadMore?: () => void;
	onSearchTermChange: (searchTerm: string) => void;
	onSelectConversation: (conversationId: string) => void;
}

export const ChatSidebarCollapsedRail: React.FC<ChatSidebarCollapsedRailProps> = ({
	disabled = false,
	inert = false,
	searchActive = false,
	onNewChat,
	onOpen,
	onSearch,
}) => (
	<div
		className="sidebar-collapsed-rail hidden lg:sticky lg:top-0 lg:flex lg:h-full lg:self-start"
		data-testid="sidebar-collapsed-rail"
		inert={inert || undefined}
	>
		<div className="sidebar-collapsed-actions">
			<button
				aria-label="Show panel"
				className="sidebar-toggle-button sidebar-toggle-button-collapsed"
				data-testid="sidebar-attached-toggle"
				onClick={onOpen}
				type="button"
			>
				<PanelLeft aria-hidden="true" className="h-4 w-4" strokeWidth={1.9} />
			</button>
			<button
				aria-controls="conversation-search-dialog"
				aria-expanded={searchActive}
				aria-haspopup="dialog"
				aria-label="Open conversation search"
				className={cn(
					"sidebar-toggle-button sidebar-collapsed-action",
					searchActive && "is-active",
				)}
				data-testid="sidebar-collapsed-search"
				onClick={onSearch}
				title="Search conversations"
				type="button"
			>
				<Search aria-hidden="true" className="h-4 w-4" strokeWidth={1.9} />
			</button>
			<button
				aria-label="New chat"
				className="sidebar-toggle-button sidebar-collapsed-action"
				data-testid="sidebar-collapsed-new-chat"
				disabled={disabled}
				onClick={onNewChat}
				title="New chat"
				type="button"
			>
				<SquarePen aria-hidden="true" className="h-4 w-4" strokeWidth={1.75} />
			</button>
		</div>
	</div>
);

const getAccountPresentation = (principal: AuthPrincipal) => {
	const emailName = principal.email?.split("@", 1)[0]?.replace(/[._-]+/g, " ");
	const fullName = principal.name?.trim() || emailName?.trim() || "Account";
	const nameParts = fullName.split(/\s+/).filter(Boolean);
	const firstName = nameParts[0] || "Account";
	const lastName = nameParts.length > 1 ? nameParts[nameParts.length - 1] : "";

	return {
		fullName,
		initials: `${firstName[0] || "A"}${lastName[0] || ""}`.toUpperCase(),
		shortName: lastName ? `${firstName[0]?.toUpperCase()} ${lastName}` : firstName,
	};
};

const isOIDCPrincipal = (
	principal: AuthPrincipal | null | undefined,
): principal is AuthPrincipal =>
	Boolean(principal?.issuer?.trim() && principal.subject?.trim());

const previewConversation = (conversation: Conversation): string => {
	return (
		conversation.summary ||
		conversation.preview ||
		conversation.firstMessage ||
		"Untitled conversation"
	);
};

const getConversationTime = (conversation: Conversation): number => {
	const timestamp =
		conversation.updatedAt ??
		conversation.updated_at ??
		conversation.createdAt ??
		conversation.created_at;

	return timestamp ? new Date(timestamp).getTime() : 0;
};

const formatCwdGroupLabel = (cwd?: string): string => {
	const normalized = cwd?.trim();
	if (!normalized) {
		return "No directory";
	}

	return normalized;
};

const getCwdGroupPrimaryLabel = (cwd?: string): string => {
	const normalized = cwd?.trim();
	if (!normalized) {
		return "No directory";
	}

	const parts = normalized.split(/[\\/]+/).filter(Boolean);
	return parts[parts.length - 1] || normalized;
};

export const ConversationSearchDialog: React.FC<ConversationSearchDialogProps> = ({
	conversations,
	cwdFilter,
	cwdOptions,
	error = null,
	hasMore = false,
	loading,
	loadingMore = false,
	returnFocusSelector,
	searchTerm,
	total,
	onClose,
	onCwdFilterChange,
	onLoadMore,
	onSearchTermChange,
	onSelectConversation,
}) => {
	const dialogRef = React.useRef<HTMLDivElement | null>(null);
	const inputRef = React.useRef<HTMLInputElement | null>(null);
	const onCloseRef = React.useRef(onClose);
	const returnFocusSelectorRef = React.useRef(returnFocusSelector);
	onCloseRef.current = onClose;
	returnFocusSelectorRef.current = returnFocusSelector;

	React.useEffect(() => {
		const previousFocus =
			document.activeElement instanceof HTMLElement &&
			document.activeElement !== document.body
				? document.activeElement
				: null;
		const focusInput = window.setTimeout(() => inputRef.current?.focus(), 0);
		const handleKeyDown = (event: KeyboardEvent) => {
			const dialog = dialogRef.current;
			if (!dialog) {
				return;
			}

			if (event.key === "Escape") {
				event.preventDefault();
				event.stopPropagation();
				onCloseRef.current();
				return;
			}

			if (event.key !== "Tab") {
				return;
			}

			const focusableElements = Array.from(
				dialog.querySelectorAll<HTMLElement>(SEARCH_DIALOG_FOCUSABLE_SELECTOR),
			).filter((element) => !element.hasAttribute("disabled"));
			if (focusableElements.length === 0) {
				event.preventDefault();
				dialog.focus();
				return;
			}

			const firstElement = focusableElements[0];
			const lastElement = focusableElements[focusableElements.length - 1];
			const activeElement = document.activeElement;
			if (!activeElement || !dialog.contains(activeElement)) {
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

		window.addEventListener("keydown", handleKeyDown, true);
		return () => {
			window.clearTimeout(focusInput);
			window.removeEventListener("keydown", handleKeyDown, true);
			window.setTimeout(() => {
				if (document.querySelector("[aria-modal='true']")) {
					return;
				}
				const returnFocusSelector = returnFocusSelectorRef.current;
				if (returnFocusSelector) {
					const returnFocus = document.querySelector<HTMLElement>(returnFocusSelector);
					if (returnFocus) {
						returnFocus.focus();
						return;
					}
				}
				if (previousFocus?.isConnected) {
					previousFocus.focus();
					return;
				}
				document
					.querySelector<HTMLElement>(
						"[data-testid='sidebar-search-toggle'], [data-testid='sidebar-collapsed-search'], [data-testid='sidebar-attached-toggle-mobile']",
					)
					?.focus();
			}, 0);
		};
	}, []);

	const trimmedSearchTerm = searchTerm.trim();
	const trimmedCwdFilter = cwdFilter.trim();
	const hasActiveFilters = Boolean(trimmedSearchTerm || trimmedCwdFilter);
	const resultsLabel = hasActiveFilters ? "Search results" : "Recent conversations";
	const resultTotal = Math.max(total ?? conversations.length, conversations.length);
	const initialLoading = loading && conversations.length === 0;
	const resultCountLabel =
		initialLoading
			? "…"
			: error && conversations.length === 0
				? "—"
				: resultTotal > conversations.length
			? `${conversations.length} of ${resultTotal}`
			: String(resultTotal);
	const emptyStateText = trimmedSearchTerm
		? "No conversations match your search."
		: trimmedCwdFilter
			? "No conversations in this workspace."
			: "No saved conversations yet.";

	return (
		<div
			className="new-chat-dialog-backdrop conversation-search-backdrop"
			onMouseDown={(event) => {
				if (event.target === event.currentTarget) {
					onClose();
				}
			}}
		>
			<div
				aria-labelledby="conversation-search-title"
				aria-modal="true"
				className="conversation-search-dialog surface-panel"
				data-testid="conversation-search-dialog"
				id="conversation-search-dialog"
				ref={dialogRef}
				role="dialog"
				tabIndex={-1}
			>
				<header className="conversation-search-header">
					<div>
						<h2 className="conversation-search-title" id="conversation-search-title">
							Search conversations
						</h2>
						<p className="conversation-search-copy">
							Find a conversation by title, first message, ID, or workspace.
						</p>
					</div>
					<button
						aria-label="Close conversation search"
						className="new-chat-context-close"
						onClick={onClose}
						type="button"
					>
						<X aria-hidden="true" className="h-4 w-4" strokeWidth={1.8} />
					</button>
				</header>

				<div className="conversation-search-body">
					<div className="conversation-search-controls">
						<div className="conversation-search-input-shell">
							<Search
								aria-hidden="true"
								className="conversation-search-input-icon"
								strokeWidth={1.8}
							/>
							<input
								aria-label="Search conversations"
								autoCapitalize="off"
								autoComplete="off"
								autoCorrect="off"
								className="conversation-search-input"
								onChange={(event) => onSearchTermChange(event.target.value)}
								placeholder="Search conversations"
								ref={inputRef}
								spellCheck={false}
								type="search"
								value={searchTerm}
							/>
							{searchTerm ? (
								<button
									aria-label="Clear conversation search"
									className="conversation-search-clear"
									onClick={() => {
										onSearchTermChange("");
										inputRef.current?.focus();
									}}
									type="button"
								>
									<X aria-hidden="true" className="h-3.5 w-3.5" strokeWidth={2} />
								</button>
							) : null}
						</div>

						<div className="conversation-search-workspace-shell">
							<select
								aria-label="Search workspace"
								className="conversation-search-workspace"
								onChange={(event) => onCwdFilterChange(event.target.value)}
								value={cwdFilter}
							>
								<option value="">All workspaces</option>
								{cwdOptions.map((cwd) => (
									<option key={cwd} value={cwd}>
										{cwd}
									</option>
								))}
							</select>
							<ChevronDown
								aria-hidden="true"
								className="conversation-search-workspace-chevron"
								strokeWidth={1.8}
							/>
						</div>
					</div>

					<div aria-busy={loading || loadingMore} className="conversation-search-results">
						<div aria-live="polite" className="conversation-search-results-header">
							<span>{resultsLabel}</span>
							<span>{resultCountLabel}</span>
						</div>

						{initialLoading ? (
							<div className="conversation-search-empty">Searching…</div>
						) : null}

						{!loading && error ? (
							<div className="conversation-search-empty" role="alert">
								{error}
							</div>
						) : null}

						{!loading && !error && conversations.length === 0 ? (
							<div className="conversation-search-empty">{emptyStateText}</div>
						) : null}

						{!loading && conversations.map((conversation) => {
							const preview = previewConversation(conversation);
							return (
								<button
									className="conversation-search-result"
									key={conversation.id}
									onClick={() => onSelectConversation(conversation.id)}
									type="button"
								>
									<span className="conversation-search-result-copy">
										<span className="conversation-search-result-title">
											{truncateText(preview, 100)}
										</span>
										<span className="conversation-search-result-path">
											{formatCwdGroupLabel(conversation.cwd)} · {conversation.id}
										</span>
									</span>
									<ChevronRight
										aria-hidden="true"
										className="conversation-search-result-chevron"
										strokeWidth={1.8}
									/>
								</button>
							);
						})}

						{!loading && conversations.length > 0 && hasMore && onLoadMore ? (
							<button
								className="conversation-search-load-more"
								disabled={loadingMore}
								onClick={onLoadMore}
								type="button"
							>
								{loadingMore ? "Loading more…" : "Load more"}
							</button>
						) : null}
					</div>
				</div>
			</div>
		</div>
	);
};

const groupConversationsByCwd = (conversations: Conversation[]) => {
	const groups = new Map<
		string,
		{
			key: string;
			cwd?: string;
			label: string;
			primaryLabel: string;
			secondaryLabel?: string;
			conversations: Conversation[];
		}
	>();

	conversations.forEach((conversation) => {
		const normalizedCwd = conversation.cwd?.trim();
		const key = normalizedCwd || "__no_cwd__";

		if (!groups.has(key)) {
			const label = formatCwdGroupLabel(normalizedCwd);
			groups.set(key, {
				key,
				cwd: normalizedCwd,
				label,
				primaryLabel: getCwdGroupPrimaryLabel(normalizedCwd),
				secondaryLabel: normalizedCwd ? label : undefined,
				conversations: [],
			});
		}

		groups.get(key)?.conversations.push(conversation);
	});

	return Array.from(groups.values()).sort((left, right) => {
		const leftTime = getConversationTime(left.conversations[0]);
		const rightTime = getConversationTime(right.conversations[0]);
		return rightTime - leftTime;
	});
};

type ConversationGroup = ReturnType<typeof groupConversationsByCwd>[number];

const isGroupExpandedByDefault = (
	group: ConversationGroup,
	index: number,
	activeConversationId: string | null,
): boolean =>
	index === 0 ||
	group.conversations.some(
		(conversation) => conversation.id === activeConversationId,
	);

const ChatSidebar: React.FC<ChatSidebarProps> = ({
	authPrincipal,
	conversations,
	activeConversationId,
	loading,
	disabled = false,
	onHide,
	onNewChat,
	onOpenProviderSettings,
	onSearch,
	onSelectConversation,
	onForkConversation,
	onDeleteConversation,
	searchActive = false,
}) => {
	const [openMenuConversationId, setOpenMenuConversationId] = React.useState<
		string | null
	>(null);
	const [accountMenuOpen, setAccountMenuOpen] = React.useState(false);
	const [expandedGroups, setExpandedGroups] = React.useState<Record<string, boolean>>(
		{},
	);
	const [visibleGroupCounts, setVisibleGroupCounts] = React.useState<
		Record<string, number>
	>({});
	const menuRef = React.useRef<HTMLDivElement | null>(null);
	const accountMenuRef = React.useRef<HTMLDivElement | null>(null);

	React.useEffect(() => {
		if (!openMenuConversationId) {
			return undefined;
		}

		const handlePointerDown = (event: MouseEvent) => {
			if (!menuRef.current?.contains(event.target as Node)) {
				setOpenMenuConversationId(null);
			}
		};

		const handleEscape = (event: KeyboardEvent) => {
			if (event.key === "Escape") {
				setOpenMenuConversationId(null);
			}
		};

		document.addEventListener("mousedown", handlePointerDown);
		document.addEventListener("keydown", handleEscape);

		return () => {
			document.removeEventListener("mousedown", handlePointerDown);
			document.removeEventListener("keydown", handleEscape);
		};
	}, [openMenuConversationId]);

	React.useEffect(() => {
		if (!accountMenuOpen) {
			return undefined;
		}

		const handlePointerDown = (event: MouseEvent) => {
			if (!accountMenuRef.current?.contains(event.target as Node)) {
				setAccountMenuOpen(false);
			}
		};

		const handleEscape = (event: KeyboardEvent) => {
			if (event.key === "Escape") {
				setAccountMenuOpen(false);
			}
		};

		document.addEventListener("mousedown", handlePointerDown);
		document.addEventListener("keydown", handleEscape);

		return () => {
			document.removeEventListener("mousedown", handlePointerDown);
			document.removeEventListener("keydown", handleEscape);
		};
	}, [accountMenuOpen]);

	const groupedConversations = React.useMemo(
		() => groupConversationsByCwd(conversations),
		[conversations],
	);

	React.useEffect(() => {
		setExpandedGroups((currentState) => {
			const nextState: Record<string, boolean> = {};

			groupedConversations.forEach((group, index) => {
				const containsActiveConversation = group.conversations.some(
					(conversation) => conversation.id === activeConversationId,
				);
				nextState[group.key] = containsActiveConversation
					? true
					: currentState[group.key] ??
						isGroupExpandedByDefault(group, index, activeConversationId);
			});

			return nextState;
		});
	}, [activeConversationId, groupedConversations]);

	React.useEffect(() => {
		setVisibleGroupCounts((currentState) => {
			const nextState: Record<string, number> = {};

			groupedConversations.forEach((group) => {
				const activeIndex = group.conversations.findIndex(
					(conversation) => conversation.id === activeConversationId,
				);
				const minimumVisibleCount =
					activeIndex >= 0
						? Math.max(
								DEFAULT_VISIBLE_CONVERSATIONS_PER_GROUP,
								activeIndex + 1,
							)
						: DEFAULT_VISIBLE_CONVERSATIONS_PER_GROUP;

				nextState[group.key] = Math.min(
					group.conversations.length,
					Math.max(currentState[group.key] ?? minimumVisibleCount, minimumVisibleCount),
				);
			});

			return nextState;
		});
	}, [activeConversationId, groupedConversations]);
	const showLoadingState = loading && conversations.length === 0;
	const account = isOIDCPrincipal(authPrincipal)
		? getAccountPresentation(authPrincipal)
		: null;
	const canManageProviders = Boolean(
		account && onOpenProviderSettings && authPrincipal?.roles.includes("admin"),
	);

	return (
		<aside className="chat-sidebar-surface relative flex h-full flex-col overflow-visible border-b border-black/8 px-6 py-6 lg:border-b-0">
			<div className="sidebar-header">
				<div className="sidebar-brand" aria-label="Kodelet conversations">
					Kodelet
				</div>

				<div className="sidebar-header-actions">
					<button
						aria-controls="conversation-search-dialog"
						aria-expanded={searchActive}
						aria-haspopup="dialog"
						aria-label="Open conversation search"
						className="sidebar-toggle-button sidebar-search-toggle"
						data-testid="sidebar-search-toggle"
						onClick={onSearch}
						title="Search conversations"
						type="button"
					>
						<Search aria-hidden="true" className="h-4 w-4" strokeWidth={1.9} />
					</button>

					{onHide ? (
						<button
							aria-label="Hide panel"
							className="sidebar-toggle-button sidebar-toggle-button-open"
							data-testid="sidebar-hide-button"
							onClick={onHide}
							type="button"
						>
							<PanelLeft aria-hidden="true" className="h-4 w-4" strokeWidth={1.9} />
						</button>
					) : null}
				</div>
			</div>

			<div className="flex min-h-0 flex-1 flex-col">
				<button
					className="sidebar-action-link"
					data-testid="sidebar-new-chat-button"
					disabled={disabled}
					onClick={onNewChat}
					type="button"
				>
					<SquarePen aria-hidden="true" className="sidebar-action-icon" strokeWidth={1.9} />
					<span className="sidebar-action-label">New Chat</span>
				</button>

				<div className="sidebar-section-title">Recents</div>

				<div
					aria-busy={loading}
					className="conversation-list min-h-0 flex-1 overflow-y-auto pr-1"
				>
					{conversations.length === 0 && !showLoadingState ? (
						<div className="px-2 py-2 text-sm text-kodelet-dark/65">
							No saved conversations yet.
						</div>
					) : null}

					{showLoadingState ? (
						<div className="px-2 py-2 text-sm text-kodelet-dark/65">
							Loading…
						</div>
					) : null}

					{groupedConversations.map((group, groupIndex) => (
						<section className="conversation-group" key={group.key}>
							{(() => {
								const isExpanded =
									expandedGroups[group.key] ??
									isGroupExpandedByDefault(
										group,
										groupIndex,
										activeConversationId,
									);
								const activeIndex = group.conversations.findIndex(
									(conversation) => conversation.id === activeConversationId,
								);
								const minimumVisibleCount =
									activeIndex >= 0
										? Math.max(
												DEFAULT_VISIBLE_CONVERSATIONS_PER_GROUP,
												activeIndex + 1,
											)
										: DEFAULT_VISIBLE_CONVERSATIONS_PER_GROUP;
							const visibleCount = Math.min(
								visibleGroupCounts[group.key] ?? minimumVisibleCount,
								group.conversations.length,
							);
							const remainingCount = group.conversations.length - visibleCount;
							const canShowLess = visibleCount > minimumVisibleCount;
							const canShowMore = remainingCount > 0;
							const visibleConversations = group.conversations.slice(0, visibleCount);

								return (
									<>
							<button
								aria-expanded={isExpanded}
								className="conversation-group-header"
								onClick={() =>
									setExpandedGroups((currentState) => ({
										...currentState,
										[group.key]: !isExpanded,
									}))
								}
								type="button"
							>
								<span className="conversation-group-chevron" aria-hidden="true">
									<ChevronRight
										className={cn(
											"h-3.5 w-3.5",
											isExpanded && "rotate-90",
										)}
										strokeWidth={1.9}
									/>
								</span>
								<span
									className="conversation-group-labels"
									title={group.cwd || group.label}
								>
									<span className="conversation-group-title">
										{group.primaryLabel}
									</span>
									{group.secondaryLabel ? (
										<span className="conversation-group-path">
											{group.secondaryLabel}
										</span>
									) : null}
								</span>
								<span className="conversation-group-count">
									{group.conversations.length}
								</span>
							</button>

							{isExpanded ? (
								<div className="conversation-group-list">
									{visibleConversations.map((conversation) => {
										const isActive = conversation.id === activeConversationId;
										const isRunning = Boolean(conversation.isRunning);
										const isMenuOpen =
											conversation.id === openMenuConversationId;
										const isDeleteDisabled = isRunning;
										const preview = previewConversation(conversation);

										return (
											<div
												data-testid={`conversation-row-${conversation.id}`}
												key={conversation.id}
												className={cn(
													"conversation-link-row",
													isActive && "active",
													isRunning && "running",
													isMenuOpen && "menu-open",
												)}
												ref={isMenuOpen ? menuRef : undefined}
										>
											<button
												className={cn(
													"conversation-link",
													isActive && "active",
												)}
													disabled={disabled}
													onClick={() => {
														setOpenMenuConversationId(null);
														onSelectConversation(conversation.id);
													}}
													type="button"
												>
													<span className="conversation-link-title">
														{truncateText(preview, 80)}
													</span>
													{isRunning ? (
														<span
															className="conversation-running-indicator"
															data-testid={`conversation-running-indicator-${conversation.id}`}
															title="Conversation is running"
														>
															<Spinner className="conversation-running-spinner" />
															<span className="sr-only">Running</span>
														</span>
													) : null}
												</button>

												<div className="conversation-actions">
													<button
														aria-expanded={isMenuOpen}
														aria-haspopup="menu"
														aria-label={`More actions for ${preview}`}
														className="conversation-link-more-button"
														disabled={disabled}
														onClick={() => {
															setAccountMenuOpen(false);
															setOpenMenuConversationId((currentId) =>
																currentId === conversation.id ? null : conversation.id,
															);
														}}
														type="button"
													>
														<span className="conversation-link-more">•••</span>
													</button>

												{isMenuOpen ? (
													<div className="conversation-action-menu" role="menu">
														<button
															className="conversation-action-menu-item"
															onClick={() => {
																setOpenMenuConversationId(null);
																onForkConversation(conversation.id);
															}}
															role="menuitem"
															type="button"
														>
															Copy
														</button>
														<button
															className="conversation-action-menu-item danger"
															disabled={isDeleteDisabled}
															onClick={() => {
																setOpenMenuConversationId(null);
																onDeleteConversation(conversation.id);
															}}
															role="menuitem"
															type="button"
														>
															Delete
														</button>
													</div>
												) : null}
											</div>
										</div>
									);
									})}

									{canShowLess || canShowMore ? (
										<div className="conversation-group-controls">
											{canShowLess ? (
												<button
													className="conversation-group-more"
													onClick={() =>
														setVisibleGroupCounts((currentState) => ({
															...currentState,
															[group.key]: minimumVisibleCount,
														}))
													}
													type="button"
												>
													Show less
												</button>
											) : null}

											{canShowMore ? (
												<button
													className="conversation-group-more"
													onClick={() =>
														setVisibleGroupCounts((currentState) => ({
															...currentState,
															[group.key]: Math.min(
																group.conversations.length,
																visibleCount + VISIBLE_CONVERSATIONS_STEP,
															),
														}))
													}
													type="button"
												>
													Show {Math.min(remainingCount, VISIBLE_CONVERSATIONS_STEP)} more
												</button>
											) : null}
										</div>
									) : null}
								</div>
							) : null}
									</>
								);
							})()}
						</section>
					))}
					</div>
				</div>

			{account ? (
				<div className="sidebar-account" ref={accountMenuRef}>
					{accountMenuOpen ? (
						<div
							aria-label="Account options"
							className="sidebar-account-menu"
							data-testid="sidebar-account-menu"
							role="menu"
						>
							{canManageProviders ? (
								<button
									className="sidebar-account-menu-item"
									data-testid="sidebar-provider-settings"
									onClick={() => {
										setAccountMenuOpen(false);
										onOpenProviderSettings?.();
									}}
									role="menuitem"
									type="button"
								>
									<Settings2 aria-hidden="true" className="h-4 w-4" strokeWidth={1.8} />
									<span>Provider settings</span>
								</button>
							) : null}
							<a
								className="sidebar-account-menu-item"
								href="/auth/logout"
								onClick={() => setAccountMenuOpen(false)}
								role="menuitem"
							>
								<LogOut aria-hidden="true" className="h-4 w-4" strokeWidth={1.9} />
								<span>Sign out</span>
							</a>
						</div>
					) : null}

					<button
						aria-expanded={accountMenuOpen}
						aria-haspopup="menu"
						aria-label={`${account.fullName} account menu`}
						className="sidebar-account-trigger"
						onClick={() => {
							setOpenMenuConversationId(null);
							setAccountMenuOpen((open) => !open);
						}}
						title={account.fullName}
						type="button"
					>
						<span aria-hidden="true" className="sidebar-account-avatar">
							{account.initials}
						</span>
						<span className="sidebar-account-name">{account.shortName}</span>
					</button>
				</div>
			) : null}
		</aside>
	);
};

export default ChatSidebar;
