import { useState, type ComponentProps } from "react";
import type { Meta, StoryObj } from "@storybook/react-vite";
import { fn, userEvent, within } from "storybook/test";
import ChatSidebar, {
	ChatSidebarCollapsedRail,
	ConversationSearchDialog,
} from "./ChatSidebar";
import { sampleConversations } from "../../stories/fixtures";

const conversationSearchCwdOptions = [
	"/home/jingkaihe/workspace/kodelet",
	"/home/jingkaihe/workspace/plugins",
];

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
		authPrincipal: {
			id: "https://issuer.example.com|jingkai-he",
			issuer: "https://issuer.example.com",
			subject: "jingkai-he",
			name: "Jingkai He",
			email: "jingkai@example.com",
			roles: ["user"],
		},
		conversations: sampleConversations,
		disabled: false,
		loading: false,
		onDeleteConversation: fn(),
		onForkConversation: fn(),
		onHide: fn(),
		onNewChat: fn(),
		onSearch: fn(),
		onSelectConversation: fn(),
		searchActive: false,
	},
} satisfies Meta<typeof ChatSidebar>;

export default meta;

type Story = StoryObj<typeof meta>;

const ConversationSearchStory = (
	args: Pick<ComponentProps<typeof ChatSidebar>, "onSelectConversation">,
) => {
	const [searchTerm, setSearchTerm] = useState("");
	const [cwdFilter, setCwdFilter] = useState("");
	const normalizedSearch = searchTerm.trim().toLowerCase();
	const conversations = sampleConversations.filter((conversation) => {
		if (cwdFilter && conversation.cwd !== cwdFilter) {
			return false;
		}

		if (!normalizedSearch) {
			return true;
		}

		return [
			conversation.id,
			conversation.summary,
			conversation.preview,
			conversation.firstMessage,
			conversation.cwd,
		].some((value) => value?.toLowerCase().includes(normalizedSearch));
	});

	return (
		<ConversationSearchDialog
			conversations={conversations}
			cwdFilter={cwdFilter}
			cwdOptions={conversationSearchCwdOptions}
			loading={false}
			onClose={fn()}
			onCwdFilterChange={setCwdFilter}
			onSearchTermChange={setSearchTerm}
			onSelectConversation={args.onSelectConversation}
			searchTerm={searchTerm}
		/>
	);
};

export const GroupedConversations: Story = {};

export const ConversationSearchModal: Story = {
	render: (args) => <ConversationSearchStory {...args} />,
};

export const CollapsedRail: Story = {
	render: (args) => (
		<ChatSidebarCollapsedRail
			disabled={args.disabled}
			onNewChat={args.onNewChat}
			onOpen={() => args.onHide?.()}
			onSearch={args.onSearch}
			searchActive={true}
		/>
	),
};

export const AccountMenuOpen: Story = {
	play: async ({ canvasElement }) => {
		const canvas = within(canvasElement);
		const accountButton = canvas.getByRole("button", {
			name: "Jingkai He account menu",
		});
		await userEvent.click(accountButton);
		accountButton.blur();
	},
};

export const WithoutOIDCSession: Story = {
	args: {
		authPrincipal: null,
	},
};

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
