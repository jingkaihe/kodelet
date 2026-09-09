import { z } from "zod";

/** Credential-free options for daemon-backed ACP sessions. */
export const executionOptionsSchema = z.strictObject({
  provider: z.enum(["openai", "anthropic"]).optional(),
  model: z.string().min(1).optional(),
  weakModel: z.string().min(1).optional(),
  maxTokens: z.number().int().positive().optional(),
  weakModelMaxTokens: z.number().int().positive().optional(),
  thinkingBudgetTokens: z.number().int().nonnegative().optional(),
  reasoningEffort: z.string().min(1).optional(),
  maxTurns: z.number().int().nonnegative().optional(),
  useWeakModel: z.boolean().optional(),
  noTools: z.boolean().optional(),
  noExtensions: z.boolean().optional(),
  noSkills: z.boolean().optional(),
  allowedTools: z.array(z.string().min(1)).optional(),
  allowedCommands: z.array(z.string().min(1)).optional(),
  enableFSSearchTools: z.boolean().optional(),
});

export type ExecutionOptions = z.infer<typeof executionOptionsSchema>;

/** @internal Model and provider settings for remote profile registration only. */
export const extensionProfileOptionsSchema = executionOptionsSchema.pick({
  provider: true,
  model: true,
  weakModel: true,
  maxTokens: true,
  weakModelMaxTokens: true,
  thinkingBudgetTokens: true,
  reasoningEffort: true,
}).required({ provider: true, model: true }).extend({
  openai: z.record(z.string(), z.json()).optional(),
  anthropic: z.record(z.string(), z.json()).optional(),
  anthropicAPIAccess: z.enum(["auto", "subscription", "api-key"]).optional(),
}).superRefine((options, ctx) => {
  if (options.provider !== "openai" && options.openai !== undefined) {
    ctx.addIssue({
      code: "custom",
      path: ["openai"],
      message: "openai settings require the openai provider",
    });
  }
  if (options.provider !== "anthropic" && (options.anthropic !== undefined || options.anthropicAPIAccess !== undefined)) {
    ctx.addIssue({
      code: "custom",
      message: "anthropic settings require the anthropic provider",
    });
  }
});

export type ExtensionProfileOptions = z.infer<typeof extensionProfileOptionsSchema>;

/** @internal ACP is a thin daemon client; flags preserve explicit false/empty values. */
export function executionArgs(options: ExecutionOptions): string[] {
  return Object.entries(options).map(([name, value]) => {
    const flag = name === "enableFSSearchTools" ? "enable-fs-search-tools" : name.replace(/[A-Z]/g, (letter) => `-${letter.toLowerCase()}`);
    const encoded = Array.isArray(value) ? value.map((item) => `"${item.replaceAll('"', '""')}"`).join(",") : String(value);
    return `--${flag}=${encoded}`;
  });
}

/** @internal Convert supported legacy inline settings; never write a config file. */
export function remoteExecutionOptions(input: Record<string, unknown>): ExecutionOptions {
  const aliases: Record<string, string> = {
    weak_model: "weakModel", max_tokens: "maxTokens", weak_model_max_tokens: "weakModelMaxTokens",
    thinking_budget_tokens: "thinkingBudgetTokens", reasoning_effort: "reasoningEffort", max_turns: "maxTurns",
    use_weak_model: "useWeakModel", no_tools: "noTools", no_extensions: "noExtensions", no_skills: "noSkills",
    allowed_tools: "allowedTools", allowed_commands: "allowedCommands", enable_fs_search_tools: "enableFSSearchTools",
  };
  const options: Record<string, unknown> = {};
  for (const [key, value] of Object.entries(input)) {
    if (key === "name" || value === undefined) continue;
    const mapped = aliases[key] ?? key;
    if (mapped in options) throw new Error(`Duplicate execution option: ${mapped}`);
    options[mapped] = value;
  }
  return executionOptionsSchema.parse(options);
}
