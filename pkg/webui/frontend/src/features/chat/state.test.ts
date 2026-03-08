import { describe, expect, it } from 'vitest';
import { applyChatStreamEvent, conversationToChatMessages } from './state';
import type { ChatRenderMessage, Conversation } from '../../types';

describe('conversationToChatMessages', () => {
  it('converts assistant thinking, tool calls, and content into ordered blocks', () => {
    const conversation: Conversation = {
      id: 'conv-123',
      createdAt: '2026-03-08T00:00:00Z',
      updatedAt: '2026-03-08T00:00:00Z',
      messageCount: 2,
      messages: [
        {
          role: 'user',
          content: 'hello',
        },
        {
          role: 'assistant',
          content: 'done',
          thinkingText: 'thinking',
          toolCalls: [
            {
              id: 'tool-1',
              function: {
                name: 'bash',
                arguments: '{"command":"pwd"}',
              },
            },
          ],
        },
      ],
      toolResults: {
        'tool-1': {
          toolName: 'bash',
          success: true,
          timestamp: '2026-03-08T00:00:00Z',
        },
      },
    };

    const messages = conversationToChatMessages(conversation);

    expect(messages).toHaveLength(2);
    expect(messages[0]).toEqual({
      role: 'user',
      content: 'hello',
    });
    expect(messages[1].blocks).toEqual([
      {
        type: 'thinking',
        content: 'thinking',
        inProgress: false,
      },
      {
        type: 'tools',
        tools: [
          {
            callId: 'tool-1',
            name: 'bash',
            input: '{"command":"pwd"}',
            result: {
              toolName: 'bash',
              success: true,
              timestamp: '2026-03-08T00:00:00Z',
            },
          },
        ],
      },
      {
        type: 'message',
        content: 'done',
        inProgress: false,
      },
    ]);
  });

  it('merges consecutive assistant fragments into a single rendered reply', () => {
    const conversation: Conversation = {
      id: 'conv-merged',
      createdAt: '2026-03-08T00:00:00Z',
      updatedAt: '2026-03-08T00:00:00Z',
      messageCount: 4,
      messages: [
        {
          role: 'user',
          content: 'summarize the machine',
        },
        {
          role: 'assistant',
          content: '',
          thinkingText: 'Gathering system overview',
        },
        {
          role: 'assistant',
          content: '',
          toolCalls: [
            {
              id: 'tool-1',
              function: {
                name: 'bash',
                arguments: '{"command":"uname -a"}',
              },
            },
          ],
        },
        {
          role: 'assistant',
          content: 'Here is the summary.',
        },
      ],
      toolResults: {
        'tool-1': {
          toolName: 'bash',
          success: true,
          timestamp: '2026-03-08T00:00:00Z',
        },
      },
    };

    const messages = conversationToChatMessages(conversation);

    expect(messages).toHaveLength(2);
    expect(messages[1]).toEqual({
      role: 'assistant',
      blocks: [
        {
          type: 'thinking',
          content: 'Gathering system overview',
          inProgress: false,
        },
        {
          type: 'tools',
          tools: [
            {
              callId: 'tool-1',
              name: 'bash',
              input: '{"command":"uname -a"}',
              result: {
                toolName: 'bash',
                success: true,
                timestamp: '2026-03-08T00:00:00Z',
              },
            },
          ],
        },
        {
          type: 'message',
          content: 'Here is the summary.',
          inProgress: false,
        },
      ],
    });
  });
});

