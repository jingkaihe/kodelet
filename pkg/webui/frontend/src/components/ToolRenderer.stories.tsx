import type { Meta, StoryObj } from "@storybook/react-vite";
import { expect } from "storybook/test";
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

export const McpJsonOutput: Story = {
	args: {
		toolInput: undefined,
		toolResult: {
			toolName: "mcp__maco_code_execute",
			metadataType: "extension_tool",
			success: true,
			metadata: {
				type: "extension_tool",
				extensionID: "mcp",
				toolName: "mcp__maco_code_execute",
				executionTime: 3061000000,
				output: JSON.stringify({
					ok: true,
					exit_code: 0,
					stdout: '### Result\n{"results":[{"width":1440,"height":960,"story":"compact","font":"JetBrains Mono, monospace","fits":true},{"width":390,"height":844,"story":"compact","fits":true}]}',
					stderr: "",
				}),
			},
		},
	},
	render: (args) => (
		<div className="activity-detail-content">
			<ToolRenderer {...args} />
		</div>
	),
	play: async ({ canvasElement }) => {
		const code = canvasElement.querySelector(".tool-code-block code");
		const header = canvasElement.querySelector(".quiet-tool-line");
		if (!code || !header || !code.parentElement) {
			throw new Error("Expected MCP output and its header");
		}
		const styles = getComputedStyle(code);
		const rootSize = Number.parseFloat(getComputedStyle(canvasElement.ownerDocument.documentElement).fontSize);
		expect(Number.parseFloat(styles.fontSize)).toBeCloseTo(rootSize * 0.75);
		expect(Number.parseFloat(styles.lineHeight)).toBeCloseTo(rootSize * 0.75 * 1.45);
		expect(styles.fontFamily).toBe(getComputedStyle(header).fontFamily);
		expect(styles.color).toBe(getComputedStyle(code.parentElement).color);
		expect(styles.whiteSpace).toBe("pre-wrap");
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
