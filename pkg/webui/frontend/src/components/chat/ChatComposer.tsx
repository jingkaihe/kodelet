import React from "react";
import {
	ArrowUp,
	GitCompareArrows,
	ImageUp,
	Maximize2,
	Minimize2,
	Square,
	SquareTerminal,
	X,
} from "lucide-react";
import type { PendingImageAttachment, SlashCommandOption } from "../../types";
import { cn } from "../../utils";

interface ChatComposerProps {
	addImageDisabled: boolean;
	attachments: PendingImageAttachment[];
	canStop: boolean;
	contextDisabled: boolean;
	contextIsStatic: boolean;
	contextText: string;
	dragActive: boolean;
	draft: string;
	expanded: boolean;
	placeholder: string;
	showStop: boolean;
	slashCommandIndex: number;
	slashCommandSuggestions: SlashCommandOption[];
	slashCommandSuggestionsOpen: boolean;
	slashUsageHint: string;
	statusText?: string;
	stopActionLabel: string;
	streamError: string | null;
	submitActionLabel: string;
	submitDisabled: boolean;
	textareaDisabled: boolean;
	onAttachImages: (files: File[]) => void | Promise<void>;
	onContextOpen: () => void;
	onDragLeave: (event: React.DragEvent<HTMLDivElement>) => void;
	onDragOver: (event: React.DragEvent<HTMLDivElement>) => void;
	onDrop: (event: React.DragEvent<HTMLDivElement>) => void;
	onDraftChange: (value: string) => void;
	onDraftKeyDown: (event: React.KeyboardEvent<HTMLTextAreaElement>) => void;
	onGitDiffOpen: () => void;
	onPaste: (event: React.ClipboardEvent<HTMLTextAreaElement>) => void;
	onRemoveAttachment: (attachmentId: string) => void;
	onSelectSlashCommand: (commandName: string) => void;
	onStop: () => void;
	onSubmit: () => void | Promise<void>;
	onTerminalOpen: () => void;
	onToggleExpanded: () => void;
}

