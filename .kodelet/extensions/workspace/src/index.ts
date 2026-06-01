import { defineExtension, z } from "@jingkaihe/kodelet";

// const SummaryInput = z.object({
//   maxFiles: z.coerce.number().int().min(1).max(50).default(12).describe("Maximum number of top-level files to include"),
// });

// const OpenInput = z.object({
//   path: z.string().default(".").describe("Project-relative path to open"),
// });

const AskUserChoiceInput = z.object({
    question: z.string().min(1).describe("The question to ask the user"),
    options: z
        .array(z.string().min(1))
        .min(2)
        .max(5)
        .describe("The options to choose from (2-5 items)"),
});

export default defineExtension((ext) => {
    ext.setMetadata({ name: "workspace", version: "0.1.0" });

    // ext.registerTool({
    //   name: "workspace_summary",
    //   description: "Summarize the current workspace with git branch, status, and top-level files",
    //   inputSchema: SummaryInput,
    //   async execute(input, ctx) {
    //     const [branch, status, entries] = await Promise.all([
    //       ctx.process.exec("git", ["branch", "--show-current"], { timeoutMs: 2_000 }),
    //       ctx.process.exec("git", ["status", "--short"], { timeoutMs: 2_000 }),
    //       ctx.fs.list("."),
    //     ]);

    //     const visibleEntries = entries
    //       .filter((entry) => !entry.name.startsWith("."))
    //       .slice(0, input.maxFiles)
    //       .map((entry) => `${entry.type === "dir" ? "dir " : "file"} ${entry.name}`);
    //     const changedFiles = status.stdout
    //       .split("\n")
    //       .map((line) => line.trim())
    //       .filter(Boolean);

    //     return {
    //       content: [
    //         `Workspace: ${ctx.cwd}`,
    //         `Branch: ${branch.stdout.trim() || "unknown"}`,
    //         `Changed files: ${changedFiles.length}`,
    //         "Top-level entries:",
    //         ...visibleEntries,
    //       ].join("\n"),
    //       data: {
    //         cwd: ctx.cwd,
    //         branch: branch.stdout.trim() || undefined,
    //         changedFiles,
    //         entries: visibleEntries,
    //       },
    //     };
    //   },
    // });

    ext.registerTool({
        name: "ask_user_choice",
        description:
            "Present user with multiple choices when there are several possible approaches and you need them to pick one. Use when you have 2-5 concrete options to choose from",
        inputSchema: AskUserChoiceInput,
        async execute(input, ctx) {
            const optionsList = input.options
                .map((option, index) => `${index + 1}. ${option}`)
                .join("\n");
            const answer = await ctx.ui.input({
                title: input.question,
                helpText: `${optionsList}\n\nType the number of your choice`,
                submitButtonText: "Select",
            });

            if (!answer) {
                return "User dismissed the question without choosing.";
            }

            const index = parseInt(answer.trim(), 10) - 1;
            if (index >= 0 && index < input.options.length) {
                return `User selected option ${index + 1}: ${input.options[index]}`;
            }
            return `User responded with: ${answer}`;
        },
    });

    // ext.registerCommand({
    //   name: "open",
    //   aliases: ["/open"],
    //   description: "Open the current project or a project-relative path in the configured editor",
    //   inputSchema: OpenInput,
    //   async execute(input, ctx) {
    //     const target = ctx.path.resolveWorkspacePath(input.path);
    //     if (!(await ctx.fs.exists(target))) {
    //       return {
    //         action: "respond",
    //         response: `Cannot open ${ctx.path.relativeToWorkspace(target)} because it does not exist.`,
    //       };
    //     }

    //     const editor = ctx.env.get("EDITOR") ?? ctx.env.get("VISUAL") ?? "code";
    //     const editorLookup = await ctx.process.exec("sh", ["-lc", `command -v ${JSON.stringify(editor)}`], { timeoutMs: 1_000 });
    //     if (editorLookup.exitCode !== 0 || editorLookup.stdout.trim() === "") {
    //       return {
    //         action: "respond",
    //         response: `Set EDITOR or install ${editor} to open ${ctx.path.relativeToWorkspace(target)}.`,
    //       };
    //     }

    //     await ctx.process.spawn(editor, [target], { detach: true });
    //     ctx.log.info("opened path in editor", { editor, target });
    //     return {
    //       action: "respond",
    //       response: `Opened ${ctx.path.relativeToWorkspace(target)} in ${editor}.`,
    //     };
    //   },
    // });
});
