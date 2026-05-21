import React from "react";
import type { Meta, StoryObj } from "@storybook/react-vite";
import { fn } from "storybook/test";
import ChatComposer from "./ChatComposer";
import {
	sampleAttachment,
	sampleSlashCommands,
} from "../../stories/fixtures";

type ChatComposerStoryProps = React.ComponentProps<typeof ChatComposer>;

const InteractiveComposer = (args: ChatComposerStoryProps) => {
	const [draft, setDraft] = React.useState(args.draft);
	const [expanded, setExpanded] = React.useState(args.expanded);
	const [attachments, setAttachments] = React.useState(args.attachments);

	return (
		<ChatComposer
			{...args}
			attachments={attachments}
			draft={draft}
			expanded={expanded}
			onAttachImages={(files) => {
				void args.onAttachImages(files);
			}}
			onDraftChange={setDraft}
			onRemoveAttachment={(attachmentId) => {
				setAttachments((currentAttachments) =>
					currentAttachments.filter((attachment) => attachment.id !== attachmentId),
				);
				args.onRemoveAttachment(attachmentId);
			}}
			onToggleExpanded={() => {
				setExpanded((currentValue) => !currentValue);
				args.onToggleExpanded();
			}}
		/>
	);
};

const meta = {
	title: "Chat/ChatComposer",
	component: ChatComposer,
	render: (args) => <InteractiveComposer {...args} />,
	parameters: {
		layout: "fullscreen",
	},
	args: {
		addImageDisabled: false,
		attachments: [],
		canStop: false,
		contextDisabled: false,
		contextIsStatic: false,
		contextText: "default · kodelet",
		dragActive: false,
		draft: "Extract the reusable component and add a story.",
		expanded: false,
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
		onAttachImages: fn(),
		onContextOpen: fn(),
		onDragLeave: fn(),
		onDragOver: fn(),
		onDrop: fn(),
		onDraftChange: fn(),
		onDraftKeyDown: fn(),
		onGitDiffOpen: fn(),
		onPaste: fn(),
		onRemoveAttachment: fn(),
		onSelectSlashCommand: fn(),
		onStop: fn(),
		onSubmit: fn(),
		onTerminalOpen: fn(),
		onToggleExpanded: fn(),
	},
} satisfies Meta<typeof ChatComposer>;

export default meta;

type Story = StoryObj<typeof meta>;

export const ReadyToSend: Story = {};

export const WithSlashSuggestions: Story = {
	args: {
		draft: "/re",
		slashCommandIndex: 0,
		slashCommandSuggestions: sampleSlashCommands,
		slashCommandSuggestionsOpen: true,
		slashUsageHint: "/review frontend extraction",
	},
};

export const SteeringActiveConversation: Story = {
	args: {
		contextIsStatic: true,
		contextText: "code-review · /home/jingkaihe/workspace/kodelet",
		draft: "Focus the review on the extracted components.",
		showStop: true,
		canStop: true,
		stopActionLabel: "Stop",
		submitActionLabel: "Steer",
	},
};

export const ErrorWithAttachment: Story = {
	args: {
		attachments: [sampleAttachment],
		draft: "",
		streamError: "Failed to send message",
		submitDisabled: false,
	},
};
