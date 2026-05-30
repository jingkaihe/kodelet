import { defineExtension, z } from "@jingkaihe/kodelet";

const ReviewInput = z.object({
  target: z.string().default("HEAD").describe("Git revision, branch, or ref to review"),
  focus: z.string().default("correctness, simplicity, and tests").describe("Review focus areas"),
});

const RiskyCommandPattern = /\b(rm\s+-rf\s+\/|mkfs|dd\s+.*of=\/dev\/|shutdown|reboot)\b/i;

export default defineExtension((ext) => {
  ext.setMetadata({ name: "guardrails", version: "0.1.0" });

  ext.registerCommand({
    name: "guardrail-review",
    aliases: ["/guardrail-review", "/greview"],
    description: "Run a review recipe with a safety and regression focus",
    kind: "recipe",
    inputSchema: ReviewInput,
    async execute(input) {
      return {
        action: "runAgent",
        recipeName: "guardrail-review",
        prompt: [
          `Review ${input.target}.`,
          `Focus on ${input.focus}.`,
          "Call out security, data-loss, and compatibility risks first.",
          "Prefer the smallest correct fix and identify missing verification.",
        ].join("\n"),
      };
    },
  });

  ext.on("agent.init", { priority: 10 }, async () => ({
    systemPrompt: {
      append: "When making changes, explicitly preserve user worktree changes you did not create.",
    },
  }));

  ext.on("tool.call", { priority: 100 }, async (event) => {
    if (event.tool.name !== "bash") {
      return;
    }

    const input = event.tool.input as { command?: unknown };
    const command = typeof input.command === "string" ? input.command : JSON.stringify(event.tool.input);
    if (RiskyCommandPattern.test(command)) {
      return {
        block: { reason: `Guardrails blocked a potentially destructive command: ${command}` },
      };
    }
  });

  ext.on("tool.result", async (event, ctx) => {
    ctx.log.debug("tool completed", { tool: event.tool.name });
  });
});
