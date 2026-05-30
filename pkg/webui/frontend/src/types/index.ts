// Core types for the Kodelet Web UI

export interface Message {
	role: "user" | "assistant";
	content: string | ContentBlock[];
	toolCalls?: ToolCall[];
	tool_calls?: ToolCall[]; // Alternative format
	thinkingText?: string; // For Claude thinking blocks
	thinkingTexts?: string[]; // Multiple persisted thinking blocks
}

export interface ContentBlock {
	type: "text" | "image" | "slash-command" | "goal";
	text?: string;
	command?: string;
	source?: {
		data: string;
		media_type: string;
	};
	image_url?: {
		url: string;
	};
}

export interface ToolCall {
	id: string;
	function: {
		name: string;
		arguments: string;
	};
}

export interface ToolResult {
	toolName: string;
	metadataType?: string;
	success: boolean;
	error?: string;
	metadata?:
		| FileMetadata
		| ApplyPatchMetadata
		| BashMetadata
		| GrepMetadata
		| GlobMetadata
		| WebFetchMetadata
		| ThinkingMetadata
		| SubagentMetadata
		| BatchMetadata
		| ViewImageMetadata
		| BrowserMetadata
		| SkillMetadata
		| OpenAIWebSearchMetadata
		| ReadConversationMetadata
		| CodeExecutionMetadata
		| ExtensionToolMetadata
		| MCPToolMetadata
		| Record<string, unknown>;
	timestamp?: string;
}

export interface Usage {
	inputTokens?: number;
	outputTokens?: number;
	cacheCreationInputTokens?: number;
	cacheReadInputTokens?: number;
	inputCost?: number;
	outputCost?: number;
	cacheCreationCost?: number;
	cacheReadCost?: number;
	currentContextWindow?: number;
	maxContextWindow?: number;
}

export interface Conversation {
	id: string;
	messages?: Message[]; // Optional for list view
	pendingSteer?: Message[];
	toolResults?: Record<string, ToolResult>; // Optional for list view
	usage?: Usage; // Optional for list view
	createdAt: string;
	updatedAt: string;
	messageCount: number;
	summary?: string;
	provider?: string;
	cwd?: string;
	cwdLocked?: boolean;
	profile?: string;
	profileLocked?: boolean;
	platform?: string;
	api_mode?: string;
	metadata?: Record<string, unknown>;
	preview?: string;
	firstMessage?: string; // For list view - truncated first user message
	created_at?: string; // Alternative format
	updated_at?: string; // Alternative format
}

export interface ConversationListResponse {
	conversations: Conversation[];
	hasMore: boolean;
	total: number;
	limit: number;
	offset: number;
	stats?: ConversationStats;
}

export interface ConversationStats {
	totalConversations: number;
	totalMessages: number;
	totalTokens: number;
	totalCost: number;
	inputTokens: number;
	outputTokens: number;
	cacheReadTokens: number;
	cacheWriteTokens: number;
	inputCost: number;
	outputCost: number;
	cacheReadCost: number;
	cacheWriteCost: number;
}

export interface SearchFilters {
	searchTerm: string;
	sortBy: "updated" | "created" | "messages";
	sortOrder: "asc" | "desc";
	limit: number;
	offset: number;
}

export interface ApiError {
	error: string;
	message?: string;
}

export interface ChatRequest {
	message: string;
	content?: ContentBlock[];
	conversationId?: string;
	profile?: string;
	cwd?: string;
}

export interface SteerConversationRequest {
	message: string;
	content?: ContentBlock[];
}

export interface SteerConversationResponse {
	success: boolean;
	conversation_id: string;
	queued: boolean;
}

export interface StopConversationResponse {
	success: boolean;
	conversation_id: string;
	stopped: boolean;
}

export interface ForkConversationResponse {
	success: boolean;
	conversation_id: string;
}

export interface ChatProfileOption {
	name: string;
	scope: string;
	active?: boolean;
}

export interface SlashCommandOption {
	name: string;
	description: string;
	hint?: string;
	placeholder?: string;
}

export interface SlashCommandsResponse {
	commands: SlashCommandOption[];
}

export interface ChatSettings {
	currentProfile?: string;
	profiles: ChatProfileOption[];
	defaultCWD?: string;
}

export interface CWDHint {
	path: string;
}

export interface CWDHintsResponse {
	baseDir?: string;
	query?: string;
	hints: CWDHint[];
}

export interface GitDiffResponse {
	cwd: string;
	diff: string;
	has_diff: boolean;
	git_root?: string;
	exit_code: number;
}

export interface TerminalReadyEvent {
	type: "ready";
	cwd: string;
	name: string;
	git: boolean;
	pid: number;
}

export interface TerminalExitEvent {
	type: "exit";
	code: number;
}

export interface TerminalInfoEvent {
	type: "info";
	text: string;
}

export interface TerminalReplayCompleteEvent {
	type: "replay-complete";
}

export interface TerminalInputMessage {
	type: "input";
	data: string;
}

export interface TerminalResizeMessage {
	type: "resize";
	rows: number;
	cols: number;
}

export interface TerminalSignalMessage {
	type: "signal";
	name: string;
}

export type TerminalServerEvent =
	| TerminalReadyEvent
	| TerminalExitEvent
	| TerminalInfoEvent
	| TerminalReplayCompleteEvent;
export type TerminalClientMessage =
	| TerminalInputMessage
	| TerminalResizeMessage
	| TerminalSignalMessage;

