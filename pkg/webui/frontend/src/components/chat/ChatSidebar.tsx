import React from "react";
import type { Conversation } from "../../types";
import { cn, truncateText } from "../../utils";

const DEFAULT_VISIBLE_CONVERSATIONS_PER_GROUP = 10;
const VISIBLE_CONVERSATIONS_STEP = 10;

interface ChatSidebarProps {
	conversations: Conversation[];
	activeConversationId: string | null;
	runningConversationId?: string | null;
	loading: boolean;
	disabled?: boolean;
	onHide?: () => void;
	onNewChat: () => void;
	onSelectConversation: (conversationId: string) => void;
	onForkConversation: (conversationId: string) => void;
	onDeleteConversation: (conversationId: string) => void;
}

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

const ChatSidebar: React.FC<ChatSidebarProps> = ({
	conversations,
	activeConversationId,
	runningConversationId = null,
	loading,
	disabled = false,
	onHide,
	onNewChat,
	onSelectConversation,
	onForkConversation,
	onDeleteConversation,
}) => {
	const [openMenuConversationId, setOpenMenuConversationId] = React.useState<
		string | null
	>(null);
	const [expandedGroups, setExpandedGroups] = React.useState<Record<string, boolean>>(
		{},
	);
	const [visibleGroupCounts, setVisibleGroupCounts] = React.useState<
		Record<string, number>
	>({});
	const menuRef = React.useRef<HTMLDivElement | null>(null);

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

	const groupedConversations = React.useMemo(
		() => groupConversationsByCwd(conversations),
		[conversations],
	);

	React.useEffect(() => {
		setExpandedGroups((currentState) => {
			const nextState: Record<string, boolean> = {};

			groupedConversations.forEach((group, index) => {
				const hasActiveConversation = group.conversations.some(
					(conversation) => conversation.id === activeConversationId,
				);
				nextState[group.key] =
					currentState[group.key] ?? (hasActiveConversation || index === 0);
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

	return (
		<aside className="chat-sidebar-surface relative overflow-visible border-b border-black/8 px-6 py-6 lg:flex lg:h-screen lg:flex-col lg:border-b-0 lg:border-r">
			{onHide ? (
				<button
					aria-label="Hide panel"
					className="sidebar-toggle-button sidebar-toggle-button-open"
					data-testid="sidebar-hide-button"
					disabled={disabled}
					onClick={onHide}
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
							d="M15 6 9 12l6 6"
							stroke="currentColor"
							strokeLinecap="round"
							strokeLinejoin="round"
							strokeWidth="1.8"
						/>
					</svg>
				</button>
			) : null}

			<div className="min-h-0 flex-1 pt-8 lg:pt-2">
				<div className="sidebar-action-link">
					<button
						className="sidebar-action-icon"
						data-testid="sidebar-new-chat-button"
						disabled={disabled}
						onClick={onNewChat}
						type="button"
					>
						<span className="sidebar-action-plus">+</span>
					</button>
					<span className="sidebar-action-label">New chat</span>
				</div>

				<div className="sidebar-section-title">Recent chats</div>

				<div className="conversation-list max-h-[calc(100vh-13.5rem)] overflow-y-auto pr-1">
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

					{groupedConversations.map((group) => (
						<section className="conversation-group" key={group.key}>
							{(() => {
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
								aria-expanded={expandedGroups[group.key] !== false}
								className="conversation-group-header"
								onClick={() =>
									setExpandedGroups((currentState) => ({
										...currentState,
										[group.key]: currentState[group.key] === false,
									}))
								}
								type="button"
							>
								<span className="conversation-group-chevron" aria-hidden="true">
									<svg
										className={cn(
											"h-3.5 w-3.5",
											expandedGroups[group.key] !== false && "rotate-90",
										)}
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

							{expandedGroups[group.key] !== false ? (
								<div className="conversation-group-list">
									{visibleConversations.map((conversation) => {
									const isActive = conversation.id === activeConversationId;
									const isMenuOpen =
										conversation.id === openMenuConversationId;
									const isDeleteDisabled =
										conversation.id === runningConversationId;
									const preview = previewConversation(conversation);

									return (
										<div
											key={conversation.id}
											className={cn(
												"conversation-link-row",
												isActive && "active",
												isMenuOpen && "menu-open",
											)}
											ref={isMenuOpen ? menuRef : undefined}
										>
											<button
												className={cn("conversation-link", isActive && "active")}
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
											</button>

											<div className="conversation-actions">
												<button
													aria-expanded={isMenuOpen}
													aria-haspopup="menu"
													aria-label={`More actions for ${preview}`}
													className="conversation-link-more-button"
													disabled={disabled}
													onClick={() => {
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

		</aside>
	);
};

export default ChatSidebar;
