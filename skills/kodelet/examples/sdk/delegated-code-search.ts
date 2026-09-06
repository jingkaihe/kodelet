import { defineExtension, z } from "kodelet";
import { runExtension } from "kodelet/runtime";

// Run from a kodelet-extension-* executable installed on the selected runner.
// The daemon needs provider credentials, but no named code_search profile.
await runExtension(defineExtension((ext) => {
    ext.registerProfile({
        name: "code_search",
        systemPrompt: "Search the repository and explain relevant code with file locations. Do not modify files.",
        // Alternatively: systemPromptPath: "search-prompt.md" (runner-local).
        options: {
            model: "gpt-4o-mini",
            allowedTools: ["file_read", "grep_tool", "glob_tool"],
            noExtensions: true,
            noSkills: true,
            enableFSSearchTools: true,
            maxTurns: 3,
        },
    });
    ext.registerTool({
        name: "code_search",
        description: "Search the repository with a read-only delegated agent.",
        inputSchema: z.object({ query: z.string().min(1) }),
        async execute(input, ctx) {
            const child = await ctx.children.start({ profile: "code_search", message: input.query });
            const result = await child.wait({
                signal: ctx.signal,
                onEvent: (event) => ctx.update(event.text ?? event.kind),
            });
            return result.output ?? "No matches found.";
        },
    });
}));
