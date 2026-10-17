import type {
  ChatAssistantBlock,
  ChatRenderMessage,
  ChatRenderToolCall,
  ChatStreamEvent,
  CompactionMarker,
  ContentBlock,
  Conversation,
  Message,
  RealtimeEvent,
} from '../../types';

const hasRenderableContent = (content: string | ContentBlock[] | undefined): boolean => {
  if (typeof content === 'string') {
    return content.trim().length > 0;
  }

  if (Array.isArray(content)) {
    return content.length > 0;
  }

  return false;
};

const ensureAssistantBlocks = (message: ChatRenderMessage): ChatAssistantBlock[] => {
  if (!message.blocks) {
    message.blocks = [];
  }
  return message.blocks;
};

export const lastAgentMessageIndex = (messages: ChatRenderMessage[]): number => {
  for (let index = messages.length - 1; index >= 0; index -= 1) {
    if (!messages[index].id) return index;
  }
  return -1;
};

const ensureCurrentAssistantMessage = (
  messages: ChatRenderMessage[],
  forceNew = false
): ChatRenderMessage => {
  // Projected captions have stable IDs. Coding deltas must continue their own
  // block even when a spoken utterance arrives between two deltas.
  const lastIndex = lastAgentMessageIndex(messages);
  const lastMessage = messages[lastIndex];
  if (!lastMessage || lastMessage.role !== 'assistant' || forceNew) {
    const nextMessage: ChatRenderMessage = {
      role: 'assistant',
      blocks: [],
    };
    messages.push(nextMessage);
    return nextMessage;
  }

  // Copy the path being edited, not completed history or tool result payloads.
  const nextMessage = { ...lastMessage, blocks: [...(lastMessage.blocks || [])] };
  messages[lastIndex] = nextMessage;
  return nextMessage;
};

const insertCompactionMarker = (
  messages: ChatRenderMessage[],
  compaction: CompactionMarker,
  beforeCurrentUser = false
) => {
  if (
    messages.some((message) =>
      message.blocks?.some(
        (block) => block.type === 'compaction' && block.compaction.id === compaction.id
      )
    )
  ) {
    return;
  }

  let insertionIndex = messages.length;
  if (beforeCurrentUser) {
    // Responses pre-turn compaction precedes the already submitted user message,
    // even when assistant output has since been appended to the transcript.
    for (let i = messages.length - 1; i >= 0; i -= 1) {
      if (messages[i].role === 'user') {
        insertionIndex = i;
        break;
      }
    }
  }

  const block: ChatAssistantBlock = { type: 'compaction', compaction: { ...compaction } };
  const previousMessage = messages[insertionIndex - 1];
  if (previousMessage?.role === 'assistant') {
    messages[insertionIndex - 1] = {
      ...previousMessage,
      blocks: [...(previousMessage.blocks || []), block],
    };
  } else {
    messages.splice(insertionIndex, 0, { role: 'assistant', blocks: [block] });
  }
};

const insertResourceReloadMarker = (
  messages: ChatRenderMessage[],
  id: string | undefined,
  content: unknown,
  refused: boolean
) => {
  if (
    !id?.trim() ||
    messages.some((message) =>
      message.blocks?.some((block) => block.type === 'resources-reloaded' && block.id === id)
    )
  ) {
    return;
  }
  const text = typeof content === 'string' ? content.trim() : '';
  ensureAssistantBlocks(ensureCurrentAssistantMessage(messages)).push({
    type: 'resources-reloaded',
    id,
    ...(text ? { text } : {}),
    ...(refused ? { refused } : {}),
  });
};

const appendAssistantBlocks = (target: ChatRenderMessage, blocks: ChatAssistantBlock[]) => {
  const targetBlocks = ensureAssistantBlocks(target);
  const ends =
    target.codingMessageEnds ||
    (targetBlocks.some((block) => block.type !== 'compaction') ? [targetBlocks.length] : []);
  targetBlocks.push(...blocks);
  if (ends.length) target.codingMessageEnds = [...ends, targetBlocks.length];
};

const findMatchingBlock = (
  message: ChatRenderMessage | undefined,
  type: 'thinking' | 'message',
  content: string
) => {
  if (message?.role !== 'assistant' || !message.blocks) {
    return null;
  }

  for (let i = message.blocks.length - 1; i >= 0; i -= 1) {
    const block = message.blocks[i];
    if (block.type === 'compaction' || block.type === 'resources-reloaded') {
      break;
    }
    if (block.type !== type || typeof block.content !== 'string') {
      continue;
    }

    if (block.content === content) {
      const nextBlock = { ...block };
      message.blocks[i] = nextBlock;
      return nextBlock;
    }
  }

  return null;
};

