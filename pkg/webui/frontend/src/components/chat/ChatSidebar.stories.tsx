import type { Meta, StoryObj } from "@storybook/react-vite";
import { fn } from "storybook/test";
import ChatSidebar from "./ChatSidebar";
import { sampleConversations } from "../../stories/fixtures";

const meta = {
	title: "Chat/ChatSidebar",
	component: ChatSidebar,
	parameters: {
		layout: "fullscreen",
	},
	decorators: [
		(Story) => (
			<div className="h-screen max-w-[360px]">
				<Story />
			</div>
		),
	],
	args: {
		activeConversationId: "conv-active",
		conversations: sampleConversations,
		disabled: false,
		loading: false,
		onDeleteConversation: fn(),
		onForkConversation: fn(),
		onHide: fn(),
		onNewChat: fn(),
		onSelectConversation: fn(),
	},
} satisfies Meta<typeof ChatSidebar>;

export default meta;

type Story = StoryObj<typeof meta>;

export const GroupedConversations: Story = {};

export const RunningConversation: Story = {
	args: {
		conversations: sampleConversations.map((conversation) =>
			conversation.id === "conv-active"
				? { ...conversation, isRunning: true }
				: conversation,
		),
	},
};

export const Loading: Story = {
	args: {
		conversations: [],
		loading: true,
	},
};

export const DisabledDuringStartup: Story = {
	args: {
		disabled: true,
		conversations: sampleConversations.map((conversation) =>
			conversation.id === "conv-active"
				? { ...conversation, isRunning: true }
				: conversation,
		),
	},
};
