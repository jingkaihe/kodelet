import React from "react";
import { ChevronDown, CornerDownRight } from "lucide-react";
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
			? trimmedWorkspace.replace(/\/+$/, "")
			: trimmedWorkspace;
	const pathParts = normalizedWorkspace.split("/").filter(Boolean);
	const name = pathParts[pathParts.length - 1] || normalizedWorkspace || "/";
	const parent =
		pathParts.length > 1 ? `/${pathParts.slice(0, -1).join("/")}` : "";

	return { name, parent };
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
		<div className="new-chat-dialog-backdrop">
			<div
				aria-label="New chat settings"
				className="new-chat-dialog surface-panel"
				data-testid="new-chat-dialog"
				ref={ref}
				role="dialog"
			>
				<div
					className="new-chat-context-panel"
					data-testid="new-chat-context-panel"
				>
					<div className="new-chat-dialog-grid">
						<label className="new-chat-field">
							<span className="composer-profile-label">Profile</span>
							<div className="new-chat-select-shell">
								<select
									aria-label="Profile"
									className="new-chat-field-control new-chat-field-control-select"
									data-testid="new-chat-profile-select"
									onChange={(event) =>
										onProfileDraftChange(event.target.value)
									}
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

						<label className="new-chat-field">
							<span className="composer-profile-label">Reasoning effort</span>
							<div className="new-chat-select-shell">
								<select
									aria-busy={reasoningEffortLoading}
									aria-label="Reasoning effort"
									className="new-chat-field-control new-chat-field-control-select"
									data-testid="new-chat-reasoning-effort-select"
									disabled={
										reasoningEffortLoading ||
										reasoningEffortOptions.length <= 1
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

						<label className="new-chat-field new-chat-field-wide">
							<span className="composer-profile-label">Directory</span>
							<div className="new-chat-field-autocomplete">
								<input
									aria-autocomplete="list"
									aria-expanded={cwdSuggestionsOpen && cwdSuggestions.length > 0}
									aria-label="Working directory"
									autoCapitalize="off"
									autoComplete="off"
									autoCorrect="off"
									className="new-chat-field-control new-chat-field-control-mono"
									data-testid="cwd-input"
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
								<div
									className="new-chat-recent-workspaces"
									data-testid="recent-workspaces"
								>
									{recentWorkspaces.map((workspace) => {
										const { name, parent } =
											getWorkspaceLabelParts(workspace);

										return (
											<button
												aria-label={workspace}
												className="new-chat-recent-workspace"
												key={workspace}
												onClick={() => onRecentWorkspaceSelect(workspace)}
												title={workspace}
												type="button"
											>
												<span
													className="new-chat-recent-workspace-icon"
													aria-hidden="true"
												>
													<CornerDownRight className="h-3.5 w-3.5" strokeWidth={1.8} />
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
											</button>
										);
									})}
								</div>
							) : null}
						</label>
					</div>
				</div>

				<div className="new-chat-dialog-actions">
					<button className="composer-capsule" onClick={onCancel} type="button">
						Cancel
					</button>
					<button
						className="composer-capsule composer-capsule-accent"
						disabled={reasoningEffortLoading}
						onClick={onCommit}
						type="button"
					>
						Start
					</button>
				</div>
			</div>
		</div>
	),
);

NewChatContextDialog.displayName = "NewChatContextDialog";

export default NewChatContextDialog;
