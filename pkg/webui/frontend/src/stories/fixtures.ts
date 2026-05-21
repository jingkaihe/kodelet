import type {
	ChatProfileOption,
	ChatRenderMessage,
	Conversation,
	CWDHint,
	GitDiffResponse,
	PendingImageAttachment,
	SlashCommandOption,
	ToolResult,
} from "../types";

export const sampleConversations: Conversation[] = [
	 {
		id: "conv-active",
		createdAt: "2026-05-19T17:20:00Z",
		updatedAt: "2026-05-20T10:35:00Z",
		messageCount: 18,
		summary: "Extract chat UI into stories",
		cwd: "/home/jingkaihe/workspace/kodelet",
		profile: "default",
	},
	{
		id: "conv-review",
		createdAt: "2026-05-18T14:10:00Z",
		updatedAt: "2026-05-19T21:02:00Z",
		messageCount: 9,
		preview: "Review terminal session handling",
		cwd: "/home/jingkaihe/workspace/kodelet",
		profile: "code-review",
	},
	{
		id: "conv-docs",
		createdAt: "2026-05-17T09:00:00Z",
		updatedAt: "2026-05-18T09:30:00Z",
		messageCount: 5,
		firstMessage: "Document the plugin install flow",
		cwd: "/home/jingkaihe/workspace/plugins",
		profile: "docs",
	},
	{
		id: "conv-empty-cwd",
		createdAt: "2026-05-16T11:00:00Z",
		updatedAt: "2026-05-16T12:30:00Z",
		messageCount: 2,
		summary: "Untitled quick check",
	},
];

export const sampleSlashCommands: SlashCommandOption[] = [
	{
		name: "review",
		description: "Review the current changes",
		hint: "[focus area]",
		placeholder: "/review frontend extraction",
	},
	{
		name: "test",
		description: "Run the relevant tests",
		hint: "[package]",
	},
	{
		name: "docs",
		description: "Update documentation for this change",
	},
];

export const sampleProfiles: ChatProfileOption[] = [
	{ name: "default", scope: "global", active: true },
	{ name: "code-review", scope: "repo" },
	{ name: "docs", scope: "repo" },
];

export const sampleCwdHints: CWDHint[] = [
	{ path: "/home/jingkaihe/workspace/kodelet" },
	{ path: "/home/jingkaihe/workspace/kodelet/pkg/webui/frontend" },
	{ path: "/home/jingkaihe/workspace/plugins" },
];

export const sampleAttachment: PendingImageAttachment = {
	id: "attachment-1",
	name: "sidebar-state.png",
	mediaType: "image/png",
	data: "",
	previewUrl:
		"data:image/png;base64,iVBORw0KGgoAAAANSUhEUgAAAEAAAABACAYAAACqaXHeAAAAIElEQVR4nO3BMQEAAADCoPVPbQ0PoAAAAAAAAAAA4G0YQAABH9H9kQAAAABJRU5ErkJggg==",
	size: 48210,
};

export const sampleBashToolResult: ToolResult = {
	toolName: "bash",
	success: true,
	metadata: {
		command: "npm run test:run -- ChatComposer",
		output:
			"✓ src/components/chat/ChatComposer.test.tsx (3 tests)\n\nTest Files  1 passed\nTests       3 passed",
		exitCode: 0,
		executionTime: 1.42,
		workingDir: "/home/jingkaihe/workspace/kodelet/pkg/webui/frontend",
	},
};

export const sampleFileReadToolResult: ToolResult = {
	toolName: "file_read",
	success: true,
	metadata: {
		filePath: "pkg/webui/frontend/src/components/chat/ChatComposer.tsx",
		language: "tsx",
		lines: [
			"interface ChatComposerProps {",
			"  draft: string;",
			"  onDraftChange: (value: string) => void;",
			"}",
		],
		offset: 1,
		lineLimit: 80,
		remainingLines: 42,
	},
};

export const sampleChatMessages: ChatRenderMessage[] = [
	{
		role: "user",
		content: [
			{
				type: "text",
				text: "Please extract the composer so we can render it in Storybook.",
			},
			{
				type: "image",
				image_url: { url: sampleAttachment.previewUrl },
			},
		],
	},
	{
		role: "assistant",
		blocks: [
			{
				type: "thinking",
				content: "The page should keep state and the composer should take props.",
			},
			{
				type: "tools",
				tools: [
					{
						callId: "tool-1",
						name: "bash",
						input: JSON.stringify({
							command: "npm run test:run -- ChatComposer",
							description: "Run focused component tests",
						}),
						result: sampleBashToolResult,
					},
				],
			},
			{
				type: "message",
				content:
					"Done. The composer is now isolated and has a focused story with attachments, slash commands, and streaming controls.",
			},
		],
	},
];

export const sampleGitDiff: GitDiffResponse = {
	cwd: "/home/jingkaihe/workspace/kodelet",
	diff: [
		"diff --git a/pkg/webui/frontend/src/pages/ChatPage.tsx b/pkg/webui/frontend/src/pages/ChatPage.tsx",
		"index 4a4d0a1..7f0959b 100644",
		"--- a/pkg/webui/frontend/src/pages/ChatPage.tsx",
		"+++ b/pkg/webui/frontend/src/pages/ChatPage.tsx",
		"@@ -20,6 +20,7 @@",
		"+import ChatComposer from \"../components/chat/ChatComposer\";",
	].join("\n"),
	has_diff: true,
	git_root: "/home/jingkaihe/workspace/kodelet",
	exit_code: 0,
};
