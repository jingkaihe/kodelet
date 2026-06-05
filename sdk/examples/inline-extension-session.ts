import { Client, defineExtension, z } from "../src/index.js";
import type { UINotifyRequest } from "../src/index.js";

const DEFAULT_MESSAGE =
    "Use the sdk_echo tool with text 'hello from the TypeScript SDK example', then summarize the tool result in one sentence.";

const echoExtension = defineExtension((ext) => {
    ext.setMetadata({ name: "sdk-agent-example", version: "0.1.0" });

    ext.registerTool({
        name: "sdk_echo",
        description:
            "Echo input text back to test the TypeScript Agent SDK inline extension bridge.",
        inputSchema: z.object({ text: z.string().min(1) }),
        async execute(input, ctx) {
            await ctx.ui.notify({
                title: "sdk_echo called",
                message: input.text,
            });
            return `sdk_echo received: ${input.text}`;
        },
    });
});

function notify(request: UINotifyRequest): void {
    console.error(`[${request.title ?? "notification"}] ${request.message}`);
}

const message = process.argv.slice(2).join(" ").trim() || DEFAULT_MESSAGE;
const command = process.env.KODELET_BIN || "kodelet";
const client = new Client({ command, cwd: process.cwd() });

try {
    const session = await client.createSession({
        extensions: [echoExtension],
        extensionTransport: "tcp",
        streaming: true,
        ui: { notify },
    });

    session.on("assistant.message_delta", (event) => {
        process.stdout.write(event.data.deltaContent);
    });

    const response = await session.runAndWait({ message });
    if (!response.content) {
        console.log("(no assistant content)");
    }
    console.log(`\n\nConversation: ${response.conversationId ?? session.id}`);
} finally {
    await client.close();
}