describe('applyChatStreamEvent', () => {
  it('appends repeated text deltas into the same assistant message', () => {
    let messages: ChatRenderMessage[] = [
      {
        role: 'user',
        content: 'say thanks',
      },
    ];

    messages = applyChatStreamEvent(messages, {
      kind: 'text-delta',
      delta: "You're ",
      conversation_id: 'conv-123',
      role: 'assistant',
    });
    messages = applyChatStreamEvent(messages, {
      kind: 'text-delta',
      delta: 'welcome',
      conversation_id: 'conv-123',
      role: 'assistant',
    });
    messages = applyChatStreamEvent(messages, {
      kind: 'content-end',
      conversation_id: 'conv-123',
      role: 'assistant',
    });

    expect(messages).toHaveLength(2);
    expect(messages[1].blocks).toEqual([
      {
        type: 'message',
        content: "You're welcome",
        inProgress: false,
      },
    ]);
  });

  it('builds an assistant reply from streamed deltas and tool events', () => {
    let messages: ChatRenderMessage[] = [
      {
        role: 'user',
        content: 'inspect the repo',
      },
    ];

    messages = applyChatStreamEvent(messages, {
      kind: 'thinking-start',
      conversation_id: 'conv-123',
      role: 'assistant',
    });
    messages = applyChatStreamEvent(messages, {
      kind: 'thinking-delta',
      delta: 'Looking around',
      conversation_id: 'conv-123',
      role: 'assistant',
    });
    messages = applyChatStreamEvent(messages, {
      kind: 'thinking-end',
      conversation_id: 'conv-123',
      role: 'assistant',
    });
    messages = applyChatStreamEvent(messages, {
      kind: 'tool-use',
      tool_call_id: 'tool-1',
      tool_name: 'bash',
      input: '{"command":"ls"}',
      conversation_id: 'conv-123',
      role: 'assistant',
    });
    messages = applyChatStreamEvent(messages, {
      kind: 'tool-result',
      tool_call_id: 'tool-1',
      conversation_id: 'conv-123',
      role: 'assistant',
      tool_result: {
        toolName: 'bash',
        success: true,
        timestamp: '2026-03-08T00:00:00Z',
      },
    });
    messages = applyChatStreamEvent(messages, {
      kind: 'text-delta',
      delta: 'All set.',
      conversation_id: 'conv-123',
      role: 'assistant',
    });
    messages = applyChatStreamEvent(messages, {
      kind: 'content-end',
      conversation_id: 'conv-123',
      role: 'assistant',
    });

    expect(messages).toHaveLength(2);
    expect(messages[1].role).toBe('assistant');
    expect(messages[1].blocks).toEqual([
      {
        type: 'thinking',
        content: 'Looking around',
        inProgress: false,
      },
      {
        type: 'tools',
        tools: [
          {
            callId: 'tool-1',
            name: 'bash',
            input: '{"command":"ls"}',
            result: {
              toolName: 'bash',
              success: true,
              timestamp: '2026-03-08T00:00:00Z',
            },
          },
        ],
      },
      {
        type: 'message',
        content: 'All set.',
        inProgress: false,
      },
    ]);
  });

  it('keeps later streamed text in the same assistant container after tool events', () => {
    let messages: ChatRenderMessage[] = [
      {
        role: 'user',
        content: 'inspect the machine',
      },
    ];

    messages = applyChatStreamEvent(messages, {
      kind: 'thinking',
      content: 'Gathering system overview',
      conversation_id: 'conv-456',
      role: 'assistant',
    });
    messages = applyChatStreamEvent(messages, {
      kind: 'tool-use',
      tool_call_id: 'tool-1',
      tool_name: 'bash',
      input: '{"command":"uname -a"}',
      conversation_id: 'conv-456',
      role: 'assistant',
    });
    messages = applyChatStreamEvent(messages, {
      kind: 'tool-result',
      tool_call_id: 'tool-1',
      conversation_id: 'conv-456',
      role: 'assistant',
      tool_result: {
        toolName: 'bash',
        success: true,
        timestamp: '2026-03-08T00:00:00Z',
      },
    });
    messages = applyChatStreamEvent(messages, {
      kind: 'text',
      content: 'Here is the summary.',
      conversation_id: 'conv-456',
      role: 'assistant',
    });

    expect(messages).toHaveLength(2);
    expect(messages[1]).toEqual({
      role: 'assistant',
      blocks: [
        {
          type: 'thinking',
          content: 'Gathering system overview',
          inProgress: false,
        },
        {
          type: 'tools',
          tools: [
            {
              callId: 'tool-1',
              name: 'bash',
              input: '{"command":"uname -a"}',
              result: {
                toolName: 'bash',
                success: true,
                timestamp: '2026-03-08T00:00:00Z',
              },
            },
          ],
        },
        {
          type: 'message',
          content: 'Here is the summary.',
          inProgress: false,
        },
      ],
    });
  });

  it('renders repeated non-streamed assistant replies in later turns', () => {
    let messages: ChatRenderMessage[] = [
      {
        role: 'user',
        content: 'first check',
      },
      {
        role: 'assistant',
        blocks: [
          {
            type: 'message',
            content: 'Done.',
            inProgress: false,
          },
        ],
      },
      {
        role: 'user',
        content: 'second check',
      },
    ];

    messages = applyChatStreamEvent(messages, {
      kind: 'text',
      content: 'Done.',
      conversation_id: 'conv-789',
      role: 'assistant',
    });

    expect(messages).toHaveLength(4);
    expect(messages[3]).toEqual({
      role: 'assistant',
      blocks: [
        {
          type: 'message',
          content: 'Done.',
          inProgress: false,
        },
      ],
    });
  });
});