const ensureToolsBlock = (message: ChatRenderMessage): ChatRenderToolCall[] => {
  const blocks = ensureAssistantBlocks(message);
  const lastBlock = blocks[blocks.length - 1];
  if (lastBlock?.type === 'tools') {
    const nextBlock = { ...lastBlock, tools: [...lastBlock.tools] };
    blocks[blocks.length - 1] = nextBlock;
    return nextBlock.tools;
  }

  const toolsBlock: ChatAssistantBlock = {
    type: 'tools',
    tools: [],
  };
  blocks.push(toolsBlock);
  return toolsBlock.tools;
};

const setMostRecentBlockProgress = (
  messages: ChatRenderMessage[],
  type: 'thinking' | 'message',
  inProgress: boolean
) => {
  const messageIndex = lastAgentMessageIndex(messages);
  const message = messages[messageIndex];
  if (!message?.blocks) {
    return;
  }

  for (let i = message.blocks.length - 1; i >= 0; i -= 1) {
    const block = message.blocks[i];
    if (block.type === type) {
      const blocks = [...message.blocks];
      blocks[i] = { ...block, inProgress };
      messages[messageIndex] = { ...message, blocks };
      return;
    }
  }
};

const applyToolResultEvent = (
  messages: ChatRenderMessage[],
  event: ChatStreamEvent,
  inProgress: boolean
): ChatRenderMessage[] => {
  for (let i = messages.length - 1; i >= 0; i -= 1) {
    const message = messages[i];
    if (message.role !== 'assistant' || !message.blocks) {
      continue;
    }

    for (let j = message.blocks.length - 1; j >= 0; j -= 1) {
      const block = message.blocks[j];
      if (block.type !== 'tools') {
        continue;
      }

      for (let k = block.tools.length - 1; k >= 0; k -= 1) {
        const tool = block.tools[k];
        if (tool.callId !== event.tool_call_id) {
          continue;
        }

        const nextTool = {
          ...tool,
          result: event.tool_result,
          name: event.tool_result?.toolName || event.tool_name || tool.name,
        };
        if (inProgress) {
          nextTool.inProgress = true;
        } else {
          delete nextTool.inProgress;
        }
        const tools = [...block.tools];
        tools[k] = nextTool;
        const blocks = [...message.blocks];
        blocks[j] = { ...block, tools };
        messages[i] = { ...message, blocks };
        return messages;
      }
    }
  }

  const assistantMessage = ensureCurrentAssistantMessage(messages);
  const tools = ensureToolsBlock(assistantMessage);
  const metadata = event.tool_result?.metadata;
  const metadataRecord =
    metadata && typeof metadata === 'object' ? (metadata as Record<string, unknown>) : undefined;
  const command =
    metadataRecord && typeof metadataRecord.command === 'string'
      ? metadataRecord.command
      : undefined;
  const tool: ChatRenderToolCall = {
    callId: event.tool_call_id || '',
    name: event.tool_result?.toolName || event.tool_name || 'unknown',
    input: event.input || (command ? JSON.stringify({ command }) : '{}'),
    result: event.tool_result,
  };
  if (inProgress) {
    tool.inProgress = true;
  }
  tools.push(tool);

  return messages;
};

const getThinkingTexts = (message: Message): string[] => {
  const texts = Array.isArray(message.thinkingTexts) ? message.thinkingTexts : [];
  const normalizedTexts = texts
    .filter((text): text is string => typeof text === 'string')
    .map((text) => text.trim())
    .filter(Boolean);

  if (normalizedTexts.length > 0) {
    return normalizedTexts;
  }

  const legacyText = message.thinkingText?.trim();
  return legacyText ? [legacyText] : [];
};

