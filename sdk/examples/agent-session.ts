/**
 * Local smoke-test for the Kodelet TypeScript Agent SDK.
 *
 * From ./sdk, run:
 *   npm run example:agent -- "Use sdk_echo with text hello"
 *
 * Build the local binary first with `mise run build-dev`. If ../bin/kodelet is
 * not present, this falls back to `kodelet` from PATH. Set KODELET_BIN to force
 * a specific binary or KODELET_PROFILE to test a named profile, for example:
 *   KODELET_PROFILE=openai npm run example:agent
 */
import { existsSync } from "node:fs";
import path from "node:path";
import { fileURLToPath } from "node:url";

import { Client, defineExtension, z } from "../src/index.js";

const DEFAULT_MESSAGE =
  "Use the sdk_echo tool with text 'hello from the TypeScript SDK example', then summarize the tool result in one sentence.";

const message = process.argv.slice(2).join(" ").trim() || DEFAULT_MESSAGE;
const command = process.env.KODELET_BIN || defaultKodeletBinary();
const namedProfile = process.env.KODELET_PROFILE || undefined;
const profile = namedProfile ?? { allowed_tools: ["sdk_echo"] };

const echoExtension = defineExtension((ext) => {
  ext.setMetadata({ name: "sdk-agent-example", version: "0.1.0" });

  ext.registerTool({
    name: "sdk_echo",
    description: "Echo input text back to test the TypeScript Agent SDK inline extension bridge.",
    inputSchema: z.object({
      text: z.string().min(1).describe("Text to echo back."),
    }),
    async execute(input, ctx) {
      await ctx.ui.notify({
        title: "sdk_echo called",
        message: input.text,
      });
      return `sdk_echo received: ${input.text}`;
    },
  });
});

const client = new Client({ command, cwd: process.cwd() });

try {
  const session = await client.createSession({
    profile,
    extensions: [echoExtension],
    streaming: true,
  });

  session.on("assistant.message_delta", (event) => {
    process.stdout.write(event.data.deltaContent);
  });
  session.on("assistant.thinking_delta", (event) => {
    process.stderr.write(event.data.deltaContent);
  });

  const response = await session.runAndWait({ message });
  process.stdout.write(`\n\nConversation: ${response.conversationId ?? session.id}\n`);
} finally {
  await client.close();
}

function defaultKodeletBinary(): string {
  const exampleDir = path.dirname(fileURLToPath(import.meta.url));
  const repoBinary = path.resolve(exampleDir, "..", "..", "bin", "kodelet");
  return existsSync(repoBinary) ? repoBinary : "kodelet";
}