export interface PendingImageAttachment {
	id: string;
	name: string;
	mediaType: string;
	data: string;
	previewUrl: string;
	size: number;
}

export interface ChatStreamEvent {
	kind:
		| "conversation"
		| "usage"
		| "thinking-start"
		| "thinking-delta"
		| "thinking-end"
		| "thinking"
		| "text-delta"
		| "content-end"
		| "text"
		| "user-message"
		| "tool-use"
		| "tool-result"
		| "done"
		| "error";
	conversation_id?: string;
	role?: "user" | "assistant";
	delta?: string;
	content?: string | ContentBlock[];
	usage?: Usage;
	tool_name?: string;
	tool_call_id?: string;
	input?: string;
	tool_result?: ToolResult;
	error?: string;
}

export interface ChatRenderMessage {
	role: "user" | "assistant";
	content?: string | ContentBlock[];
	blocks?: ChatAssistantBlock[];
}

export type ChatAssistantBlock =
	| {
			type: "thinking";
			content: string;
			inProgress?: boolean;
	  }
	| {
			type: "message";
			content: string | ContentBlock[];
			inProgress?: boolean;
	  }
	| {
			type: "tools";
			tools: ChatRenderToolCall[];
	  };

export interface ChatRenderToolCall {
	callId: string;
	name: string;
	input: string;
	result?: ToolResult;
}

// Tool renderer types
export interface ToolRenderProps {
	toolResult: ToolResult;
	toolInput?: string;
}

export interface FileMetadata {
	filePath: string;
	language?: string;
	size?: number;
	lines?: string[];
	offset?: number;
	lineLimit?: number;
	totalLines?: number;
	remainingLines?: number;
	truncated?: boolean;
}

export interface ApplyPatchMetadata {
	changes: ApplyPatchChange[];
	added?: string[];
	modified?: string[];
	deleted?: string[];
}

export interface ApplyPatchChange {
	path: string;
	operation: "add" | "delete" | "update" | string;
	oldContent?: string;
	newContent?: string;
	unifiedDiff?: string;
	movePath?: string;
}

export interface BashMetadata {
	command: string;
	output?: string;
	exitCode?: number;
	executionTime?: number;
	workingDir?: string;
}

export interface GrepMetadata {
	pattern: string;
	path?: string;
	include?: string;
	results: GrepResult[];
	truncated?: boolean;
	truncationReason?: "file_limit" | "output_size" | "";
	maxResults?: number;
}

export interface GrepResult {
	filePath: string;
	matches?: GrepMatch[];
	lineNumber?: number;
	content?: string;
}

export interface GrepMatch {
	lineNumber: number;
	content: string;
	isContext?: boolean;
}

export interface GlobMetadata {
	pattern: string;
	path?: string;
	files: FileInfo[];
	truncated?: boolean;
}

export interface FileInfo {
	path: string;
	name?: string;
	size?: number;
	modTime?: string;
	modified?: string;
	type?: string;
	language?: string;
}

export interface WebFetchMetadata {
	url: string;
	contentType?: string;
	size?: number;
	savedPath?: string;
	filePath?: string;
	prompt?: string;
	processedType?: string;
	content?: string;
}

export interface ThinkingMetadata {
	thought: string;
}

export interface SubagentMetadata {
	question: string;
	response?: string;
	workflow?: string;
	cwd?: string;
}

export interface BatchMetadata {
	description: string;
	subResults: ToolResult[];
	results?: ToolResult[];
	successCount?: number;
	failureCount?: number;
}

export interface ViewImageMetadata {
	path?: string;
	mimeType?: string;
	mime_type?: string;
	detail?: string;
	imageSize?: {
		width?: number;
		height?: number;
	};
}

export interface BrowserMetadata {
	url: string;
	title?: string;
	pageTitle?: string;
	filePath?: string;
	file_path?: string;
	path?: string;
	dimensions?: string;
	size?: string;
}

export interface SkillMetadata {
	skillName: string;
	directory: string;
}

export interface OpenAIWebSearchMetadata {
	callId?: string;
	status?: string;
	action?: string;
	queries?: string[];
	sources?: string[];
	results?: string[];
	url?: string;
	pattern?: string;
}

export interface ReadConversationMetadata {
	conversationID?: string;
	conversationId?: string;
	goal?: string;
	content?: string;
}

export interface CodeExecutionMetadata {
	code?: string;
	output?: string;
	runtime?: string;
}

export interface ExtensionToolMetadata {
	extensionID?: string;
	extensionId?: string;
	toolName?: string;
	executionTime?: number;
	output?: string;
	data?: Record<string, unknown>;
}

export interface MCPContent {
	type?: string;
	text?: string;
	data?: string;
	mimeType?: string;
	uri?: string;
	metadata?: Record<string, unknown>;
}

export interface MCPToolMetadata {
	mcpToolName?: string;
	serverName?: string;
	parameters?: Record<string, unknown>;
	content?: MCPContent[];
	contentText?: string;
	executionTime?: number;
}

// Component props
export interface ConversationListProps {
	conversations: Conversation[];
	loading: boolean;
	error: string | null;
	hasMore: boolean;
	onLoadMore: () => void;
	onSearch: (filters: SearchFilters) => void;
	onDelete: (conversationId: string) => void;
}

export interface ConversationViewProps {
	conversation: Conversation;
	loading: boolean;
	error: string | null;
	onExport: () => void;
	onDelete: () => void;
}
