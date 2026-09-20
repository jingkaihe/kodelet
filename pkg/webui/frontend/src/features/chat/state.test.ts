import { describe, expect, it } from 'vitest';
import type { ChatRenderMessage, CompactionMarker, Conversation, Message } from '../../types';
import { applyChatStreamEvent, conversationToChatMessages } from './state';

describe('conversationToChatMessages', () => {
  it.each([
    'CustomTool',
    undefined,
  ])('preserves extension name casing with result name %s', (resultName) => {
    const conversation: Conversation = {
      id: 'subscription-history',
      createdAt: '',
      updatedAt: '',
      messageCount: 1,
      messages: [
        {
          role: 'assistant',
          content: '',
          toolCalls: [{ id: 'tool-1', function: { name: 'CustomTool', arguments: '{}' } }],
        },
      ],
      toolResults: resultName ? { 'tool-1': { toolName: resultName, success: true } } : undefined,
    };

    expect(conversationToChatMessages(conversation)[0].blocks?.[0]).toMatchObject({
      type: 'tools',
      tools: [{ name: 'CustomTool' }],
    });
    expect(conversation.messages?.[0].toolCalls?.[0].function.name).toBe('CustomTool');
  });

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

  it('preserves multiple persisted thinking texts as separate adjacent blocks', () => {
    const conversation: Conversation = {
      id: 'conv-thoughts',
      createdAt: '2026-03-08T00:00:00Z',
      updatedAt: '2026-03-08T00:00:00Z',
      messageCount: 2,
      messages: [
        {
          role: 'user',
          content: 'think through this',
        },
        {
          role: 'assistant',
          content: 'Done.',
          thinkingText: 'legacy combined thought',
          thinkingTexts: ['First thought', 'Second thought', 'Third thought'],
        },
      ],
    };

    const messages = conversationToChatMessages(conversation);

    expect(messages[1].blocks).toEqual([
      {
        type: 'thinking',
        content: 'First thought',
        inProgress: false,
      },
      {
        type: 'thinking',
        content: 'Second thought',
        inProgress: false,
      },
      {
        type: 'thinking',
        content: 'Third thought',
        inProgress: false,
      },
      {
        type: 'message',
        content: 'Done.',
        inProgress: false,
      },
    ]);
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

  it('replaces tool updates with the final result using canonical names', () => {
    let messages: ChatRenderMessage[] = [];

    messages = applyChatStreamEvent(messages, {
      kind: 'tool-use',
      tool_call_id: 'tool-1',
      tool_name: 'Bash',
      input: '{"command":"printf hello"}',
      role: 'assistant',
    });
    messages = applyChatStreamEvent(messages, {
      kind: 'tool-update',
      tool_call_id: 'tool-1',
      tool_name: 'Bash',
      role: 'assistant',
      tool_result: {
        toolName: 'bash',
        success: true,
        metadata: {
          command: 'printf hello',
          output: 'hel',
          exitCode: 0,
        },
      },
    });

    expect(messages[0].blocks?.[0]).toEqual({
      type: 'tools',
      tools: [
        {
          callId: 'tool-1',
          name: 'bash',
          input: '{"command":"printf hello"}',
          inProgress: true,
          result: {
            toolName: 'bash',
            success: true,
            metadata: {
              command: 'printf hello',
              output: 'hel',
              exitCode: 0,
            },
          },
        },
      ],
    });

    messages = applyChatStreamEvent(messages, {
      kind: 'tool-result',
      tool_call_id: 'tool-1',
      tool_name: 'Bash',
      role: 'assistant',
      tool_result: {
        toolName: 'bash',
        success: true,
        metadata: {
          command: 'printf hello',
          output: 'hello',
          exitCode: 0,
        },
      },
    });

    expect(messages[0].blocks?.[0]).toEqual({
      type: 'tools',
      tools: [
        {
          callId: 'tool-1',
          name: 'bash',
          input: '{"command":"printf hello"}',
          result: {
            toolName: 'bash',
            success: true,
            metadata: {
              command: 'printf hello',
              output: 'hello',
              exitCode: 0,
            },
          },
        },
      ],
    });
  });

  it('tracks interleaved parallel subagent updates independently', () => {
    let messages: ChatRenderMessage[] = [];

    messages = applyChatStreamEvent(messages, {
      kind: 'tool-use',
      tool_call_id: 'subagent-1',
      tool_name: 'subagent',
      input: '{"task":"first"}',
      role: 'assistant',
    });
    messages = applyChatStreamEvent(messages, {
      kind: 'tool-use',
      tool_call_id: 'subagent-2',
      tool_name: 'subagent',
      input: '{"task":"second"}',
      role: 'assistant',
    });
    messages = applyChatStreamEvent(messages, {
      kind: 'tool-update',
      tool_call_id: 'subagent-2',
      tool_name: 'subagent',
      role: 'assistant',
      tool_result: {
        toolName: 'subagent',
        success: true,
        metadata: {
          extensionId: 'subagent',
          toolName: 'subagent',
          output: '',
          data: {
            taskRun: {
              title: 'Second task',
              detail: 'reviewing',
              status: 'running',
            },
          },
        },
      },
    });
    messages = applyChatStreamEvent(messages, {
      kind: 'tool-update',
      tool_call_id: 'subagent-1',
      tool_name: 'subagent',
      role: 'assistant',
      tool_result: {
        toolName: 'subagent',
        success: true,
        metadata: {
          extensionId: 'subagent',
          toolName: 'subagent',
          output: '',
          data: {
            taskRun: {
              title: 'First task',
              detail: 'testing',
              status: 'running',
            },
          },
        },
      },
    });
    messages = applyChatStreamEvent(messages, {
      kind: 'tool-result',
      tool_call_id: 'subagent-2',
      tool_name: 'subagent',
      role: 'assistant',
      tool_result: {
        toolName: 'subagent',
        success: true,
        metadata: {
          extensionId: 'subagent',
          toolName: 'subagent',
          output: 'second result',
          data: {
            taskRun: {
              title: 'Second task complete',
              detail: '',
              status: 'completed',
            },
          },
        },
      },
    });

    const toolsBlock = messages[0].blocks?.find((block) => block.type === 'tools');
    expect(toolsBlock?.type).toBe('tools');
    if (!toolsBlock || toolsBlock.type !== 'tools') {
      throw new Error('expected tools block');
    }

    expect(toolsBlock.tools).toHaveLength(2);
    expect(toolsBlock.tools[0]).toMatchObject({
      callId: 'subagent-1',
      name: 'subagent',
      inProgress: true,
      result: {
        metadata: {
          data: {
            taskRun: {
              title: 'First task',
              detail: 'testing',
              status: 'running',
            },
          },
        },
      },
    });
    expect(toolsBlock.tools[1]).toMatchObject({
      callId: 'subagent-2',
      name: 'subagent',
      result: {
        metadata: {
          output: 'second result',
          data: {
            taskRun: {
              title: 'Second task complete',
              status: 'completed',
            },
          },
        },
      },
    });
    expect(toolsBlock.tools[1]).not.toHaveProperty('inProgress');
  });

  it.each([
    { kind: 'tool-update' as const, command: 'echo hello' },
    { kind: 'tool-result' as const, command: undefined },
  ])('upserts $kind with a canonical name when a reconnect missed tool-use', ({
    kind,
    command,
  }) => {
    const result = {
      toolName: 'bash',
      success: true,
      metadata: command ? { command, output: 'hello' } : undefined,
    };
    const messages = applyChatStreamEvent([], {
      kind,
      tool_call_id: 'tool-1',
      tool_name: 'Bash',
      role: 'assistant',
      tool_result: result,
    });

    expect(messages).toEqual([
      {
        role: 'assistant',
        blocks: [
          {
            type: 'tools',
            tools: [
              {
                callId: 'tool-1',
                name: 'bash',
                input: command ? JSON.stringify({ command }) : '{}',
                ...(kind === 'tool-update' ? { inProgress: true } : {}),
                result,
              },
            ],
          },
        ],
      },
    ]);
  });

  it('ignores usage events for transcript rendering', () => {
    const messages: ChatRenderMessage[] = [
      {
        role: 'user',
        content: 'hello',
      },
    ];

    const nextMessages = applyChatStreamEvent(messages, {
      kind: 'usage',
      conversation_id: 'conv-123',
      role: 'assistant',
      usage: {
        inputTokens: 100,
        outputTokens: 50,
      },
    });

    expect(nextMessages).toEqual(messages);
  });

  it('appends streamed user-message events as user blocks', () => {
    const messages = applyChatStreamEvent([], {
      kind: 'user-message',
      role: 'user',
      content: [
        { type: 'text', text: 'Use this image' },
        {
          type: 'image',
          source: {
            data: 'aGVsbG8=',
            media_type: 'image/png',
          },
        },
      ],
    });

    expect(messages).toEqual([
      {
        role: 'user',
        content: [
          { type: 'text', text: 'Use this image' },
          {
            type: 'image',
            source: {
              data: 'aGVsbG8=',
              media_type: 'image/png',
            },
          },
        ],
      },
    ]);
  });

  it('replaces the latest user message display without duplicating it', () => {
    const messages = applyChatStreamEvent(
      [
        { role: 'assistant', content: 'Earlier response' },
        { role: 'user', content: '/dictate' },
      ],
      {
        kind: 'user-message-display',
        role: 'user',
        content: 'What should I make for breakfast?',
      }
    );

    expect(messages).toEqual([
      { role: 'assistant', content: 'Earlier response' },
      { role: 'user', content: 'What should I make for breakfast?' },
    ]);
  });

  it('appends a user message display when loaded history ends with an assistant', () => {
    let messages: ChatRenderMessage[] = [
      { role: 'user', content: 'Earlier question' },
      { role: 'assistant', content: 'Earlier response' },
    ];

    messages = applyChatStreamEvent(messages, {
      kind: 'user-message-display',
      role: 'user',
      content: 'What should I make for breakfast?',
    });
    messages = applyChatStreamEvent(messages, {
      kind: 'text-delta',
      role: 'assistant',
      delta: 'Try pancakes.',
    });

    expect(messages).toEqual([
      { role: 'user', content: 'Earlier question' },
      { role: 'assistant', content: 'Earlier response' },
      { role: 'user', content: 'What should I make for breakfast?' },
      {
        role: 'assistant',
        blocks: [{ type: 'message', content: 'Try pancakes.', inProgress: true }],
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

describe('compaction history and streaming', () => {
  const apiCompaction: CompactionMarker = {
    id: 'compact-1',
    method: 'api',
    createdAt: '2026-09-20T12:00:00Z',
  };
  const summaryCompaction: CompactionMarker = {
    id: 'compact-2',
    method: 'summary',
    summary: '**Progress**\n\nThe repository has been inspected.',
    createdAt: '2026-09-20T12:10:00Z',
  };
  const conversation: Conversation = {
    id: 'compacted-history',
    createdAt: '',
    updatedAt: '',
    messageCount: 2,
    messages: [
      { role: 'user', content: 'Inspect the repository.' },
      {
        role: 'assistant',
        content: 'Done.',
        thinkingTexts: ['Inspecting files.'],
        toolCalls: [{ id: 'tool-1', function: { name: 'bash', arguments: '{"command":"ls"}' } }],
      },
    ],
    toolResults: {
      'tool-1': { toolName: 'bash', success: true, metadata: { output: 'README.md' } },
    },
  };

  it.each([
    false,
    true,
  ])('preserves all history across repeated compactions and replay (before current user: %s)', (beforeCurrentUser) => {
    const original = conversationToChatMessages(conversation);
    const originalSnapshot = JSON.stringify(original);
    let live = original;
    const persisted: Message[] = [...(conversation.messages || [])];

    for (const [index, compaction] of [apiCompaction, summaryCompaction].entries()) {
      const user: Message = { role: 'user', content: `Continue ${index + 1}.` };
      const marker: Message = {
        role: 'assistant',
        kind: 'context-compacted',
        content: 'Context compacted',
        compaction,
      };
      live = applyChatStreamEvent(live, { kind: 'user-message', content: user.content });
      const event = {
        kind: 'context-compacted' as const,
        compaction,
        before_current_user: beforeCurrentUser,
      };
      live = applyChatStreamEvent(live, event);
      expect(applyChatStreamEvent(live, event)).toEqual(live);
      live = applyChatStreamEvent(live, { kind: 'text-delta', delta: 'Done.' });
      live = applyChatStreamEvent(live, { kind: 'content-end' });

      persisted.push(...(beforeCurrentUser ? [marker, user] : [user, marker]));
      persisted.push({ role: 'assistant', content: 'Done.' });
    }

    const replayed = conversationToChatMessages({ ...conversation, messages: persisted });
    expect(live).toEqual(replayed);
    expect(JSON.stringify(original)).toBe(originalSnapshot);
    expect(replayed[0]).toEqual(original[0]);
    expect(replayed[1].blocks?.slice(0, 3)).toEqual(original[1].blocks);
    expect(
      replayed
        .flatMap((message) => message.blocks || [])
        .filter((block) => block.type === 'compaction')
    ).toEqual([
      { type: 'compaction', compaction: apiCompaction },
      { type: 'compaction', compaction: summaryCompaction },
    ]);

    // An older completion arriving after reload must not move or duplicate its marker.
    expect(
      applyChatStreamEvent(replayed, {
        kind: 'context-compacted',
        compaction: apiCompaction,
        before_current_user: true,
      })
    ).toEqual(replayed);
    expect(
      conversationToChatMessages({
        ...conversation,
        messages: [
          ...persisted,
          { role: 'assistant', kind: 'context-compacted', compaction: apiCompaction, content: '' },
        ],
      })
    ).toEqual(replayed);
  });

  it('inserts a pre-turn marker before the latest user even with an assistant tail', () => {
    const submittedUser: Message = { role: 'user', content: 'Continue.' };
    const answer: Message = { role: 'assistant', content: 'Already streaming.' };
    const previous = conversationToChatMessages({
      ...conversation,
      messages: [...(conversation.messages || []), submittedUser, answer],
    });
    const updated = applyChatStreamEvent(previous, {
      kind: 'context-compacted',
      compaction: apiCompaction,
      before_current_user: true,
    });
    expect(updated[1].blocks?.[3]).toEqual({ type: 'compaction', compaction: apiCompaction });
    expect(updated.slice(2)).toEqual(previous.slice(2));
    expect(updated).toEqual(
      conversationToChatMessages({
        ...conversation,
        messages: [
          ...(conversation.messages || []),
          { role: 'assistant', kind: 'context-compacted', compaction: apiCompaction, content: '' },
          submittedUser,
          answer,
        ],
      })
    );
  });

  it('can insert a pre-turn marker before the first visible user', () => {
    const user: ChatRenderMessage = { role: 'user', content: 'Continue.' };
    const updated = applyChatStreamEvent([user], {
      kind: 'context-compacted',
      compaction: apiCompaction,
      before_current_user: true,
    });
    expect(updated).toEqual([
      { role: 'assistant', blocks: [{ type: 'compaction', compaction: apiCompaction }] },
      user,
    ]);
  });

  it.each([
    'text',
    'thinking',
  ] as const)('does not deduplicate repeated %s across a compaction boundary', (kind) => {
    const blockType = kind === 'text' ? 'message' : 'thinking';
    let messages = applyChatStreamEvent([], { kind, content: 'Done.' });
    messages = applyChatStreamEvent(messages, {
      kind: 'context-compacted',
      compaction: summaryCompaction,
    });
    messages = applyChatStreamEvent(messages, { kind, content: 'Done.' });
    expect(messages[0].blocks).toEqual([
      { type: blockType, content: 'Done.', inProgress: false },
      { type: 'compaction', compaction: summaryCompaction },
      { type: blockType, content: 'Done.', inProgress: false },
    ]);
  });

  it('ignores a compaction event without a marker', () => {
    const messages = conversationToChatMessages(conversation);
    expect(applyChatStreamEvent(messages, { kind: 'context-compacted' })).toEqual(messages);
  });
});
