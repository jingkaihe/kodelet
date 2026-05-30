import { readFile } from "node:fs/promises";
import path from "node:path";
import { fileURLToPath } from "node:url";

import { defineExtension, renderTemplate, z } from "@jingkaihe/kodelet";

const extensionDir = path.resolve(path.dirname(fileURLToPath(import.meta.url)), "..");
const promptPath = path.join(extensionDir, "reviewer-prompt.md");

const ReviewChangesInput = z.object({
  target: z.string().default("HEAD").describe("Git ref or branch to compare against"),
  focus: z.string().default("correctness, tests, and maintainability").describe("What the review should emphasize"),
});

export default defineExtension((ext) => {
  ext.setMetadata({ name: "review", version: "0.1.0" });

  ext.registerCommand({
    name: "review-changes",
    aliases: ["/review-changes", "/changes"],
    description: "Review local git changes against a target ref",
    kind: "recipe",
    inputSchema: ReviewChangesInput,
    async execute(input, ctx) {
      const status = await ctx.process.exec("git", ["status", "--short"], { timeoutMs: 2_000 });
      const diff = await ctx.process.exec("git", ["diff", "--stat", input.target], { timeoutMs: 5_000 });
      const promptTemplate = await readFile(promptPath, "utf8");

      return {
        action: "runAgent",
        recipeName: "review-changes",
        prompt: renderTemplate(promptTemplate, {
          target: input.target,
          focus: input.focus,
          gitStatus: status.stdout.trim() || "No changes reported by git status --short.",
          diffStat: diff.stdout.trim() || `No diff stat against ${input.target}.`,
        }),
      };
    },
  });
});
