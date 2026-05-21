import type { Meta, StoryObj } from "@storybook/react-vite";
import ChatTranscript from "./ChatTranscript";
import { sampleChatMessages } from "../../stories/fixtures";

const meta = {
	title: "Chat/ChatTranscript",
	component: ChatTranscript,
	parameters: {
		layout: "fullscreen",
	},
	decorators: [
		(Story) => (
			<div className="chat-main-panel min-h-screen">
				<Story />
			</div>
		),
	],
	args: {
		emptyStateTitle: "Good afternoon",
		isStreaming: false,
		messages: sampleChatMessages,
	},
} satisfies Meta<typeof ChatTranscript>;

export default meta;

type Story = StoryObj<typeof meta>;

export const WithToolActivity: Story = {};

export const EmptyState: Story = {
	args: {
		messages: [],
	},
};

export const Streaming: Story = {
	args: {
		isStreaming: true,
		messages: [
			...sampleChatMessages,
			{
				role: "assistant",
				blocks: [
					{
						type: "message",
						content: "I am updating the component boundary now",
						inProgress: true,
					},
				],
			},
		],
	},
};
