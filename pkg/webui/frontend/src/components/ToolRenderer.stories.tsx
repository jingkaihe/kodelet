import type { Meta, StoryObj } from "@storybook/react-vite";
import ToolRenderer from "./ToolRenderer";
import {
	sampleBashToolResult,
	sampleFileReadToolResult,
} from "../stories/fixtures";

const meta = {
	title: "Tools/ToolRenderer",
	component: ToolRenderer,
	parameters: {
		layout: "padded",
	},
	args: {
		toolInput: JSON.stringify({
			command: "npm run test:run -- ChatComposer",
			description: "Run focused component tests",
		}),
		toolResult: sampleBashToolResult,
	},
} satisfies Meta<typeof ToolRenderer>;

export default meta;

type Story = StoryObj<typeof meta>;

export const BashSuccess: Story = {};

export const FileRead: Story = {
	args: {
		toolInput: JSON.stringify({
			file_path: "pkg/webui/frontend/src/components/chat/ChatComposer.tsx",
			offset: 1,
			line_limit: 80,
		}),
		toolResult: sampleFileReadToolResult,
	},
};

export const FailureFallback: Story = {
	args: {
		toolInput: JSON.stringify({ path: "missing.tsx" }),
		toolResult: {
			toolName: "file_read",
			success: false,
			error: "file does not exist",
		},
	},
};
