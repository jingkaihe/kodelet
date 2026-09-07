import { Client, defineExtension, z } from "kodelet";

if (process.argv.includes("--help")) {
  console.log(`Usage: npm --prefix sdk run example:inline -- [message]

Optional environment: KODELET_BIN, KODELET_SERVER, KODELET_RUNNER,
KODELET_CWD, KODELET_PROFILE. Configure model credentials on the daemon.
Press Ctrl+C to cancel the current run and close the session.`);
} else {
  // Captured state lives in this process, not on the daemon or runner.
  let calls = 0;
  const echo = defineExtension((ext) => {
    ext.registerTool({
      name: "sdk_echo",
      description: "Echo text with a counter held in the TypeScript application",
      inputSchema: z.object({ text: z.string() }),
      async execute(input, ctx) {
        ctx.signal.throwIfAborted();
        const call = ++calls;
        await ctx.update(`Echo callback ${call} is running`);
        await ctx.ui.notify({ message: `Callback ${call}: ${input.text}` });
        return `closure:${call}: ${input.text}`;
      },
    });
  });

  const client = new Client({
    command: process.env.KODELET_BIN,
    server: process.env.KODELET_SERVER,
    runner: process.env.KODELET_RUNNER,
    cwd: process.env.KODELET_CWD ?? process.cwd(),
  });
  const controller = new AbortController();
  const cancel = () => controller.abort();
  process.once("SIGINT", cancel);
  try {
    const session = await client.createSession({
      profile: process.env.KODELET_PROFILE,
      extensions: [echo],
      ui: {
        notify: (request, signal) => {
          signal?.throwIfAborted();
          console.error(`[SDK UI] ${request.message}`);
        },
      },
    });
    console.error(`Session: ${session.id}`);
    session.on("tool.update", (event) => console.error(event.data.result));
    const response = await session.runAndWait({
      message: process.argv.slice(2).join(" ") || "Use sdk_echo twice to echo hello, then summarize the results.",
      signal: controller.signal,
    });
    console.log(response.content);
    console.error(`Callbacks executed: ${calls}`);
  } finally {
    process.off("SIGINT", cancel);
    // Closing the client also closes its sessions and callback runtimes.
    await client.close();
  }
}
