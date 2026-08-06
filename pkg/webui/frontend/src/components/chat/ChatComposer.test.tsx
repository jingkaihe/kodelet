import type React from "react";
import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";
import ChatComposer from "./ChatComposer";
import { sampleAttachment, sampleSlashCommands } from "../../stories/fixtures";

const renderComposer = (
	overrides: Partial<React.ComponentProps<typeof ChatComposer>> = {},
) => {
	const props: React.ComponentProps<typeof ChatComposer> = {
		addImageDisabled: false,
		attachments: [],
		canStop: false,
		contextDisabled: false,
		contextIsStatic: false,
		contextText: "default · kodelet",
		dragActive: false,
		draft: "hello",
		placeholder: "Ask kodelet anything...",
		showStop: false,
		slashCommandIndex: -1,
		slashCommandSuggestions: [],
		slashCommandSuggestionsOpen: false,
		slashUsageHint: "",
		stopActionLabel: "Stop",
		streamError: null,
		submitActionLabel: "Send",
		submitDisabled: false,
		textareaDisabled: false,
		onAttachImages: vi.fn(),
		onContextOpen: vi.fn(),
		onDragLeave: vi.fn(),
		onDragOver: vi.fn(),
		onDrop: vi.fn(),
		onDraftChange: vi.fn(),
		onDraftKeyDown: vi.fn(),
		onPaste: vi.fn(),
		onRemoveAttachment: vi.fn(),
		onSelectSlashCommand: vi.fn(),
		onStop: vi.fn(),
		onSubmit: vi.fn(),
		...overrides,
	};

	const view = render(<ChatComposer {...props} />);

	return {
		...props,
		rerenderComposer: (
			nextOverrides: Partial<React.ComponentProps<typeof ChatComposer>>,
		) => view.rerender(<ChatComposer {...props} {...nextOverrides} />),
	};
};

describe("ChatComposer", () => {
	it("emits composer actions through props", () => {
		const props = renderComposer();
		const addImageButton = screen.getByLabelText("Add image");

		fireEvent.change(screen.getByTestId("composer-textarea"), {
			target: { value: "next draft" },
		});
		fireEvent.click(screen.getByLabelText("Send"));
		fireEvent.click(screen.getByText("default · kodelet"));

		expect(screen.getByLabelText("Send")).toHaveAttribute(
			"title",
			"Send (Shift+Enter)",
		);
		expect(addImageButton.querySelector("svg")).toHaveClass(
			"lucide-paperclip",
		);
		expect(screen.getByTestId("composer-textarea").parentElement).toHaveClass(
			"composer-control-grid",
		);
		expect(screen.getByTestId("composer-textarea")).toHaveAttribute("rows", "1");
		expect(screen.queryByTestId("composer-expand-toggle")).not.toBeInTheDocument();
		expect(props.onDraftChange).toHaveBeenCalledWith("next draft");
		expect(props.onSubmit).toHaveBeenCalledTimes(1);
		expect(props.onContextOpen).toHaveBeenCalledTimes(1);
	});

	it("uses the automatic multiline layout for drafts with line breaks", () => {
		renderComposer({ draft: "a\nb\nc" });

		expect(screen.getByTestId("composer-textarea").parentElement).toHaveClass(
			"is-multiline",
		);
		expect(screen.queryByTestId("composer-expand-toggle")).not.toBeInTheDocument();
	});

	it("keeps a fitting single-line editor stable during layout syncs", () => {
		renderComposer({ draft: "hello" });

		const textarea = screen.getByTestId("composer-textarea");
		Object.defineProperties(textarea, {
			clientHeight: { configurable: true, value: 52 },
			scrollHeight: { configurable: true, value: 52 },
		});
		const styleObserver = new MutationObserver(() => undefined);
		styleObserver.observe(textarea, {
			attributes: true,
			attributeFilter: ["style"],
		});

		window.dispatchEvent(new Event("resize"));

		expect(styleObserver.takeRecords()).toHaveLength(0);
		styleObserver.disconnect();
	});

	it("resets a stale multiline height when the draft is cleared", () => {
		const composer = renderComposer({ draft: "a\nb\nc" });
		const textarea = screen.getByTestId("composer-textarea");

		textarea.style.height = "91px";
		textarea.style.overflowY = "auto";
		Object.defineProperty(textarea, "scrollHeight", {
			configurable: true,
			value: 91,
		});

		composer.rerenderComposer({ draft: "" });

		expect(textarea.parentElement).not.toHaveClass("is-multiline");
		expect(textarea.style.height).toBe("");
		expect(textarea.style.overflowY).toBe("");
	});

	it("renders and emits the compact stop action", () => {
		const props = renderComposer({ canStop: true, showStop: true });
		const stopButton = screen.getByLabelText("Stop");

		expect(stopButton).toHaveClass("composer-action-icon-button-stop");
		expect(stopButton.querySelector("svg")).toHaveClass(
			"composer-action-stop-icon",
		);

		fireEvent.click(stopButton);
		expect(props.onStop).toHaveBeenCalledTimes(1);
	});

	it("renders attachment previews and slash command suggestions", () => {
		const props = renderComposer({
			attachments: [sampleAttachment],
			slashCommandIndex: 0,
			slashCommandSuggestions: sampleSlashCommands,
			slashCommandSuggestionsOpen: true,
			slashUsageHint: "/review frontend extraction",
		});

		fireEvent.click(screen.getByLabelText(`Remove ${sampleAttachment.name}`));
		fireEvent.click(screen.getByText("/review"));

		expect(screen.getByAltText(sampleAttachment.name)).toBeInTheDocument();
		expect(screen.getByTestId("composer-slash-usage-hint")).toHaveTextContent(
			"/review frontend extraction",
		);
		expect(props.onRemoveAttachment).toHaveBeenCalledWith(sampleAttachment.id);
		expect(props.onSelectSlashCommand).toHaveBeenCalledWith("review");
	});

	it("passes files selected by the hidden image input to the page", async () => {
		const onAttachImages = vi.fn();
		renderComposer({ onAttachImages });

		const file = new File(["image-data"], "capture.png", { type: "image/png" });
		fireEvent.change(screen.getByTestId("composer-image-input"), {
			target: { files: [file] },
		});

		await waitFor(() => {
			expect(onAttachImages).toHaveBeenCalledWith([file]);
		});
	});
});
