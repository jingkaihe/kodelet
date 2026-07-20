import { Client } from "kodelet";
import type {
    AgentStreamEvent,
    Session,
    ToolCallData,
    ToolUpdateData,
    ToolResultData,
} from "kodelet";

const DEFAULT_MESSAGE =
    "Explain how to use the Kodelet TypeScript SDK in two bullet points.";

function finishResponse(responseContent: string): void {
    if (!responseContent) {
        console.log("(no assistant content)");
        return;
    }
    if (!responseContent.endsWith("\n")) {
        console.log();
    }
}

function installStreamHandlers(session: Session): () => void {
    let thinkingOpen = false;
    let thinkingEndsWithNewline = true;

    const finishThinking = (_event?: AgentStreamEvent): void => {
        if (!thinkingOpen) {
            return;
        }
        if (!thinkingEndsWithNewline) {
            console.error();
        }
        console.error();
        thinkingOpen = false;
        thinkingEndsWithNewline = true;
    };

    const startThinking = (_event?: AgentStreamEvent): void => {
        finishThinking();
        console.error("[thinking]");
        thinkingOpen = true;
    };

    const writeThinking = (
        event: AgentStreamEvent<{ deltaContent: string }>,
    ): void => {
        if (!thinkingOpen) {
            startThinking();
        }
        const text = event.data.deltaContent;
        process.stderr.write(text);
        thinkingEndsWithNewline = text.endsWith("\n");
    };

    const writeAnswer = (
        event: AgentStreamEvent<{ deltaContent: string }>,
    ): void => {
        finishThinking();
        process.stdout.write(event.data.deltaContent);
    };

    const writeJSONEvent = (
        event: AgentStreamEvent<ToolCallData | ToolUpdateData | ToolResultData>,
    ): void => {
        finishThinking();
        console.error(
            `[tool] ${JSON.stringify({ type: event.type, ...event.data })}`,
        );
    };

    session.on("assistant.thinking_start", startThinking);
    session.on("assistant.thinking_delta", writeThinking);
    session.on("assistant.thinking_end", finishThinking);
    session.on("assistant.message_delta", writeAnswer);
    session.on("tool.call", writeJSONEvent);
    session.on("tool.update", writeJSONEvent);
    session.on("tool.result", writeJSONEvent);
    return finishThinking;
}

const message = process.argv.slice(2).join(" ").trim() || DEFAULT_MESSAGE;
const command = process.env.KODELET_BIN || "kodelet";
const profile = process.env.KODELET_PROFILE || undefined;
const client = new Client({ command, cwd: process.cwd() });

try {
    const session = await client.createSession({ profile, streaming: true });
    const finishThinking = installStreamHandlers(session);

    const response = await session.runAndWait({ message });
    finishThinking();
    finishResponse(response.content);
    console.log(`Conversation: ${response.conversationId ?? session.id}`);
} finally {
    await client.close();
}
