import type { Meta, StoryObj } from '@storybook/react-vite';
import { expect, userEvent, within } from 'storybook/test';
import { sampleBashToolResult, sampleFileReadToolResult } from '../../stories/fixtures';
import type { ChatRenderToolCall, CodeExecutionMetadata } from '../../types';
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
            unifiedDiff:
              '@@ -10 +10,2 @@\n-const tools = groupTools(calls);\n+const tools = groupActivities(calls);\n+renderFileActivities(tools);\n',
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

export const CommandAlignment: Story = {
  args: {
    tools: [
      {
        ...successfulTools[0],
        input: JSON.stringify({
          command: 'npm run test:run -- ChatComposer',
          description: 'Measure baseline coverage of affected frontend components',
        }),
      },
    ],
  },
  play: async ({ canvasElement }) => {
    await canvasElement.ownerDocument.fonts.ready;
    await userEvent.click(within(canvasElement).getByText('Ran 1 command'));

    const brief = canvasElement.querySelector<HTMLElement>('.bash-tool-brief');
    const description = brief?.querySelector('.bash-tool-description');
    const button = brief?.querySelector<HTMLButtonElement>('.bash-copy-command');
    const badge = brief?.querySelector('.bash-tool-badge');
    if (!brief || !description || !button || !badge) {
      throw new Error('Command description, copy button, or exit status was not rendered');
    }

    const descriptionRect = description.getBoundingClientRect();
    const center = descriptionRect.top + descriptionRect.height / 2;
    for (const element of [button, badge]) {
      const rect = element.getBoundingClientRect();
      expect(Math.abs(rect.top + rect.height / 2 - center)).toBeLessThan(1);
      expect(rect.left).toBeGreaterThanOrEqual(descriptionRect.right);
    }
    expect(brief.scrollWidth).toBeLessThanOrEqual(brief.clientWidth);
    expect(button).toHaveClass('copy-button');
    expect(getComputedStyle(button).backgroundColor).toBe('rgba(0, 0, 0, 0)');
    expect(getComputedStyle(button).borderRadius).toBe('4px');
  },
};

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

export const CodeExecution: Story = {
  args: {
    tools: [
      {
        callId: 'code-execution',
        name: 'code_execute',
        input: JSON.stringify({
          code: `const command = await tools.bash({
  command: "npm run test:run -- ChatComposer",
  description: "Run focused component tests"
});
await tools.apply_patch({input: patch});
return "Tests passed and patch applied.";`,
        }),
        result: {
          toolName: 'code_execute',
          success: true,
          metadata: {
            status: 'completed',
            durationMs: 1234,
            items: [{ type: 'json', value: 'Tests passed and patch applied.' }],
            calls: [successfulTools[0], patchAndSearchTools[0]].map((tool) => ({
              callId: tool.callId,
              toolName: tool.name,
              status: 'completed',
              durationMs: 617,
              input: JSON.parse(tool.input),
              result: tool.result,
            })),
          },
        },
      },
    ],
  },
  play: async ({ canvasElement }) => {
    const canvas = within(canvasElement);
    await userEvent.click(canvas.getByText(/Code execution/));
    await userEvent.click(canvas.getByText('Code', { exact: true }));
    await userEvent.click(canvas.getByText('bash · Run focused component tests'));
  },
};

export const ConsecutiveCodeExecutions: Story = {
  args: {
    tools: Array.from({ length: 6 }, (_, index) => {
      const tool = successfulTools[index % successfulTools.length];
      return {
        callId: `code-${index}`,
        name: 'code_execute',
        input: JSON.stringify({ code: `return await tools.${tool.name}(${tool.input});` }),
        result: {
          toolName: 'code_execute',
          success: true,
          metadata: {
            status: 'completed',
            durationMs: 119,
            calls: [
              {
                callId: tool.callId,
                toolName: tool.name,
                status: 'completed',
                durationMs: 119,
                input: JSON.parse(tool.input),
                result: tool.result,
              },
            ],
          },
        },
      };
    }),
  },
};

export const ConsecutiveCodeExecutionsWithFailure: Story = {
  args: {
    tools: ConsecutiveCodeExecutions.args?.tools?.map((tool, index) => {
      if (index !== 5 || !tool.result) return tool;
      const metadata = tool.result.metadata as CodeExecutionMetadata;
      return {
        ...tool,
        result: {
          ...tool.result,
          success: false,
          metadata: {
            ...metadata,
            status: 'failed',
            calls: metadata.calls.map((call) => ({ ...call, status: 'failed' })),
          },
        },
      };
    }),
  },
};

export const ConsecutiveCodeExecutionsAllFailed: Story = {
  args: {
    tools: ConsecutiveCodeExecutions.args?.tools?.map((tool) => {
      if (!tool.result) return tool;
      const metadata = tool.result.metadata as CodeExecutionMetadata;
      return {
        ...tool,
        result: {
          ...tool.result,
          success: false,
          metadata: {
            ...metadata,
            status: 'failed',
            calls: metadata.calls.map((call) => ({ ...call, status: 'failed' })),
          },
        },
      };
    }),
  },
};

