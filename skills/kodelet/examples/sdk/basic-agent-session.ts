import { Client } from "kodelet";

const DEFAULT_MESSAGE =
    "What is the meaning of life? Answer in one short paragraph.";

const message = process.argv.slice(2).join(" ").trim() || DEFAULT_MESSAGE;
const command = process.env.KODELET_BIN || "kodelet";
const profile = process.env.KODELET_PROFILE || undefined;
const client = new Client({ command, cwd: process.cwd() });

try {
    const session = await client.createSession({ profile });
    const response = await session.runAndWait({ message });
    console.log(response.content);
    console.error(`\nConversation: ${response.conversationId ?? session.id}`);
} finally {
    await client.close();
}
