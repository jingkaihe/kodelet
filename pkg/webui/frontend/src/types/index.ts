// Core types for the Kodelet Web UI

export interface Message {
  role: 'user' | 'assistant';
  content: string | ContentBlock[];
  toolCalls?: ToolCall[];
  tool_calls?: ToolCall[]; // Alternative format
  thinkingText?: string; // For Claude thinking blocks
}

export interface ContentBlock {
  type: 'text' | 'image';
  text?: string;
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
  success: boolean;
  error?: string;
  metadata?: FileMetadata | BashMetadata | GrepMetadata | GlobMetadata | WebFetchMetadata | ThinkingMetadata | TodoMetadata | SubagentMetadata | BatchMetadata | ImageRecognitionMetadata | BrowserMetadata | BackgroundProcessMetadata | Record<string, unknown>;
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
  toolResults?: Record<string, ToolResult>; // Optional for list view
  usage?: Usage; // Optional for list view
  createdAt: string;
  updatedAt: string;
  messageCount: number;
  summary?: string;
  modelType?: string;
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
  sortBy: 'updated' | 'created' | 'messages';
  sortOrder: 'asc' | 'desc';
  limit: number;
  offset: number;
}

export interface ApiError {
  error: string;
  message?: string;
}

// Tool renderer types
export interface ToolRenderProps {
  toolResult: ToolResult;
}

export interface FileMetadata {
  filePath: string;
  language?: string;
  size?: number;
  lines?: string[];
  offset?: number;
  totalLines?: number;
  truncated?: boolean;
}

export interface BashMetadata {
  command: string;
  output?: string;
  exitCode?: number;
  executionTime?: number;
  workingDir?: string;
  pid?: number;
  logPath?: string;
  logFile?: string;
  startTime?: string;
}

export interface GrepMetadata {
  pattern: string;
  path?: string;
  include?: string;
  results: GrepResult[];
  truncated?: boolean;
}

export interface GrepResult {
  file: string;
  filename?: string;
  matches?: GrepMatch[];
  lineNumber?: number;
  line_number?: number;
  content?: string;
  line?: string;
}

export interface GrepMatch {
  lineNumber: number;
  line_number?: number;
  content: string;
  line?: string;
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
}

export interface WebFetchMetadata {
  url: string;
  contentType?: string;
  savedPath?: string;
  filePath?: string;
  prompt?: string;
  content?: string;
}

export interface ThinkingMetadata {
  thought: string;
}

export interface TodoMetadata {
  action: string;
  todos: TodoItem[];
  todoList?: TodoItem[];
}

export interface TodoItem {
  content: string;
  status: 'pending' | 'in_progress' | 'completed' | 'canceled';
  priority: 'low' | 'medium' | 'high';
}

export interface SubagentMetadata {
  question: string;
  response?: string;
}

export interface BatchMetadata {
  description: string;
  subResults: ToolResult[];
  results?: ToolResult[];
  successCount?: number;
  failureCount?: number;
}

export interface ImageRecognitionMetadata {
  imagePath?: string;
  image_path?: string;
  path?: string;
  prompt?: string;
  analysis?: string;
  result?: string;
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

export interface BackgroundProcessMetadata {
  processes: BackgroundProcess[];
  processCount?: number;
}

export interface BackgroundProcess {
  pid: number;
  command: string;
  status: 'running' | 'stopped';
  startTime?: string;
  logPath?: string;
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