export const CodeExecutionOutcomes: Story = {
  render: () => {
    const completed = ConsecutiveCodeExecutions.args?.tools || [];
    const failed = ConsecutiveCodeExecutionsAllFailed.args?.tools || [];
    const examples = [
      {
        title: 'All succeeded',
        description: 'A teal check when every execution succeeds.',
        tools: completed,
      },
      {
        title: 'Some failed',
        description: 'The teal check means finished; only the failure count is red.',
        tools: ConsecutiveCodeExecutionsWithFailure.args?.tools || [],
      },
      {
        title: 'All failed',
        description: 'A red cross and label only when every execution fails.',
        tools: failed,
      },
      {
        title: 'Handled child failures',
        description:
          'Every script completed after catching a tool error. The failed tool count stays red.',
        tools: failed.map((tool) => ({
          ...tool,
          result: {
            ...tool.result,
            toolName: 'code_execute',
            success: true,
            metadata: { ...(tool.result?.metadata as CodeExecutionMetadata), status: 'completed' },
          },
        })),
      },
      {
        title: 'Script-level error',
        description: 'A script failed before calling a tool, so its error is counted separately.',
        tools: completed.map((tool, index) =>
          index !== 5
            ? tool
            : {
                ...tool,
                input: JSON.stringify({ code: 'throw new Error();' }),
                result: {
                  toolName: 'code_execute',
                  success: false,
                  error: 'Script failed before calling tools',
                  metadata: { status: 'failed', durationMs: 10, calls: [] },
                },
              }
        ),
      },
      {
        title: 'Still running with a failure',
        description:
          'The spinner takes precedence while work continues. Known failures remain visible.',
        tools: completed.map((tool, index) => {
          if (index === 0) return failed[index];
          if (index !== 5) return tool;
          const metadata = tool.result?.metadata as CodeExecutionMetadata;
          return {
            ...tool,
            inProgress: true,
            result: {
              toolName: 'code_execute',
              success: true,
              metadata: {
                ...metadata,
                durationMs: 1200,
                status: 'running',
                calls: metadata.calls.map((call) => ({ ...call, status: 'running' })),
              },
            },
          };
        }),
      },
    ];
    return (
      <div className="grid items-start gap-8 md:grid-cols-2">
        {examples.map(({ title, description, tools }) => (
          <section key={title} aria-label={title} className="min-w-0">
            <h2 className="mb-1 font-heading text-base font-semibold text-kodelet-dark">{title}</h2>
            <p className="mb-3 text-sm text-kodelet-dark/70">{description}</p>
            <ChatToolActivity tools={tools} />
          </section>
        ))}
      </div>
    );
  },
  play: async ({ canvasElement }) => {
    const canvas = within(canvasElement);
    for (const [title, icon] of [
      ['All succeeded', '.lucide-check'],
      ['Some failed', '.lucide-check'],
      ['All failed', '.lucide-x'],
      ['Handled child failures', '.lucide-check'],
      ['Script-level error', '.lucide-check'],
      ['Still running with a failure', '.spinner-glyph'],
    ]) {
      const section = canvas.getByRole('region', { name: title });
      const details = section.querySelector('details');
      const summary = details?.querySelector('summary');
      if (!summary || !details) throw new Error(`Missing ${title} summary`);
      expect(summary.querySelector(icon)).toBeInTheDocument();
      expect(details.classList.contains('activity-card-error')).toBe(title === 'All failed');
      const duration = summary.querySelector('.code-execution-duration');
      const failure = summary.querySelector('.code-execution-failure');
      if (!duration) throw new Error('Missing execution duration');
      expect(getComputedStyle(duration).color).not.toBe(getComputedStyle(summary).color);
      if (failure)
        expect(getComputedStyle(failure).color).not.toBe(getComputedStyle(duration).color);
      if (details.open) await userEvent.click(summary);
      await userEvent.click(summary);
      expect(details).toHaveAttribute('open');
      await userEvent.click(summary);
      expect(details).not.toHaveAttribute('open');
    }
  },
};

export const FileOperations: Story = {
  args: {
    tools: [
      successfulTools[1],
      {
        callId: 'write-1',
        name: 'file_write',
        input: '{"file_path":"notes.md"}',
        result: {
          toolName: 'file_write',
          success: true,
          metadata: {
            filePath: 'notes.md',
            unifiedDiff: '@@ -0,0 +1 @@\n+# Notes\n',
          },
        },
      },
      patchAndSearchTools[0],
      {
        callId: 'edit-failed',
        name: 'file_edit',
        input: '{"file_path":"config.yaml"}',
        result: {
          toolName: 'file_edit',
          success: false,
          error: 'The text to replace was not found.',
          metadata: { filePath: 'config.yaml' },
        },
      },
      { callId: 'read-pending', name: 'file_read', input: '{"file_path":"AGENTS.md"}' },
    ],
  },
};
