import React from "react";
import { ArrowRight, Check, ChevronDown, FolderOpen, X } from "lucide-react";
import type { ChatProfileOption, CWDHint } from "../../types";
import { cn } from "../../utils";

interface NewChatContextDialogProps {
	availableProfiles: ChatProfileOption[];
	cwdInputRef?: React.Ref<HTMLInputElement>;
	cwdQuery: string;
	cwdSuggestionIndex: number;
	cwdSuggestions: CWDHint[];
	cwdSuggestionsOpen: boolean;
	defaultCWD?: string;
	profileDraft: string;
	reasoningEffortDraft: string;
	reasoningEffortLoading: boolean;
	reasoningEffortOptions: string[];
	recentWorkspaces: string[];
	onCancel: () => void;
	onCommit: () => void;
	onCwdInputBlur: () => void;
	onCwdInputChange: (value: string) => void;
	onCwdInputFocus: () => void;
	onCwdInputKeyDown: (event: React.KeyboardEvent<HTMLInputElement>) => void;
	onProfileDraftChange: (profileName: string) => void;
	onReasoningEffortDraftChange: (reasoningEffort: string) => void;
	onRecentWorkspaceSelect: (path: string) => void;
	onSelectCwdSuggestion: (path: string) => void;
}

const getWorkspaceLabelParts = (
	workspace: string,
): { name: string; parent: string } => {
	const trimmedWorkspace = workspace.trim();
	const normalizedWorkspace =
		trimmedWorkspace.length > 1
			? trimmedWorkspace.replace(/[\\/]+$/, "")
			: trimmedWorkspace;
	const lastSeparatorIndex = Math.max(
		normalizedWorkspace.lastIndexOf("/"),
		normalizedWorkspace.lastIndexOf("\\"),
	);
	const name =
		lastSeparatorIndex >= 0
			? normalizedWorkspace.slice(lastSeparatorIndex + 1) || normalizedWorkspace
			: normalizedWorkspace || "/";
	const parent =
		lastSeparatorIndex > 0
			? normalizedWorkspace.slice(0, lastSeparatorIndex)
			: lastSeparatorIndex === 0
				? "/"
				: "";

	return { name, parent };
};

const normalizeWorkspacePath = (workspace: string): string => {
	const trimmedWorkspace = workspace.trim();
	return trimmedWorkspace.length > 1
		? trimmedWorkspace.replace(/[\\/]+$/, "")
		: trimmedWorkspace;
};

const NewChatContextDialog = React.forwardRef<
	HTMLDivElement,
	NewChatContextDialogProps
