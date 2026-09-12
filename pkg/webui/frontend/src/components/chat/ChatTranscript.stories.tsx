import type { Meta, StoryObj } from "@storybook/react-vite";
import { useState } from "react";
import ChatTranscript from "./ChatTranscript";
import { sampleChatMessages } from "../../stories/fixtures";
import type {
	ApplyPatchMetadata,
	BashMetadata,
	ChatRenderMessage,
	ChatRenderToolCall,
	FileMetadata,
	TaskRunSnapshot,
	ToolPresentation,
} from "../../types";

const meta = {
	title: "Chat/ChatTranscript",
	component: ChatTranscript,
	parameters: {
		layout: "fullscreen",
	},
	decorators: [
		(Story) => (
			<div className="chat-main-panel h-screen overflow-y-auto">
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

const transcriptPath = "pkg/webui/frontend/src/components/chat/ChatTranscript.tsx";
const patchInput = [
	"*** Begin Patch",
	`*** Update File: ${transcriptPath}`,
	"@@",
	'-const summary = "Thoughts";',
	'+const summary = "Had " + count + " thoughts";',
	"*** End Patch",
].join("\n");

const readTranscript: ChatRenderToolCall = {
	callId: "terminal-read",
	name: "file_read",
	input: JSON.stringify({ file_path: transcriptPath, offset: 124, line_limit: 1 }),
	result: {
		toolName: "file_read",
		success: true,
		metadata: {
			filePath: transcriptPath,
			language: "tsx",
			lines: ['const summary = "Thoughts";'],
			offset: 124,
			lineLimit: 1,
		} satisfies FileMetadata,
	},
};

const completedPatch: ChatRenderToolCall = {
	callId: "terminal-patch",
	name: "apply_patch",
	input: JSON.stringify({ input: patchInput }),
	result: {
		toolName: "apply_patch",
		success: true,
		metadata: {
			changes: [{
				path: transcriptPath,
				operation: "update",
				oldContent: 'const summary = "Thoughts";',
				newContent: 'const summary = "Had " + count + " thoughts";',
				unifiedDiff: [
					`--- a/${transcriptPath}`,
					`+++ b/${transcriptPath}`,
					"@@ -124 +124 @@",
					'-const summary = "Thoughts";',
					'+const summary = "Had " + count + " thoughts";',
				].join("\n"),
			}],
		} satisfies ApplyPatchMetadata,
	},
};

const terminalMessages = (state: "completed" | "running" | "failed"): ChatRenderMessage[] => {
	const running = state === "running";
	const failed = state === "failed";
	const commands: ChatRenderToolCall[] = [
		{
			callId: "terminal-command-inspect",
			name: "bash",
			input: JSON.stringify({ command: "git diff --stat", description: "Inspect the transcript changes" }),
			result: {
				toolName: "bash",
				success: true,
				metadata: {
					command: "git diff --stat",
					output: "2 files changed, 34 insertions(+), 61 deletions(-)",
					exitCode: 0,
					executionTime: 124_000_000,
				} satisfies BashMetadata,
			},
		},
		{
			callId: "terminal-command-test",
			name: "bash",
			input: JSON.stringify({ command: "mise run frontend-test", description: "Check transcript behavior" }),
			inProgress: running,
			result: {
				toolName: "bash",
				success: !failed,
				error: failed ? "Transcript regression test failed." : undefined,
				metadata: {
					command: "mise run frontend-test",
					output: failed
						? "Expected the successful tool group to be collapsed."
						: running ? "✓ ChatTranscript.test.tsx (28 tests)" : "Test files  2 passed · Tests  46 passed",
					exitCode: failed ? 1 : 0,
					// Bash durations are nanoseconds; task-run durations below are milliseconds.
					executionTime: running ? 820_000_000 : 1_420_000_000,
				} satisfies BashMetadata,
			},
		},
	];
	const patch: ChatRenderToolCall = running
		? { ...completedPatch, inProgress: true, result: undefined }
		: failed ? {
			...completedPatch,
			result: {
				toolName: "apply_patch",
				success: false,
				error: "Patch context did not match. The transcript changed since it was read.",
				metadata: { changes: [] } satisfies ApplyPatchMetadata,
			},
		} : completedPatch;
	const taskRun: TaskRunSnapshot = {
		version: 1,
		revision: running ? 2 : 3,
		kind: "code_search",
		status: running ? "running" : "completed",
		phase: running ? "working" : "completed",
		title: "Trace transcript rendering",
		elapsedMs: running ? 3200 : 8400,
		counts: { succeeded: running ? 1 : 2, failed: 0, running: running ? 1 : 0 },
		activities: [
			{ id: "search-components", sequence: 1, kind: "search", label: "Located tool and thought renderers", status: "succeeded" },
			{ id: "search-progress", sequence: 2, kind: "read", label: "Check live-to-completed transitions", status: running ? "running" : "succeeded" },
		],
	};
	const extensions: ChatRenderToolCall[] = [
		{
			callId: "terminal-code-search",
			name: "code_search",
			input: JSON.stringify({ query: "Trace transcript grouping and live tool progress" }),
			inProgress: running,
			result: {
				toolName: "code_search",
				metadataType: "extension_tool",
				success: true,
				metadata: {
					extensionId: "code-search",
					toolName: "code_search",
					output: running ? "Tracing tool progress…" : "Found the grouping boundary in **ChatToolActivity**. Live results preserve each tool's output.",
					data: { taskRun },
				},
			},
		},
		{
			callId: "terminal-accessibility-review",
			name: "review_accessibility",
			input: JSON.stringify({ scope: "transcript disclosures" }),
			inProgress: running,
			result: {
				toolName: "review_accessibility",
				metadataType: "extension_tool",
				success: true,
				metadata: {
					extensionId: "workspace-review",
					toolName: "review_accessibility",
					data: {
						presentation: {
							summary: "Review transcript accessibility",
							body: running
								? "**Keyboard access** preserved. Checking focus indicators and touch targets."
								: "**Keyboard access** preserved. Focus indicators and touch targets checked.",
							format: "markdown",
						} satisfies ToolPresentation,
					},
				},
			},
		},
	];

	return [
		{ role: "user", content: "Give the transcript a TUI feel: collapse finished tools and thoughts, but keep live progress visible." },
		{
			role: "assistant",
			blocks: [
				{ type: "thinking", content: "Group adjacent built-in tools without changing the order of the conversation." },
				{ type: "thinking", content: "Keep extension presentations and live progress intact, then collapse successful groups." },
				{ type: "tools", tools: [...commands, readTranscript, patch, ...(failed ? [] : extensions)] },
				...(running ? [] : [{
					type: "message" as const,
					content: failed
						? "The regression check and patch both failed. I’ll re-read the changed lines before retrying; **no patch was applied**."
						: "Done — the transcript now follows the **TUI style**.\n\n- Completed commands, tools, and thoughts collapse into quiet summaries.\n- Live output, extension progress, and expandable diffs stay available.",
				}]),
			],
		},
	];
};

export const TerminalStyleCompleted: Story = {
	args: { messages: terminalMessages("completed") },
};

export const TerminalStyleRunning: Story = {
	args: { isStreaming: true, messages: terminalMessages("running") },
};

export const TerminalStyleFailed: Story = {
	args: { messages: terminalMessages("failed") },
};

export const TerminalStyleLifecycle: Story = {
	args: { isStreaming: true, messages: terminalMessages("running") },
	render: function Render(args) {
		const [finished, setFinished] = useState(false);
		return (
			<>
				<div role="toolbar" aria-label="Transcript lifecycle" className="mx-auto flex max-w-5xl gap-3 px-3 pt-3 sm:px-4 md:px-8">
					<button type="button" className="btn btn-sm" onClick={() => setFinished(true)} disabled={finished}>
						Finish tools
					</button>
					<span role="status" className="self-center font-mono text-xs text-kodelet-dark">
						{finished ? "Tools completed" : "Tools running"}
					</span>
				</div>
				<ChatTranscript {...args} isStreaming={!finished} messages={finished ? terminalMessages("completed") : args.messages} />
			</>
		);
	},
};
