package codemode

// RuntimeDeclaration is the TypeScript contract of the code-execution VM. The
// code_execute description embeds it verbatim and Describe declares each tool
// against these types, so the model reads the shared types exactly once.
const RuntimeDeclaration = `declare const catalog: {
  list(options?: CatalogOptions): Promise<ToolPage>;
  search(query: string, options?: CatalogOptions): Promise<ToolPage>;
  describe(name: string): Promise<{name: string; description: string; group?: string; inputSchema: object; outputSchema?: object; declaration: string}>;
};
interface CatalogOptions { group?: string; limit?: number; cursor?: string }
interface ToolPage { tools: {name: string; description: string; group?: string}[]; nextCursor?: string }

declare const tools: {[name: string]: (input: object) => Promise<ToolReply>};

interface ToolReply<T = unknown> {
  data: T | null;
  text: string;
  attachments: ArtifactRef[];
  truncated: boolean;
}
interface ArtifactRef {
  type: "image"; artifactId?: string; error?: string; mimeType?: string; width?: number; height?: number;
}

interface ToolError extends Error {
  kind: "blocked" | "invalid_input" | "tool_error" | "transport" | "cancelled" | "limit" | "invalid_output";
  tool: string;
  callId?: string;
  outcome: "not_started" | "completed" | "unknown";
  result?: ToolReply;
}

declare function emit(value: unknown): void;
declare namespace emit {
  function image(ref: string | ArtifactRef, options?: {detail?: "original"}): void; // sends pixels to the model
  function artifact(ref: string | ArtifactRef): void; // retains without sending pixels
}
`
