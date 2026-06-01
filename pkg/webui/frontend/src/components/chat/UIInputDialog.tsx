import React, { useEffect, useRef, useState } from "react";
import type {
	UIConfirmRequestEvent,
	UIInputRequestEvent,
	UISelectRequestEvent,
} from "../../types";

type UIInputDialogMode = "input" | "confirm" | "select";

const EMPTY_SELECT_OPTIONS: string[] = [];

interface UIInputDialogProps {
	mode?: UIInputDialogMode;
	request: UIInputRequestEvent | UIConfirmRequestEvent | UISelectRequestEvent;
	submitting?: boolean;
	onCancel: () => void;
	onSubmit: (value: string) => void;
}

const UIInputDialog: React.FC<UIInputDialogProps> = ({
	mode = "input",
	request,
	submitting = false,
	onCancel,
	onSubmit,
}) => {
	const inputRequest = request as UIInputRequestEvent;
	const selectRequest = request as UISelectRequestEvent;
	const selectOptions = mode === "select" ? selectRequest.options : EMPTY_SELECT_OPTIONS;
	const [value, setValue] = useState(
		mode === "input" ? inputRequest.defaultValue || "" : "",
	);
	const inputRef = useRef<HTMLInputElement | null>(null);
	const selectRef = useRef<HTMLSelectElement | null>(null);

	useEffect(() => {
		setValue(
			mode === "input"
				? inputRequest.defaultValue || ""
				: mode === "select"
					? selectOptions[0] || ""
					: "",
		);
		window.setTimeout(() => {
			if (mode === "select") {
				selectRef.current?.focus();
				return;
			}
			if (mode === "input") {
				inputRef.current?.focus();
			}
		}, 0);
	}, [mode, request.id, inputRequest.defaultValue, selectOptions]);

	const fallbackTitle =
		mode === "confirm"
			? "Extension requested confirmation"
			: mode === "select"
				? "Extension requested selection"
				: "Extension requested input";
	const trimmedTitle = request.title?.trim() || fallbackTitle;
	const message = "message" in request ? request.message?.trim() : "";
	const canSubmit =
		mode === "select"
			? selectOptions.length > 0
			: mode !== "input" || !inputRequest.required || value.trim().length > 0;
	const submitLabel =
		mode === "confirm"
			? (request as UIConfirmRequestEvent).confirmButtonText || "Confirm"
			: mode === "select"
				? selectRequest.submitButtonText || "Select"
				: inputRequest.submitButtonText || "Submit";

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
					<h2 className="new-chat-dialog-title">{trimmedTitle}</h2>
					{message ? (
						<p className="new-chat-dialog-copy ui-input-help-text">
							{message}
						</p>
					) : null}
					{mode === "input" && inputRequest.helpText ? (
						<p className="new-chat-dialog-copy ui-input-help-text">
							{inputRequest.helpText}
						</p>
					) : null}
				</div>

				{mode === "input" ? (
					<label className="new-chat-field">
						<input
							aria-label="Response"
							className="new-chat-field-control"
							data-testid="ui-input-response"
							disabled={submitting}
							onChange={(event) => setValue(event.target.value)}
							placeholder={inputRequest.placeholder}
							ref={inputRef}
							required={inputRequest.required}
							type={inputRequest.secret ? "password" : "text"}
							value={value}
						/>
					</label>
				) : null}

				{mode === "select" ? (
					<label className="new-chat-field">
						<select
							aria-label="Choice"
							className="new-chat-field-control new-chat-field-control-select"
							data-testid="ui-select-response"
							disabled={submitting}
							onChange={(event) => setValue(event.target.value)}
							ref={selectRef}
							value={value}
						>
							{selectOptions.map((option, index) => (
								<option key={`${index}-${option}`} value={option}>
									{option}
								</option>
							))}
						</select>
					</label>
				) : null}

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
						{submitting ? "Sending…" : submitLabel}
					</button>
				</div>
			</form>
		</div>
	);
};

export default UIInputDialog;