export const conversationToChatMessages = (
  conversation: Conversation | null
): ChatRenderMessage[] => {
  if (!conversation?.messages) {
    return [];
  }

  return conversation.messages.reduce<ChatRenderMessage[]>((chatMessages, message: Message) => {
    if (message.kind === 'resources-reloaded' || message.kind === 'resource-reload-refused') {
      insertResourceReloadMarker(
        chatMessages,
        message.resourceReloadId,
        message.content,
        message.kind === 'resource-reload-refused'
      );
      return chatMessages;
    }
    if (message.kind === 'context-compacted') {
      if (message.compaction) {
        insertCompactionMarker(chatMessages, message.compaction);
      }
      return chatMessages;
    }

    if (message.role === 'user') {
      chatMessages.push({
        ...(message.id ? { id: message.id, anchor: message.anchor } : {}),
        role: 'user',
        content: message.content,
      });
      return chatMessages;
    }

    const blocks: ChatAssistantBlock[] = [];
    getThinkingTexts(message).forEach((thinkingText) => {
      blocks.push({
        type: 'thinking',
        content: thinkingText,
        inProgress: false,
      });
    });

    const toolCalls = message.toolCalls || message.tool_calls || [];
    if (toolCalls.length > 0) {
      blocks.push({
        type: 'tools',
        tools: toolCalls.map((toolCall) => ({
          callId: toolCall.id,
          // Subscription providers may capitalize names in stored tool calls.
          // The structured result identifies the tool that actually executed.
          name:
            conversation.toolResults?.[toolCall.id]?.toolName ||
            toolCall.function?.name ||
            'unknown',
          input: toolCall.function?.arguments || '{}',
          result: conversation.toolResults?.[toolCall.id],
        })),
      });
    }

    if (hasRenderableContent(message.content)) {
      blocks.push({
        type: 'message',
        content: message.content,
        inProgress: false,
      });
    }

    if (blocks.length === 0) {
      return chatMessages;
    }

    const previousMessage = chatMessages[chatMessages.length - 1];
    if (previousMessage?.role === 'assistant' && !previousMessage.id && !message.id) {
      appendAssistantBlocks(previousMessage, blocks);
      return chatMessages;
    }

    chatMessages.push({
      ...(message.id ? { id: message.id, anchor: message.anchor } : {}),
      role: 'assistant',
      blocks,
    });
    return chatMessages;
  }, []);
};

export const applyRealtimeTranscript = (
  messages: ChatRenderMessage[],
  event: RealtimeEvent
): ChatRenderMessage[] => {
  if (event.kind !== 'transcript' || !event.id || !event.role || event.text === undefined)
    return messages;
  const index = messages.findIndex((message) => message.id === event.id);
  const anchor = index < 0 ? event.anchor : messages[index].anchor;
  const message: ChatRenderMessage =
    event.role === 'user'
      ? { id: event.id, anchor, role: 'user', content: event.text }
      : {
          id: event.id,
          anchor,
          role: 'assistant',
          blocks: [{ type: 'message', content: event.text, inProgress: !event.final }],
        };
  const next = [...messages];
  if (index >= 0) {
    next[index] = message;
    return next;
  }
  let remaining = anchor;
  if (anchor !== undefined && remaining !== undefined) {
    for (let i = 0; i < next.length; i += 1) {
      const current = next[i];
      if (current.id) {
        // Equal anchors keep first-arrival order; later updates never move them.
        if (current.anchor === undefined || current.anchor <= anchor) continue;
      } else {
        const blocks = current.blocks || [];
        // Handoffs reserve an empty coding reply before its first token. Keep
        // captions at this boundary ahead of that reply, not behind its placeholder.
        if (current.role === 'assistant' && !blocks.length && remaining === 0) {
          next.splice(i, 0, message);
          return next;
        }
        const ends =
          current.codingMessageEnds ||
          (current.role === 'user' || blocks.some((block) => block.type !== 'compaction')
            ? [blocks.length]
            : []);
        if (!ends.length) continue;
        if (remaining >= ends.length) {
          remaining -= ends.length;
          continue;
        }
        if (remaining > 0) {
          const offset = ends[remaining - 1];
          next.splice(
            i,
            1,
            {
              ...current,
              blocks: blocks.slice(0, offset),
              codingMessageEnds: ends.slice(0, remaining),
            },
            message,
            {
              ...current,
              blocks: blocks.slice(offset),
              codingMessageEnds: ends.slice(remaining).map((end) => end - offset),
            }
          );
          return next;
        }
      }
      next.splice(i, 0, message);
      return next;
    }
  }
  next.push(message);
  return next;
};

