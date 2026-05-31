import React, { useEffect, useRef, useState } from "react";
import type { UIInputRequestEvent } from "../../types";

interface UIInputDialogProps {
	request: UIInputRequestEvent;
	submitting?: boolean;
	onCancel: () => void;
	onSubmit: (value: string) => void;
}

const UIInputDialog: React.FC<UIInputDialogProps> = ({
	request,
	submitting = false,
	onCancel,
	onSubmit,
}) => {
	const [value, setValue] = useState(request.defaultValue || "");
	const inputRef = useRef<HTMLInputElement | null>(null);

	useEffect(() => {
		setValue(request.defaultValue || "");
		window.setTimeout(() => inputRef.current?.focus(), 0);
	}, [request.id, request.defaultValue]);

	const trimmedTitle = request.title?.trim() || "Extension requested input";
	const canSubmit = !request.required || value.trim().length > 0;

	return (
		<div className="new-chat-dialog-backdrop ui-input-dialog-backdrop">
			<form
				aria-label={trimmedTitle}
				className="new-chat-dialog ui-input-dialog surface-panel"
				data-testid="ui-input-dialog"
				onSubmit={(event) => {
					event.preventDefault();
					if (canSubmit && !submitting) {
						onSubmit(value);
					}
				}}
				role="dialog"
			>
				<div className="new-chat-dialog-header">
					<p className="eyebrow-label text-kodelet-orange">Extension prompt</p>
					<h2 className="new-chat-dialog-title">{trimmedTitle}</h2>
					{request.helpText ? (
						<p className="new-chat-dialog-copy ui-input-help-text">
							{request.helpText}
						</p>
					) : null}
				</div>

				<label className="new-chat-field">
					<span className="composer-profile-label">Response</span>
					<input
						aria-label="Response"
						className="new-chat-field-control"
						data-testid="ui-input-response"
						disabled={submitting}
						onChange={(event) => setValue(event.target.value)}
						placeholder={request.placeholder}
						ref={inputRef}
						required={request.required}
						type={request.secret ? "password" : "text"}
						value={value}
					/>
				</label>

				<div className="new-chat-dialog-actions">
					<button
						className="panel-action-button"
						disabled={submitting}
						onClick={onCancel}
						type="button"
					>
						{request.cancelButtonText || "Cancel"}
					</button>
					<button
						className="primary-pill-button"
						disabled={submitting || !canSubmit}
						type="submit"
					>
						{submitting ? "Sending…" : request.submitButtonText || "Submit"}
					</button>
				</div>
			</form>
		</div>
	);
};

export default UIInputDialog;