>(
	(
		{
			availableProfiles,
			cwdInputRef,
			cwdQuery,
			cwdSuggestionIndex,
			cwdSuggestions,
			cwdSuggestionsOpen,
			defaultCWD,
			profileDraft,
			reasoningEffortDraft,
			reasoningEffortLoading,
			reasoningEffortOptions,
			recentWorkspaces,
			onCancel,
			onCommit,
			onCwdInputBlur,
			onCwdInputChange,
			onCwdInputFocus,
			onCwdInputKeyDown,
			onProfileDraftChange,
			onReasoningEffortDraftChange,
			onRecentWorkspaceSelect,
			onSelectCwdSuggestion,
		},
		ref,
	) => (
		<div className="new-chat-dialog-backdrop new-chat-context-backdrop">
			<div
				aria-labelledby="new-chat-dialog-title"
				aria-modal="true"
				className="new-chat-dialog new-chat-context-dialog surface-panel"
				data-testid="new-chat-dialog"
				ref={ref}
				role="dialog"
			>
				<header className="new-chat-context-header">
					<h2 className="new-chat-context-title" id="new-chat-dialog-title">
						New chat
					</h2>
					<button
						aria-label="Close new chat dialog"
						className="new-chat-context-close"
						onClick={onCancel}
						type="button"
					>
						<X className="h-4 w-4" strokeWidth={1.8} />
					</button>
				</header>

				<div
					className="new-chat-context-panel"
					data-testid="new-chat-context-panel"
				>
					<div className="new-chat-dialog-grid">
						<label className="new-chat-field new-chat-choice-card">
							<span className="new-chat-field-label">Profile</span>
							<div className="new-chat-select-shell">
								<select
									aria-label="Profile"
									className="new-chat-field-control new-chat-field-control-select"
									data-testid="new-chat-profile-select"
									onChange={(event) => onProfileDraftChange(event.target.value)}
									value={profileDraft}
								>
									{availableProfiles.map((profile) => (
										<option key={profile.name} value={profile.name}>
											{profile.name}
										</option>
									))}
								</select>
								<span className="new-chat-select-chevron" aria-hidden="true">
									<ChevronDown className="h-4 w-4" strokeWidth={1.8} />
								</span>
							</div>
						</label>

						<label className="new-chat-field new-chat-choice-card">
							<span className="new-chat-field-label">Reasoning effort</span>
							<div className="new-chat-select-shell">
								<select
									aria-busy={reasoningEffortLoading}
									aria-label="Reasoning effort"
									className="new-chat-field-control new-chat-field-control-select"
									data-testid="new-chat-reasoning-effort-select"
									disabled={
										reasoningEffortLoading || reasoningEffortOptions.length <= 1
									}
									onChange={(event) =>
										onReasoningEffortDraftChange(event.target.value)
									}
									value={reasoningEffortDraft}
								>
									{reasoningEffortOptions.map((effort) => (
										<option key={effort} value={effort}>
											{effort}
										</option>
									))}
								</select>
								<span className="new-chat-select-chevron" aria-hidden="true">
									<ChevronDown className="h-4 w-4" strokeWidth={1.8} />
								</span>
							</div>
						</label>

						<div className="new-chat-field new-chat-field-wide new-chat-workspace-card">
							<label className="new-chat-field-label" htmlFor="new-chat-cwd">
								Working directory
							</label>
							<div className="new-chat-field-autocomplete">
								<div className="new-chat-directory-shell">
									<FolderOpen
										aria-hidden="true"
										className="new-chat-directory-icon"
										strokeWidth={1.6}
									/>
									<input
										aria-autocomplete="list"
										aria-expanded={
											cwdSuggestionsOpen && cwdSuggestions.length > 0
										}
										aria-label="Working directory"
										autoCapitalize="off"
										autoComplete="off"
										autoCorrect="off"
										className="new-chat-field-control new-chat-field-control-mono new-chat-directory-control"
										data-testid="cwd-input"
										id="new-chat-cwd"
										onBlur={onCwdInputBlur}
										onChange={(event) => onCwdInputChange(event.target.value)}
										onFocus={onCwdInputFocus}
										onKeyDown={onCwdInputKeyDown}
										placeholder={defaultCWD || "/path/to/project"}
										ref={cwdInputRef}
										spellCheck={false}
										type="text"
										value={cwdQuery}
									/>
								</div>

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
												onClick={() => onSelectCwdSuggestion(suggestion.path)}
												onMouseDown={(event) => {
													event.preventDefault();
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
							</div>
							{recentWorkspaces.length > 0 ? (
								<div className="new-chat-recent-section">
									<div className="new-chat-recent-heading">
										<span className="new-chat-recent-title">
											Recent workspaces
										</span>
									</div>
									<div
										className="new-chat-recent-workspaces"
										data-testid="recent-workspaces"
									>
										{recentWorkspaces.map((workspace) => {
											const { name, parent } =
												getWorkspaceLabelParts(workspace);
											const selected =
												normalizeWorkspacePath(workspace) ===
												normalizeWorkspacePath(cwdQuery);

											return (
												<button
													aria-label={workspace}
													aria-pressed={selected}
													className={cn(
														"new-chat-recent-workspace",
														selected && "is-selected",
													)}
													key={workspace}
													onClick={() => onRecentWorkspaceSelect(workspace)}
													title={workspace}
													type="button"
												>
													<span
														className="new-chat-recent-workspace-icon"
														aria-hidden="true"
													>
														<FolderOpen
															className="h-3.5 w-3.5"
															strokeWidth={1.7}
														/>
													</span>
													<span className="new-chat-recent-workspace-text">
														<span className="new-chat-recent-workspace-name">
															{name}
														</span>
														{parent ? (
															<span className="new-chat-recent-workspace-parent">
																{parent}
															</span>
														) : null}
													</span>
													{selected ? (
														<span
															className="new-chat-recent-workspace-check"
															aria-hidden="true"
														>
															<Check className="h-3 w-3" strokeWidth={2.2} />
														</span>
													) : null}
												</button>
											);
										})}
									</div>
								</div>
							) : null}
						</div>
					</div>
				</div>

				<div className="new-chat-dialog-actions new-chat-context-actions">
					<div className="new-chat-context-action-buttons">
						<button
							className="new-chat-secondary-button"
							onClick={onCancel}
							type="button"
						>
							Cancel
						</button>
						<button
							className="new-chat-primary-button"
							disabled={reasoningEffortLoading}
							onClick={onCommit}
							type="button"
						>
							<span>Start</span>
							<ArrowRight
								aria-hidden="true"
								className="h-3.5 w-3.5"
								strokeWidth={1.9}
							/>
						</button>
					</div>
				</div>
			</div>
		</div>
	),
);

NewChatContextDialog.displayName = "NewChatContextDialog";

export default NewChatContextDialog;
