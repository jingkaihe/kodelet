import { defineExtension, type EventContext, z } from "kodelet";
import { runExtension } from "kodelet/runtime";

const BASH_POLICY_FILE = "bash-policy.json";
const ALLOW_ONCE = "Allow once";
const ALLOW_ALWAYS = "Allow and remember this exact command";
const DENY_ONCE = "Deny once";
const DENY_ALWAYS = "Deny and remember this exact command";
const BASH_DECISION_OPTIONS = [
    ALLOW_ONCE,
    ALLOW_ALWAYS,
    DENY_ONCE,
    DENY_ALWAYS,
] as const;

interface BashPolicy {
    allowed: string[];
    denied: string[];
}

interface BashCommandDetails {
    command: string;
    description?: string;
    timeout?: number;
}

const AskUserQuestionInput = z.object({
    question: z.string().min(1).describe("The question to ask the user"),
    options: z
        .array(z.string().min(1))
        .min(2)
        .max(5)
        .describe("The options to choose from (2-5 items)"),
});

const extension = defineExtension((ext) => {
    ext.setMetadata({ name: "workspace", version: "0.1.0" });

    ext.registerTool({
        name: "ask_user_question",
        description:
            "Present user with multiple choices when there are several possible approaches and you need them to pick one. Use when you have 2-5 concrete options to choose from",
        inputSchema: AskUserQuestionInput,
        async execute(input, ctx) {
            const choice = await ctx.ui.select({
                title: input.question,
                message: "Choose one option.",
                options: input.options,
                submitButtonText: "Select",
            });

            if (!choice) {
                return "User dismissed the question without choosing.";
            }

            const index = input.options.indexOf(choice);
            if (index >= 0) {
                return `User selected option ${index + 1}: ${choice}`;
            }
            return `User responded with: ${choice}`;
        },
    });

    ext.on("agent.start", async (_event, ctx) => {
        const policy = await readBashPolicy(ctx);
        await ctx.ui.notify({
            title: "Workspace extension ready",
            message: `Workspace extension started. Remembered bash policy: ${policy.allowed.length} allowed, ${policy.denied.length} denied.`,
        });
    });

    ext.on(
        "tool.call",
        { priority: 100, timeoutInSec: 300 },
        async (event, ctx) => {
            if (event.tool.name !== "bash") {
                return;
            }

            const details = parseBashCommandDetails(event.tool.input);
            if (!details) {
                return;
            }

            const policy = await readBashPolicy(ctx);
            if (policy.denied.includes(details.command)) {
                return {
                    block: {
                        reason: `Bash command denied by remembered workspace policy: ${details.command}`,
                    },
                };
            }
            if (policy.allowed.includes(details.command)) {
                return;
            }

            const choice = await ctx.ui.select({
                title: "Allow bash command?",
                message: formatBashPrompt(details, policy),
                options: [...BASH_DECISION_OPTIONS],
                submitButtonText: "Apply",
                cancelButtonText: "Deny",
            });

            if (choice === ALLOW_ONCE) {
                return;
            }
            if (choice === ALLOW_ALWAYS) {
                await rememberBashDecision(
                    ctx,
                    details.command,
                    "allowed",
                    policy,
                );
                return;
            }
            if (choice === DENY_ALWAYS) {
                await rememberBashDecision(
                    ctx,
                    details.command,
                    "denied",
                    policy,
                );
                return {
                    block: {
                        reason: `Bash command denied and remembered by workspace policy: ${details.command}`,
                    },
                };
            }

            return {
                block: {
                    reason:
                        choice === DENY_ONCE
                            ? `Bash command denied by user: ${details.command}`
                            : `Bash command denied because no approval was received: ${details.command}`,
                },
            };
        },
    );
});

await runExtension(extension);

function parseBashCommandDetails(
    input: unknown,
): BashCommandDetails | undefined {
    const parsed = typeof input === "string" ? parseJsonObject(input) : input;
    if (!isRecord(parsed)) {
        return undefined;
    }

    const command =
        typeof parsed.command === "string" ? parsed.command.trim() : "";
    if (!command) {
        return undefined;
    }

    return {
        command,
        description:
            typeof parsed.description === "string" && parsed.description.trim()
                ? parsed.description.trim()
                : undefined,
        timeout:
            typeof parsed.timeout === "number" ? parsed.timeout : undefined,
    };
}

function formatBashPrompt(
    details: BashCommandDetails,
    policy: BashPolicy,
): string {
    const lines = [
        "A bash command is about to run.",
        "",
        "Command:",
        details.command,
    ];

    if (details.description) {
        lines.push("", `Description: ${details.description}`);
    }
    if (details.timeout !== undefined) {
        lines.push(`Timeout: ${details.timeout}s`);
    }

    lines.push(
        "",
        `Remembered decisions: ${policy.allowed.length} allowed, ${policy.denied.length} denied.`,
    );
    return lines.join("\n");
}

async function readBashPolicy(ctx: EventContext): Promise<BashPolicy> {
    try {
        return normalizeBashPolicy(
            await ctx.storage.readJson(BASH_POLICY_FILE),
        );
    } catch (error) {
        ctx.log.warn("failed to read bash policy; using empty policy", {
            error: errorMessage(error),
        });
        return { allowed: [], denied: [] };
    }
}

async function rememberBashDecision(
    ctx: EventContext,
    command: string,
    decision: "allowed" | "denied",
    currentPolicy: BashPolicy,
): Promise<void> {
    const nextPolicy: BashPolicy =
        decision === "allowed"
            ? {
                  allowed: uniqueStrings([...currentPolicy.allowed, command]),
                  denied: currentPolicy.denied.filter(
                      (entry) => entry !== command,
                  ),
              }
            : {
                  allowed: currentPolicy.allowed.filter(
                      (entry) => entry !== command,
                  ),
                  denied: uniqueStrings([...currentPolicy.denied, command]),
              };

    try {
        await ctx.storage.writeJson(BASH_POLICY_FILE, nextPolicy);
    } catch (error) {
        ctx.log.warn("failed to persist bash policy decision", {
            command,
            decision,
            error: errorMessage(error),
        });
    }
}

function normalizeBashPolicy(value: unknown): BashPolicy {
    if (!isRecord(value)) {
        return { allowed: [], denied: [] };
    }
    return {
        allowed: uniqueStrings(value.allowed),
        denied: uniqueStrings(value.denied),
    };
}

function uniqueStrings(value: unknown): string[] {
    if (!Array.isArray(value)) {
        return [];
    }
    return [
        ...new Set(
            value
                .filter((entry): entry is string => typeof entry === "string")
                .map((entry) => entry.trim())
                .filter(Boolean),
        ),
    ].sort();
}

function parseJsonObject(value: string): unknown {
    try {
        return JSON.parse(value);
    } catch {
        return undefined;
    }
}

function isRecord(value: unknown): value is Record<string, unknown> {
    return typeof value === "object" && value !== null && !Array.isArray(value);
}

function errorMessage(error: unknown): string {
    return error instanceof Error ? error.message : String(error);
}
