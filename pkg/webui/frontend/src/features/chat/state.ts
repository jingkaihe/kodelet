import type {
  ChatAssistantBlock,
  ChatRenderMessage,
  ChatRenderToolCall,
  ChatStreamEvent,
  ContentBlock,
  Conversation,
  Message,
} from '../../types';

const cloneMessages = (messages: ChatRenderMessage[]): ChatRenderMessage[] =>
  JSON.parse(JSON.stringify(messages)) as ChatRenderMessage[];

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

const ensureCurrentAssistantMessage = (
  messages: ChatRenderMessage[],
  forceNew = false
): ChatRenderMessage => {
  const lastMessage = messages[messages.length - 1];
  if (!lastMessage || lastMessage.role !== 'assistant' || forceNew) {
    const nextMessage: ChatRenderMessage = {
      role: 'assistant',
      blocks: [],
    };
    messages.push(nextMessage);
    return nextMessage;
  }

  ensureAssistantBlocks(lastMessage);
  return lastMessage;
};

const appendAssistantBlocks = (
  target: ChatRenderMessage,
  blocks: ChatAssistantBlock[]
) => {
  const targetBlocks = ensureAssistantBlocks(target);

  blocks.forEach((block) => {
    if (block.type === 'tools') {
      const lastBlock = targetBlocks[targetBlocks.length - 1];
      if (lastBlock?.type === 'tools') {
        lastBlock.tools.push(...block.tools);
      } else {
        targetBlocks.push({
          type: 'tools',
          tools: [...block.tools],
        });
      }
      return;
    }

    targetBlocks.push({ ...block });
  });
};

const findMatchingBlock = (
  messages: ChatRenderMessage[],
  type: 'thinking' | 'message',
  content: string
) => {
  for (let i = messages.length - 1; i >= 0; i -= 1) {
    const message = messages[i];
    if (message.role !== 'assistant' || !message.blocks) {
      continue;
    }

    for (let j = message.blocks.length - 1; j >= 0; j -= 1) {
      const block = message.blocks[j];
      if (block.type !== type || typeof block.content !== 'string') {
        continue;
      }

      if (block.content === content) {
        return block;
      }
    }
  }

  return null;
};

const ensureToolsBlock = (message: ChatRenderMessage): ChatRenderToolCall[] => {
  const blocks = ensureAssistantBlocks(message);
  const lastBlock = blocks[blocks.length - 1];
  if (lastBlock?.type === 'tools') {
    return lastBlock.tools;
  }

  const toolsBlock: ChatAssistantBlock = {
    type: 'tools',
    tools: [],
  };
  blocks.push(toolsBlock);
  return toolsBlock.tools;
};

const setMostRecentBlockProgress = (
  message: ChatRenderMessage | undefined,
  type: 'thinking' | 'message',
  inProgress: boolean
) => {
  if (!message?.blocks) {
    return;
  }

  for (let i = message.blocks.length - 1; i >= 0; i -= 1) {
    const block = message.blocks[i];
    if (block.type === type) {
      block.inProgress = inProgress;
      return;
    }
  }
};

export const conversationToChatMessages = (
  conversation: Conversation | null
): ChatRenderMessage[] => {
  if (!conversation?.messages) {
    return [];
  }

  return conversation.messages.reduce<ChatRenderMessage[]>((chatMessages, message: Message) => {
    if (message.role === 'user') {
      chatMessages.push({
        role: 'user',
        content: message.content,
      });
      return chatMessages;
    }

    const blocks: ChatAssistantBlock[] = [];
    if (message.thinkingText?.trim()) {
      blocks.push({
        type: 'thinking',
        content: message.thinkingText,
        inProgress: false,
      });
    }

    const toolCalls = message.toolCalls || message.tool_calls || [];
    if (toolCalls.length > 0) {
      blocks.push({
        type: 'tools',
        tools: toolCalls.map((toolCall) => ({
          callId: toolCall.id,
          name: toolCall.function?.name || 'unknown',
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
    if (previousMessage?.role === 'assistant') {
      appendAssistantBlocks(previousMessage, blocks);
      return chatMessages;
    }

    chatMessages.push({
      role: 'assistant',
      blocks,
    });
    return chatMessages;
  }, []);
};

export const applyChatStreamEvent = (
  messages: ChatRenderMessage[],
  event: ChatStreamEvent
): ChatRenderMessage[] => {
  const nextMessages = cloneMessages(messages);

  switch (event.kind) {
    case 'conversation':
    case 'done':
    case 'error':
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
        currentBlock.content += event.delta || '';
        currentBlock.inProgress = true;
      }
      return nextMessages;
    }

    case 'thinking-end':
      setMostRecentBlockProgress(nextMessages[nextMessages.length - 1], 'thinking', false);
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
        currentBlock.content += event.delta || '';
        currentBlock.inProgress = true;
      }
      return nextMessages;
    }

    case 'content-end':
      setMostRecentBlockProgress(nextMessages[nextMessages.length - 1], 'message', false);
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

    case 'tool-result': {
      for (let i = nextMessages.length - 1; i >= 0; i -= 1) {
        const message = nextMessages[i];
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
            if (tool.callId === event.tool_call_id && !tool.result) {
              tool.result = event.tool_result;
              return nextMessages;
            }
          }
        }
      }
      return nextMessages;
    }

    case 'thinking': {
      const content = event.content || '';
      if (!content) {
        return nextMessages;
      }

      const existingBlock = findMatchingBlock(nextMessages, 'thinking', content);
      if (existingBlock) {
        existingBlock.inProgress = false;
        return nextMessages;
      }

      const assistantMessage = ensureCurrentAssistantMessage(nextMessages);
      ensureAssistantBlocks(assistantMessage).push({
        type: 'thinking',
        content,
        inProgress: false,
      });
      return nextMessages;
    }

    case 'text': {
      const content = event.content || '';
      if (!content) {
        return nextMessages;
      }

      const existingBlock = findMatchingBlock(nextMessages, 'message', content);
      if (existingBlock) {
        existingBlock.inProgress = false;
        return nextMessages;
      }

      const assistantMessage = ensureCurrentAssistantMessage(nextMessages);
      ensureAssistantBlocks(assistantMessage).push({
        type: 'message',
        content,
        inProgress: false,
      });
      return nextMessages;
    }

    default:
      return nextMessages;
  }
};
