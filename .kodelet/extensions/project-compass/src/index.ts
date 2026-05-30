import { defineExtension, z } from "@jingkaihe/kodelet";

const SummarizeInput = z.object({
  maxFiles: z.coerce.number().int().min(1).max(50).default(12).describe("Maximum number of top-level files to include"),
});

const NoteInput = z.object({
  text: z.string().optional().describe("Optional note text to append after the generated project snapshot"),
});

export default defineExtension((ext) => {
  ext.setMetadata({ name: "project-compass", version: "0.1.0" });

  ext.registerTool({
    name: "project_summary",
    description: "Summarize the current workspace using top-level files and git branch information",
    inputSchema: SummarizeInput,
    async execute(input, ctx) {
      const entries = await ctx.fs.list(".");
      const visible = entries
        .filter((entry) => !entry.name.startsWith("."))
        .slice(0, input.maxFiles)
        .map((entry) => `${entry.type === "dir" ? "dir " : "file"} ${entry.name}`);

      const branch = await ctx.process.exec("git", ["branch", "--show-current"], { timeoutMs: 2_000 });
      return {
        content: [`Workspace: ${ctx.cwd}`, `Branch: ${branch.stdout.trim() || "unknown"}`, "Entries:", ...visible].join("\n"),
        data: { cwd: ctx.cwd, branch: branch.stdout.trim() || undefined, entries: visible },
      };
    },
  });

  ext.registerCommand({
    name: "project-note",
    aliases: ["/project-note"],
    description: "Create a project snapshot note in this extension's private storage",
    inputSchema: NoteInput,
    async execute(input, ctx) {
      const entries = await ctx.fs.list(".");
      const note = [
        `# Project note for ${ctx.path.relativeToWorkspace(ctx.cwd)}`,
        "",
        `Created by /${ctx.input.commandName}`,
        "",
        "## Top-level entries",
        ...entries.slice(0, 20).map((entry) => `- ${entry.name} (${entry.type})`),
        ...(input.text ? ["", "## Note", input.text] : []),
      ].join("\n");

      await ctx.storage.writeText("project-note.md", `${note}\n`);
      return {
        action: "respond",
        response: `Wrote ${ctx.storage.dataDir}/project-note.md`,
      };
    },
  });
});
