import type { Meta, StoryObj } from "@storybook/react-vite";
import PendingSteerList from "./PendingSteerList";
import { sampleAttachment } from "../../stories/fixtures";

const meta = {
	title: "Chat/PendingSteerList",
	component: PendingSteerList,
	parameters: {
		layout: "padded",
	},
	args: {
		messages: [
			{
				role: "user",
				content: "When you continue, focus on the Storybook smoke tests.",
			},
			{
				role: "user",
				content: [
					{ type: "text", text: "Also check this screenshot." },
					{ type: "image", image_url: { url: sampleAttachment.previewUrl } },
				],
			},
		],
	},
} satisfies Meta<typeof PendingSteerList>;

export default meta;

type Story = StoryObj<typeof meta>;

export const QueuedGuidance: Story = {};

export const Empty: Story = {
	args: {
		messages: [],
	},
};