const ChatComposer: React.FC<ChatComposerProps> = ({
	addImageDisabled,
	attachments,
	canStop,
	contextDisabled,
	contextIsStatic,
	contextText,
	dragActive,
	draft,
	expanded,
	placeholder,
	showStop,
	slashCommandIndex,
	slashCommandSuggestions,
	slashCommandSuggestionsOpen,
	slashUsageHint,
	statusText = "Shift+Enter to send",
	stopActionLabel,
	streamError,
	submitActionLabel,
	submitDisabled,
	textareaDisabled,
	onAttachImages,
	onContextOpen,
	onDragLeave,
	onDragOver,
	onDrop,
	onDraftChange,
	onDraftKeyDown,
	onGitDiffOpen,
	onPaste,
	onRemoveAttachment,
	onSelectSlashCommand,
	onStop,
	onSubmit,
	onTerminalOpen,
	onToggleExpanded,
}) => {
	const fileInputRef = React.useRef<HTMLInputElement | null>(null);

	const handleFileInputChange = async (
		event: React.ChangeEvent<HTMLInputElement>,
	) => {
		const files = Array.from(event.target.files || []);
		await onAttachImages(files);
		event.target.value = "";
	};

	return (
		<div className="composer-dock sticky bottom-0 z-10 shrink-0 px-4 py-2.5 pb-[calc(0.55rem+env(safe-area-inset-bottom))] md:px-8 md:py-3">
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
					onDragLeave={onDragLeave}
					onDragOver={onDragOver}
					onDrop={onDrop}
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
										onClick={() => onRemoveAttachment(attachment.id)}
										type="button"
									>
										<X aria-hidden="true" className="h-3.5 w-3.5" strokeWidth={2} />
									</button>
								</div>
							))}
						</div>
					) : null}

					{slashCommandSuggestionsOpen ? (
						<div
							className="composer-slash-suggestions"
							data-testid="slash-command-suggestions"
						>
							{slashCommandSuggestions.map((command, index) => (
								<button
									key={command.name}
									className={cn(
										"composer-slash-suggestion",
										slashCommandIndex >= 0 &&
											index === slashCommandIndex &&
											"is-active",
									)}
									onClick={() => onSelectSlashCommand(command.name)}
									onMouseDown={(event) => event.preventDefault()}
									type="button"
								>
									<span className="composer-slash-suggestion-command">
										/{command.name}
									</span>
									<span className="composer-slash-suggestion-description">
										{command.description}
									</span>
									{command.hint ? (
										<span className="composer-slash-suggestion-hint">
											{command.hint}
										</span>
									) : null}
								</button>
							))}
						</div>
					) : null}

					{slashUsageHint ? (
						<div
							className="composer-slash-usage-hint"
							data-testid="composer-slash-usage-hint"
						>
							<span className="composer-slash-usage-label">hint</span>
							<code>{slashUsageHint}</code>
						</div>
					) : null}

					<textarea
						className={cn(
							"composer-editor",
							expanded && "composer-editor-expanded",
						)}
						data-testid="composer-textarea"
						disabled={textareaDisabled}
						onChange={(event) => onDraftChange(event.target.value)}
						onKeyDown={onDraftKeyDown}
						onPaste={onPaste}
						placeholder={placeholder}
						value={draft}
					/>

					<div className="border-t border-black/8 px-2.5 pt-2">
						<div className="composer-footer-row">
							<div className="composer-leading-actions">
								<button
									aria-label="Add image"
									className="composer-icon-button"
									disabled={addImageDisabled}
									onClick={() => fileInputRef.current?.click()}
									type="button"
								>
									<ImageUp
										aria-hidden="true"
										className="h-4 w-4"
										strokeWidth={1.8}
									/>
								</button>

								<button
									aria-label="Show git diff"
									className="composer-icon-button"
									data-testid="composer-git-diff-toggle"
									onClick={onGitDiffOpen}
									title="Show git diff"
									type="button"
								>
									<GitCompareArrows
										aria-hidden="true"
										className="h-4 w-4"
										strokeWidth={1.8}
									/>
								</button>

								<button
									aria-label="Open terminal"
									className="composer-icon-button"
									data-testid="composer-terminal-toggle"
									onClick={onTerminalOpen}
									title="Open terminal"
									type="button"
								>
									<SquareTerminal
										aria-hidden="true"
										className="h-4 w-4"
										strokeWidth={1.8}
									/>
								</button>

								<button
									aria-label={expanded ? "Restore composer" : "Expand composer"}
									aria-pressed={expanded}
									className="composer-icon-button"
									data-testid="composer-expand-toggle"
									onClick={onToggleExpanded}
									type="button"
								>
									{expanded ? (
										<Minimize2
											aria-hidden="true"
											className="h-4 w-4"
											strokeWidth={1.8}
										/>
									) : (
										<Maximize2
											aria-hidden="true"
											className="h-4 w-4"
											strokeWidth={1.8}
										/>
									)}
								</button>
							</div>

							<div className="composer-context-cluster">
								{contextIsStatic ? (
									<div
										className="composer-inline-context is-static"
										data-testid="composer-inline-context"
									>
										<span
											className="composer-inline-context-value"
											title={contextText}
										>
											{contextText}
										</span>
									</div>
								) : (
									<button
										className="composer-inline-context"
										disabled={contextDisabled}
										onClick={onContextOpen}
										type="button"
									>
										<span
											className="composer-inline-context-value"
											title={contextText}
										>
											{contextText}
										</span>
									</button>
								)}

								<p className="composer-status-inline">{statusText}</p>
							</div>

							<div className="composer-status-actions">
								{showStop ? (
									<button
										aria-label={stopActionLabel}
										className="composer-action-icon-button composer-action-icon-button-stop"
										disabled={!canStop}
										onClick={onStop}
										title={stopActionLabel}
										type="button"
									>
										<Square
											aria-hidden="true"
											className="composer-action-stop-icon"
											fill="currentColor"
											strokeWidth={0}
										/>
									</button>
								) : null}

								<button
									className={cn(
										"composer-action-icon-button composer-action-icon-button-submit",
										submitDisabled
											? "composer-action-icon-button-disabled"
											: "composer-action-icon-button-ready",
									)}
									aria-label={submitActionLabel}
									disabled={submitDisabled}
									onClick={() => void onSubmit()}
									title={submitActionLabel}
									type="button"
								>
									<ArrowUp
										aria-hidden="true"
										className="composer-action-submit-icon"
										strokeWidth={3}
									/>
								</button>
							</div>
						</div>
					</div>
				</div>
			</div>
		</div>
	);
};

export default ChatComposer;