export const applyChatStreamEvent = (
  messages: ChatRenderMessage[],
  event: ChatStreamEvent
): ChatRenderMessage[] => {
  const nextMessages = [...messages];

  switch (event.kind) {
    case 'conversation':
    case 'usage':
    case 'done':
    case 'error':
      return messages;

    case 'context-compacted':
      if (event.compaction) {
        insertCompactionMarker(nextMessages, event.compaction, event.before_current_user);
      }
      return nextMessages;

    case 'resources-reloaded':
    case 'resource-reload-refused':
      insertResourceReloadMarker(
        nextMessages,
        event.resource_reload_id,
        event.content,
        event.kind === 'resource-reload-refused'
      );
      return nextMessages;

    case 'thinking-start': {
      const assistantMessage = ensureCurrentAssistantMessage(nextMessages);
      const blocks = ensureAssistantBlocks(assistantMessage);
      const lastBlock = blocks[blocks.length - 1];
      if (lastBlock?.type !== 'thinking' || !lastBlock.inProgress) {
        blocks.push({
          type: 'thinking',
          content: '',
          inProgress: true,
        });
      }
      return nextMessages;
    }

    case 'thinking-delta': {
      const assistantMessage = ensureCurrentAssistantMessage(nextMessages);
      const blocks = ensureAssistantBlocks(assistantMessage);
      const lastBlock = blocks[blocks.length - 1];
      if (lastBlock?.type !== 'thinking') {
        blocks.push({
          type: 'thinking',
          content: '',
          inProgress: true,
        });
      }

      const currentBlock = blocks[blocks.length - 1];
      if (currentBlock?.type === 'thinking') {
        blocks[blocks.length - 1] = {
          ...currentBlock,
          content: currentBlock.content + (event.delta || ''),
          inProgress: true,
        };
      }
      return nextMessages;
    }

    case 'thinking-end':
      setMostRecentBlockProgress(nextMessages, 'thinking', false);
      return nextMessages;

    case 'text-delta': {
      const assistantMessage = ensureCurrentAssistantMessage(nextMessages);
      const blocks = ensureAssistantBlocks(assistantMessage);
      const lastBlock = blocks[blocks.length - 1];
      if (lastBlock?.type !== 'message') {
        blocks.push({
          type: 'message',
          content: '',
          inProgress: true,
        });
      }

      const currentBlock = blocks[blocks.length - 1];
      if (currentBlock?.type === 'message' && typeof currentBlock.content === 'string') {
        blocks[blocks.length - 1] = {
          ...currentBlock,
          content: currentBlock.content + (event.delta || ''),
          inProgress: true,
        };
      }
      return nextMessages;
    }

    case 'content-end':
      setMostRecentBlockProgress(nextMessages, 'message', false);
      return nextMessages;

    case 'tool-use': {
      const assistantMessage = ensureCurrentAssistantMessage(nextMessages);
      const tools = ensureToolsBlock(assistantMessage);
      tools.push({
        callId: event.tool_call_id || '',
        name: event.tool_name || 'unknown',
        input: event.input || '{}',
      });
      return nextMessages;
    }

    case 'tool-update':
      return applyToolResultEvent(nextMessages, event, true);

    case 'tool-result':
      return applyToolResultEvent(nextMessages, event, false);

    case 'thinking': {
      const content = typeof event.content === 'string' ? event.content : '';
      if (!content) {
        return nextMessages;
      }

      const assistantMessage = ensureCurrentAssistantMessage(nextMessages);
      const existingBlock = findMatchingBlock(assistantMessage, 'thinking', content);
      if (existingBlock) {
        existingBlock.inProgress = false;
        return nextMessages;
      }

      ensureAssistantBlocks(assistantMessage).push({
        type: 'thinking',
        content,
        inProgress: false,
      });
      return nextMessages;
    }

    case 'text': {
      const content = typeof event.content === 'string' ? event.content : '';
      if (!content) {
        return nextMessages;
      }

      const assistantMessage = ensureCurrentAssistantMessage(nextMessages);
      const existingBlock = findMatchingBlock(assistantMessage, 'message', content);
      if (existingBlock) {
        existingBlock.inProgress = false;
        return nextMessages;
      }

      ensureAssistantBlocks(assistantMessage).push({
        type: 'message',
        content,
        inProgress: false,
      });
      return nextMessages;
    }

    case 'user-message': {
      if (event.delegatedUser) {
        // Preserve the coding turn boundary without displaying its synthesized
        // input. The original spoken user entry is projected independently.
        ensureCurrentAssistantMessage(nextMessages, true);
        return nextMessages;
      }
      if (!hasRenderableContent(event.content)) {
        return nextMessages;
      }

      nextMessages.push({
        role: 'user',
        content: event.content,
      });
      return nextMessages;
    }

    case 'user-message-display': {
      if (event.delegatedUser) return messages;
      if (!hasRenderableContent(event.content)) {
        return nextMessages;
      }

      const lastMessageIndex = lastAgentMessageIndex(nextMessages);
      if (nextMessages[lastMessageIndex]?.role === 'user') {
        nextMessages[lastMessageIndex] = {
          role: 'user',
          content: event.content,
        };
        return nextMessages;
      }

      nextMessages.push({
        role: 'user',
        content: event.content,
      });
      return nextMessages;
    }

    default:
      return messages;
  }
};
