import React from "react";
import type { Meta, StoryObj } from "@storybook/react-vite";
import { fn } from "storybook/test";
import ChatComposer from "./ChatComposer";
import ChatSidebar from "./ChatSidebar";
import ChatTranscript from "./ChatTranscript";
import {
	sampleAttachment,
	sampleChatMessages,
	sampleConversations,
	sampleSlashCommands,
} from "../../stories/fixtures";

type ChatComposerStoryProps = React.ComponentProps<typeof ChatComposer>;

const InteractiveComposer = (args: ChatComposerStoryProps) => {
	const [draft, setDraft] = React.useState(args.draft);
	const [attachments, setAttachments] = React.useState(args.attachments);

	return (
		<ChatComposer
			{...args}
			attachments={attachments}
			draft={draft}
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
		onPaste: fn(),
		onRemoveAttachment: fn(),
		onSelectSlashCommand: fn(),
		onStop: fn(),
		onSubmit: fn(),
	},
} satisfies Meta<typeof ChatComposer>;

export default meta;

type Story = StoryObj<typeof meta>;

export const ReadyToSend: Story = {};

export const Multiline: Story = {
	args: {
		draft: "Review these points:\n- mobile layout\n- terminal behavior",
	},
};

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

export const InWorkspace: Story = {
	args: {
		...SteeringActiveConversation.args,
		draft: "",
		placeholder: "Steer the active conversation…",
		submitDisabled: true,
	},
	render: (args) => (
		<div className="flex h-dvh">
			<div className="hidden w-80 shrink-0 border-r border-black/10 lg:block">
				<ChatSidebar
					activeConversationId="conv-active"
					authPrincipal={{
						id: "https://issuer.example.com|jingkai-he",
						issuer: "https://issuer.example.com",
						subject: "jingkai-he",
						name: "Jingkai He",
						roles: ["user"],
					}}
					conversations={[
						...sampleConversations,
						{
							...sampleConversations[0],
							id: "child-review",
							summary: "Review sidebar changes",
							isRunning: true,
							metadata: { parent_conversation_id: "conv-active" },
						},
					]}
					loading={false}
					onDeleteConversation={fn()}
					onForkConversation={fn()}
					onHide={fn()}
					onNewChat={fn()}
					onSearch={fn()}
					onSelectConversation={fn()}
				/>
			</div>
			<main className="chat-main-panel flex min-w-0 flex-1 flex-col overflow-hidden">
				<div className="chat-main-scroll min-h-0 flex-1 overflow-y-auto">
					<ChatTranscript
						emptyStateTitle="Good afternoon"
						isStreaming={false}
						messages={[
							{
								role: "user",
								content: "Please make the composer and sidebar match the transcript.",
							},
							sampleChatMessages[1],
						]}
					/>
				</div>
				<InteractiveComposer {...args} />
			</main>
		</div>
	),
};
