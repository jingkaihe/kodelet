import type { Meta, StoryObj } from '@storybook/react-vite';
import type { ChatRenderToolCall } from '../../types';
import {
  sampleBashToolResult,
  sampleFileReadToolResult,
} from '../../stories/fixtures';
import ChatToolActivity from './ChatToolActivity';

const successfulTools: ChatRenderToolCall[] = [
  {
    callId: 'bash-success',
    name: 'bash',
    input: JSON.stringify({
      command: 'npm run test:run -- ChatComposer',
      description: 'Run focused component tests',
    }),
    result: sampleBashToolResult,
  },
  {
    callId: 'file-read-success',
    name: 'file_read',
    input: JSON.stringify({
      file_path: 'pkg/webui/frontend/src/components/chat/ChatComposer.tsx',
      offset: 1,
      line_limit: 80,
    }),
    result: sampleFileReadToolResult,
  },
];

const runningTools: ChatRenderToolCall[] = [
  {
    callId: 'bash-running',
    name: 'bash',
    input: JSON.stringify({
      command: 'npm run frontend-test',
      description: 'Run the full frontend suite',
    }),
  },
];

const failedTools: ChatRenderToolCall[] = [
  {
    callId: 'bash-failed',
    name: 'bash',
    input: JSON.stringify({
      command: 'npm run test:run -- MissingComponent',
      description: 'Run a focused test file',
    }),
    result: {
      toolName: 'bash',
      success: false,
      error: 'No tests matched the supplied pattern.',
      metadata: {
        command: 'npm run test:run -- MissingComponent',
        exitCode: 1,
        output: '',
        executionTime: 119000000,
        workingDir: '/home/jingkaihe/workspace/kodelet/pkg/webui/frontend',
      },
    },
  },
];

const patchAndSearchTools: ChatRenderToolCall[] = [
  {
    callId: 'patch-success',
    name: 'apply_patch',
    input: JSON.stringify({
      input: [
        '*** Begin Patch',
        '*** Update File: pkg/webui/frontend/src/components/chat/ChatTranscript.tsx',
        '*** End Patch',
      ].join('\n'),
    }),
    result: {
      toolName: 'apply_patch',
      success: true,
      metadata: {
        changes: [
          {
            path: 'pkg/webui/frontend/src/components/chat/ChatTranscript.tsx',
            operation: 'update',
            unifiedDiff: '@@ -10 +10,2 @@\n-const tools = groupTools(calls);\n+const tools = groupActivities(calls);\n+renderFileActivities(tools);\n',
          },
          {
            path: 'pkg/webui/frontend/src/components/chat/ChatToolActivity.tsx',
            operation: 'add',
            unifiedDiff: '@@ -0,0 +1,2 @@\n+export const FileActivity = () => {\n+};\n',
          },
          {
            path: 'pkg/webui/frontend/src/components/chat/LegacyToolGroup.tsx',
            operation: 'delete',
            unifiedDiff: '@@ -1 +0,0 @@\n-export const LegacyToolGroup = () => null;\n',
          },
          {
            path: 'pkg/webui/frontend/src/components/chat/OldActivity.tsx',
            movePath: 'pkg/webui/frontend/src/components/chat/Activity.tsx',
            operation: 'update',
            unifiedDiff: '',
          },
        ],
      },
    },
  },
  {
    callId: 'search-success',
    name: 'openai_web_search',
    input: JSON.stringify({
      type: 'find_in_page',
      pattern: 'React Vite',
      url: 'https://storybook.js.org/docs',
    }),
    result: {
      toolName: 'openai_web_search',
      success: true,
      metadata: {
        action: 'find_in_page',
        status: 'completed',
        pattern: 'React Vite',
        url: 'https://storybook.js.org/docs',
      },
    },
  },
];

const meta = {
  title: 'Chat/ChatToolActivity',
  component: ChatToolActivity,
  parameters: {
    layout: 'fullscreen',
  },
  decorators: [
    (Story) => (
      <div className="chat-main-panel min-h-screen px-4 py-6">
        <div className="mx-auto max-w-3xl">
          <Story />
        </div>
      </div>
    ),
  ],
  args: {
    tools: successfulTools,
  },
} satisfies Meta<typeof ChatToolActivity>;

export default meta;

type Story = StoryObj<typeof meta>;

export const Successful: Story = {};

export const Running: Story = {
  args: {
    tools: runningTools,
  },
};

export const Failed: Story = {
  args: {
    tools: failedTools,
  },
};

export const PatchAndSearch: Story = {
  args: {
    tools: patchAndSearchTools,
  },
};

export const FileOperations: Story = {
  args: {
    tools: [
      successfulTools[1],
      {
        callId: 'write-1', name: 'file_write', input: '{"file_path":"notes.md"}',
        result: { toolName: 'file_write', success: true, metadata: {
          filePath: 'notes.md', unifiedDiff: '@@ -0,0 +1 @@\n+# Notes\n',
        } },
      },
      patchAndSearchTools[0],
      {
        callId: 'edit-failed', name: 'file_edit', input: '{"file_path":"config.yaml"}',
        result: { toolName: 'file_edit', success: false, error: 'The text to replace was not found.', metadata: { filePath: 'config.yaml' } },
      },
      { callId: 'read-pending', name: 'file_read', input: '{"file_path":"AGENTS.md"}' },
    ],
  },
};
