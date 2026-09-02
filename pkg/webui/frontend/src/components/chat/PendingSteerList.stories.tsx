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

export const LongQueuedGuidance: Story = {
	args: {
		messages: [
			{
				role: "user",
				content:
					"Investigate the failed provider request, compare the installed client version with the minimum supported release, verify whether any authentication headers need to change, inspect the request-building path for model-specific behavior, confirm the migration guidance against the current SDK source, and report the required, recommended, and informational follow-up work with evidence before continuing. Include the exact error details, affected model capability checks, and any compatibility risks that should be addressed in a later cleanup.",
			},
		],
	},
};

export const Empty: Story = {
	args: {
		messages: [],
	},
};
