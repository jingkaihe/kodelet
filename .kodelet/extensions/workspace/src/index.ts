import { defineExtension, z } from "@jingkaihe/kodelet";

const SummaryInput = z.object({
  maxFiles: z.coerce.number().int().min(1).max(50).default(12).describe("Maximum number of top-level files to include"),
});

const OpenInput = z.object({
  path: z.string().default(".").describe("Project-relative path to open"),
});

export default defineExtension((ext) => {
  ext.setMetadata({ name: "workspace", version: "0.1.0" });

  ext.registerTool({
    name: "workspace_summary",
    description: "Summarize the current workspace with git branch, status, and top-level files",
    inputSchema: SummaryInput,
    async execute(input, ctx) {
      const [branch, status, entries] = await Promise.all([
        ctx.process.exec("git", ["branch", "--show-current"], { timeoutMs: 2_000 }),
        ctx.process.exec("git", ["status", "--short"], { timeoutMs: 2_000 }),
        ctx.fs.list("."),
      ]);

      const visibleEntries = entries
        .filter((entry) => !entry.name.startsWith("."))
        .slice(0, input.maxFiles)
        .map((entry) => `${entry.type === "dir" ? "dir " : "file"} ${entry.name}`);
      const changedFiles = status.stdout
        .split("\n")
        .map((line) => line.trim())
        .filter(Boolean);

      return {
        content: [
          `Workspace: ${ctx.cwd}`,
          `Branch: ${branch.stdout.trim() || "unknown"}`,
          `Changed files: ${changedFiles.length}`,
          "Top-level entries:",
          ...visibleEntries,
        ].join("\n"),
        data: {
          cwd: ctx.cwd,
          branch: branch.stdout.trim() || undefined,
          changedFiles,
          entries: visibleEntries,
        },
      };
    },
  });

  ext.registerCommand({
    name: "open",
    aliases: ["/open"],
    description: "Open the current project or a project-relative path in the configured editor",
    inputSchema: OpenInput,
    async execute(input, ctx) {
      const target = ctx.path.resolveWorkspacePath(input.path);
      if (!(await ctx.fs.exists(target))) {
        return {
          action: "respond",
          response: `Cannot open ${ctx.path.relativeToWorkspace(target)} because it does not exist.`,
        };
      }

      const editor = ctx.env.get("EDITOR") ?? ctx.env.get("VISUAL") ?? "code";
      const editorLookup = await ctx.process.exec("sh", ["-lc", `command -v ${JSON.stringify(editor)}`], { timeoutMs: 1_000 });
      if (editorLookup.exitCode !== 0 || editorLookup.stdout.trim() === "") {
        return {
          action: "respond",
          response: `Set EDITOR or install ${editor} to open ${ctx.path.relativeToWorkspace(target)}.`,
        };
      }

      await ctx.process.spawn(editor, [target], { detach: true });
      ctx.log.info("opened path in editor", { editor, target });
      return {
        action: "respond",
        response: `Opened ${ctx.path.relativeToWorkspace(target)} in ${editor}.`,
      };
    },
  });
});
