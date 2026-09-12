import { render, screen, within } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { describe, expect, it } from 'vitest';
import type { ChatRenderToolCall } from '../../types';
import ChatToolActivity, {
  formatToolInputPreview,
  getToolActivityStatus,
  getToolSummary,
} from './ChatToolActivity';

const bashTool = (
  command: string,
  overrides: Partial<ChatRenderToolCall> = {}
): ChatRenderToolCall => ({
  callId: command,
  name: 'bash',
  input: JSON.stringify({ command }),
  result: {
    toolName: 'bash',
    success: true,
    metadata: { command, output: `${command} output`, executionTime: 119000000, exitCode: 0 },
  },
  ...overrides,
});

describe('ChatToolActivity', () => {
  it('keeps generated image previews visible outside collapsed tool details', () => {
    const tool: ChatRenderToolCall = {
      callId: 'diagram-1',
      name: 'draw_diagram',
      input: '{"prompt":"Draw the architecture"}',
      result: {
        toolName: 'draw_diagram',
        metadataType: 'extension_tool',
        success: true,
        metadata: {
          extensionId: 'diagram',
          toolName: 'draw_diagram',
          output: 'Generated a diagram.',
        },
        attachments: [
          {
            type: 'image',
            artifactId: 'art_1',
            shortCode: 'diagram',
            mimeType: 'image/png',
            alt: 'Architecture diagram',
          },
        ],
      },
    };
    const { container } = render(<ChatToolActivity tools={[tool]} />);

    expect(screen.getByText('Generated image')).toBeVisible();
    expect(container.querySelector('details')).not.toHaveAttribute('open');
    const preview = screen.getByRole('img', { name: 'Architecture diagram' });
    expect(preview).toBeVisible();
    expect(preview.closest('details')).toBeNull();
    expect(screen.getAllByRole('img')).toHaveLength(1);
    expect(
      screen.getByRole('link', { name: 'Download image: Architecture diagram' })
    ).toBeVisible();
  });

  it('does not show image previews from transient tool updates', () => {
    render(
      <ChatToolActivity
        tools={[
          {
            callId: 'diagram-1',
            name: 'draw_diagram',
            input: '{}',
            inProgress: true,
            result: {
              toolName: 'draw_diagram',
              success: true,
              attachments: [
                { type: 'image', artifactId: 'art_1', shortCode: 'diagram', mimeType: 'image/png' },
              ],
            },
          },
        ]}
      />
    );

    expect(screen.queryByRole('img')).not.toBeInTheDocument();
    expect(screen.getByLabelText('Tool running')).toBeInTheDocument();
  });

  it('uses an inspected image label for artifact-backed view_image results', () => {
    const tool: ChatRenderToolCall = {
      callId: 'view-1',
      name: 'view_image',
      input: '{"artifactId":"art_1"}',
      result: {
        toolName: 'view_image',
        success: true,
        attachments: [
          { type: 'image', artifactId: 'art_1', shortCode: 'diagram', mimeType: 'image/png' },
        ],
      },
    };
    render(<ChatToolActivity tools={[tool]} />);

    expect(getToolSummary(tool)).toBe('Viewed image');
    expect(screen.getByRole('img', { name: 'Viewed image' })).toBeVisible();
  });

  it('renders a running tool with a compact input preview', () => {
    const longPrompt = 'x'.repeat(700);
    const { container } = render(
      <ChatToolActivity
        tools={[
          {
            callId: 'web-fetch-1',
            name: 'web_fetch',
            input: JSON.stringify({
              url: 'https://example.com/news',
              prompt: longPrompt,
            }),
          },
        ]}
      />
    );

    expect(screen.getByText('Fetch URL: https://example.com/news')).toBeInTheDocument();
    expect(screen.getByLabelText('Tool running')).toHaveTextContent('running');
    expect(screen.getByText('Awaiting tool result…')).toBeInTheDocument();
    expect(container.querySelector('.activity-card-live')).toBeInTheDocument();
    expect(container.querySelector('.running-tool-input-preview')).toBeInTheDocument();
    expect(screen.getByText(/more characters/)).toBeInTheDocument();
    expect(screen.queryByText(longPrompt)).not.toBeInTheDocument();
  });

  it('renders completed web fetch details without repeating the header URL or metadata cards', () => {
    const { container } = render(
      <ChatToolActivity
        tools={[
          {
            callId: 'web-fetch-1',
            name: 'web_fetch',
            input: '{"url":"https://example.com/news","prompt":"Extract headlines"}',
            result: {
              toolName: 'web_fetch',
              success: true,
              metadata: {
                url: 'https://example.com/news',
                processedType: 'saved',
                contentType: 'text/markdown',
                savedPath: '/tmp/news.md',
                prompt: 'Extract headlines',
                content: '# Headlines',
              },
            },
          },
        ]}
      />
    );

    expect(screen.getAllByText('https://example.com/news')).toHaveLength(1);
    expect(screen.getByText('saved page')).toBeInTheDocument();
    expect(screen.getByText('/tmp/news.md')).toBeInTheDocument();
    expect(screen.getByText('Extract headlines')).toBeInTheDocument();
    expect(container.querySelector('.tool-kv-grid')).not.toBeInTheDocument();
  });

  it('collapses a successful command and reveals its shell transcript and duration on expansion', async () => {
    const user = userEvent.setup();
    const { container } = render(
      <ChatToolActivity
        tools={[
          {
            callId: 'bash-1',
            name: 'bash',
            input: '{"command":"pwd","description":"Print working directory"}',
            result: {
              toolName: 'bash',
              success: true,
              metadata: {
                command: 'pwd',
                exitCode: 0,
                output: '/workspace/kodelet',
                executionTime: 119000000,
                workingDir: '/workspace/kodelet',
              },
            },
          },
        ]}
      />
    );

    const group = container.querySelector('details.activity-card.activity-command-group');
    expect(group).not.toHaveAttribute('open');
    expect(screen.getByText('Ran 1 command')).toBeVisible();
    expect(screen.queryByText('Bash: pwd')).not.toBeInTheDocument();
    expect(screen.getByText('$ pwd')).not.toBeVisible();
    expect(screen.getByText('/workspace/kodelet')).not.toBeVisible();

    await user.click(screen.getByText('Ran 1 command'));

    expect(group).toHaveAttribute('open');
    expect(screen.getByText('$ pwd')).toBeVisible();
    expect(screen.getByLabelText('Tool 119ms')).toHaveTextContent('119ms');
    expect(screen.getByLabelText('Tool 119ms')).toBeVisible();
    expect(screen.getByText('Print working directory')).toBeVisible();
    expect(screen.getByText('/workspace/kodelet')).toBeVisible();
    expect(container.querySelector('.activity-card-live')).not.toBeInTheDocument();
  });

  it('renders accumulated bash output while the tool is still running', () => {
    const { container } = render(
      <ChatToolActivity
        tools={[
          {
            callId: 'bash-1',
            name: 'bash',
            input: '{"command":"long-task","description":"Run long task"}',
            inProgress: true,
            result: {
              toolName: 'bash',
              success: true,
              metadata: {
                command: 'long-task',
                output: 'partial output',
                exitCode: 0,
              },
            },
          },
        ]}
      />
    );

    expect(screen.getByText('Running 1 command')).toBeVisible();
    expect(screen.getByText('$ long-task')).toBeVisible();
    expect(screen.getByLabelText('Tool running')).toHaveTextContent('running');
    expect(screen.getByText('partial output')).toBeVisible();
    expect(container.querySelector('details.activity-command-group')).toHaveAttribute('open');
    expect(screen.queryByText('Awaiting tool result…')).not.toBeInTheDocument();
    expect(container.querySelector('.running-tool-input-preview')).not.toBeInTheDocument();
    expect(container.querySelector('.activity-card-live')).toBeInTheDocument();
  });

  it('collapses failed commands with visible failure status and expandable errors', async () => {
    const user = userEvent.setup();
    const { container } = render(
      <ChatToolActivity
        tools={[
          {
            callId: 'bash-1',
            name: 'bash',
            input: '{"command":"false","description":"Run failing command"}',
            result: {
              toolName: 'bash',
              success: false,
              error: 'Command exited with status 1.',
              metadata: {
                command: 'false',
                exitCode: 1,
                output: '',
                executionTime: 119000000,
                workingDir: '/workspace/kodelet',
              },
            },
          },
        ]}
      />
    );

    expect(screen.getByText('Ran 1 command')).toBeVisible();
    expect(screen.getByText('$ false')).not.toBeVisible();
    expect(screen.getByText('Command exited with status 1.')).not.toBeVisible();
    expect(
      container.querySelector('.activity-command-group.activity-card-error')
    ).not.toHaveAttribute('open');
    expect(container.querySelector('summary .lucide-x')).toBeVisible();
    expect(screen.getByText('1 failed')).toBeVisible();

    await user.click(screen.getByText('Ran 1 command'));

    expect(screen.getByText('$ false')).toBeVisible();
    expect(screen.getByLabelText('Tool failed')).toHaveTextContent('failed');
    expect(screen.getByLabelText('Tool failed')).toBeVisible();
    expect(screen.getByText('Command exited with status 1.')).toBeVisible();
    expect(container.querySelector('.activity-command-group.activity-card-error')).toHaveAttribute(
      'open'
    );
    expect(container.querySelector('summary .lucide-x')).toBeInTheDocument();
    expect(container.querySelector('summary')).toHaveTextContent('1 failed');
  });

  it('groups consecutive bash calls in a single expandable disclosure', async () => {
    const user = userEvent.setup();
    const { container } = render(<ChatToolActivity tools={[bashTool('pwd'), bashTool('ls')]} />);
    const group = container.querySelector('details.activity-command-group');
    const summary = screen.getByText('Ran 2 commands').closest('summary');

    expect(container.querySelectorAll('details')).toHaveLength(1);
    expect(group).not.toHaveAttribute('open');
    expect(summary?.querySelector('.lucide-check')).toBeInTheDocument();
    expect(screen.getByText('$ pwd')).not.toBeVisible();
    expect(screen.getByText('$ ls')).not.toBeVisible();

    // user-event omits summary from tab navigation and native Enter/Space activation.
    // Check those in a browser rather than emulating browser defaults in this test.
    await user.click(screen.getByText('Ran 2 commands'));

    expect(group).toHaveAttribute('open');
    for (const command of ['pwd', 'ls']) {
      expect(screen.getByText(`$ ${command}`)).toBeVisible();
      expect(screen.getByText(`${command} output`)).toBeVisible();
    }
    expect(screen.getAllByLabelText('Tool 119ms')).toHaveLength(2);

    await user.click(screen.getByText('Ran 2 commands'));
    expect(group).not.toHaveAttribute('open');
    expect(screen.getByText('pwd output')).not.toBeVisible();
  });

  it('shows every command in a mixed live group and collapses only after the last command completes', async () => {
    const user = userEvent.setup();
    const completed = bashTool('pwd');
    const streaming = bashTool('npm test', { inProgress: true });
    const pending = bashTool('npm run lint', { result: undefined });
    const { container, rerender } = render(
      <ChatToolActivity tools={[completed, streaming, pending]} />
    );

    expect(screen.getByText('Running 3 commands')).toBeVisible();
    expect(container.querySelectorAll('details')).toHaveLength(1);
    expect(container.querySelector('.activity-command-group')).toHaveAttribute('open');
    expect(screen.getByLabelText('Tool 119ms')).toBeVisible();
    expect(screen.getAllByLabelText('Tool running')).toHaveLength(2);
    for (const command of ['pwd', 'npm test', 'npm run lint']) {
      expect(screen.getByText(`$ ${command}`)).toBeVisible();
    }
    expect(screen.getByText('pwd output')).toBeVisible();
    expect(screen.getByText('npm test output')).toBeVisible();
    expect(container.querySelector('.running-tool-input-preview')).not.toBeInTheDocument();

    rerender(
      <ChatToolActivity tools={[completed, { ...streaming, inProgress: false }, pending]} />
    );

    expect(screen.getByText('Running 3 commands')).toBeVisible();
    expect(container.querySelector('.activity-command-group')).toHaveAttribute('open');
    expect(screen.getAllByLabelText('Tool 119ms')).toHaveLength(2);
    expect(screen.getByLabelText('Tool running')).toBeVisible();
    expect(screen.getByText('npm test output')).toBeVisible();

    rerender(
      <ChatToolActivity tools={[completed, bashTool('npm test'), bashTool('npm run lint')]} />
    );

    expect(screen.getByText('Ran 3 commands')).toBeVisible();
    expect(container.querySelector('.activity-command-group')).not.toHaveAttribute('open');
    expect(screen.queryByLabelText('Tool running')).not.toBeInTheDocument();
    expect(container.querySelector('.spinner-glyph')).not.toBeInTheDocument();
    expect(screen.getByText('npm run lint output')).not.toBeVisible();

    await user.click(screen.getByText('Ran 3 commands'));
    for (const command of ['pwd', 'npm test', 'npm run lint']) {
      expect(screen.getByText(`$ ${command}`)).toBeVisible();
      expect(screen.getByText(`${command} output`)).toBeVisible();
    }
  });

  it('shows pending command input without a Bash header or raw JSON', () => {
    const { container } = render(
      <ChatToolActivity tools={[bashTool('sleep 10', { result: undefined })]} />
    );

    expect(screen.getByText('Running 1 command')).toBeVisible();
    expect(screen.getByText('$ sleep 10')).toBeVisible();
    expect(screen.getByLabelText('Tool running')).toBeVisible();
    expect(container.querySelector('summary .spinner-glyph')).toHaveTextContent('⣾');
    expect(screen.queryByText('Bash: sleep 10')).not.toBeInTheDocument();
    expect(screen.queryByText(/"command"/)).not.toBeInTheDocument();
    expect(container.querySelector('.running-tool-input-preview')).not.toBeInTheDocument();
  });

  it('collapses failed groups only after their last running command finishes', async () => {
    const user = userEvent.setup();
    const failed = bashTool('false', {
      result: {
        toolName: 'bash',
        success: false,
        error: 'Command exited with status 1.',
        metadata: { command: 'false', output: 'failure output', exitCode: 1 },
      },
    });
    const { container, rerender } = render(
      <ChatToolActivity tools={[failed, bashTool('pwd', { result: undefined })]} />
    );

    expect(screen.getByText('Running 2 commands')).toBeVisible();
    expect(container.querySelector('summary')).toHaveTextContent('1 failed');
    expect(screen.getByLabelText('Tool failed')).toBeVisible();
    expect(screen.getByText('failure output')).toBeVisible();

    rerender(<ChatToolActivity tools={[failed, bashTool('pwd')]} />);

    expect(screen.getByText('Ran 2 commands')).toBeVisible();
    expect(container.querySelector('.activity-command-group')).not.toHaveAttribute('open');
    expect(container.querySelector('summary .lucide-x')).toBeInTheDocument();
    expect(container.querySelector('summary')).toHaveTextContent('1 failed');
    expect(screen.getByText('Command exited with status 1.')).not.toBeVisible();
    expect(screen.getByText('$ pwd')).not.toBeVisible();

    await user.click(screen.getByText('Ran 2 commands'));

    expect(screen.getByText('Command exited with status 1.')).toBeVisible();
    expect(screen.getByText('failure output')).toBeVisible();
    expect(screen.getByText('$ pwd')).toBeVisible();
  });

  it('preserves manual expansion while an unrelated tool changes status', async () => {
    const user = userEvent.setup();
    const completed = bashTool('pwd');
    const unrelated: ChatRenderToolCall = {
      callId: 'read-1',
      name: 'file_read',
      input: '{"file_path":"README.md"}',
    };
    const { container, rerender } = render(<ChatToolActivity tools={[completed, unrelated]} />);

    await user.click(screen.getByText('Ran 1 command'));
    const group = container.querySelector('.activity-command-group');
    expect(group).toHaveAttribute('open');

    rerender(
      <ChatToolActivity
        tools={[
          { ...completed },
          { ...unrelated, result: { toolName: 'file_read', success: true } },
        ]}
      />
    );

    expect(container.querySelector('.activity-command-group')).toBe(group);
    expect(group).toHaveAttribute('open');
    expect(screen.getByText('pwd output')).toBeVisible();
  });

  it('does not group bash across other tools or treat similarly named extension tools as bash', async () => {
    const user = userEvent.setup();
    const { container } = render(
      <ChatToolActivity
        tools={[
          bashTool('pwd'),
          {
            callId: 'read-1',
            name: 'file_read',
            input: '{"file_path":"README.md"}',
            result: { toolName: 'file_read', success: true },
          },
          bashTool('ls'),
          {
            callId: 'remote-1',
            name: 'remote_command',
            input: '{}',
            result: { toolName: 'remote_command', success: true },
          },
        ]}
      />
    );

    const groups = container.querySelectorAll('details.activity-command-group');
    expect(groups).toHaveLength(2);
    expect(container.querySelectorAll('details')).toHaveLength(4);
    expect(screen.getAllByText('Ran 1 command')).toHaveLength(2);
    expect(screen.queryByText('Ran 2 commands')).not.toBeInTheDocument();
    expect(groups[0]).toHaveTextContent('$ pwd');
    expect(groups[0]).not.toHaveTextContent('$ ls');
    expect(groups[1]).toHaveTextContent('$ ls');
    expect(groups[1]).not.toHaveTextContent('$ pwd');

    await user.click(screen.getAllByText('Ran 1 command')[0]);
    expect(screen.getByText('$ pwd')).toBeVisible();
    expect(screen.getByText('$ ls')).not.toBeVisible();
  });

  it('returns null for an empty tool collection', () => {
    const { container } = render(<ChatToolActivity tools={[]} />);

    expect(container.firstChild).toBeNull();
  });

  it('summarizes patch and OpenAI search tool calls for compact headers', () => {
    const patchCall: ChatRenderToolCall = {
      callId: 'patch-1',
      name: 'apply_patch',
      input: '{"input":"*** Begin Patch\\n*** Update File: README.md\\n*** End Patch"}',
      result: {
        toolName: 'apply_patch',
        success: true,
        metadata: {
          changes: [
            {
              path: 'README.md',
              operation: 'update',
            },
            {
              path: 'docs/MANUAL.md',
              operation: 'update',
            },
          ],
        },
      },
    };

    const openPageCall: ChatRenderToolCall = {
      callId: 'search-1',
      name: 'openai_web_search',
      input: '{"type":"open_page","status":"completed"}',
      result: {
        toolName: 'openai_web_search',
        success: true,
        metadata: {
          status: 'completed',
          action: 'open_page',
        },
      },
    };

    expect(getToolSummary(patchCall)).toBe('Apply patch: update README.md (+1 more)');
    expect(getToolSummary(openPageCall)).toBe('Open page: URL unavailable');
  });

  it('uses generic extension presentation summaries without body previews', () => {
    const spawnCall: ChatRenderToolCall = {
      callId: 'spawn-1',
      name: 'launch_worker',
      input: '{"name":"parser-reviewer","task":"Review the parser and tests"}',
      result: {
        toolName: 'launch_worker',
        success: true,
        metadata: {
          data: {
            presentation: {
              summary: 'Spawn parser-reviewer',
              body: 'Review the parser and tests',
            },
          },
        },
      },
    };
    const listCall: ChatRenderToolCall = {
      callId: 'list-1',
      name: 'inventory_workers',
      input: '{}',
      result: {
        toolName: 'inventory_workers',
        success: true,
        metadata: {
          data: {
            presentation: {
              summary: 'List agents',
              body: '- **parser-reviewer** — completed',
            },
          },
        },
      },
    };
    const followupCall: ChatRenderToolCall = {
      callId: 'followup-1',
      name: 'send_instruction',
      input: '{"agent_id":"agt_123","task":"Review the parser\\nand tests"}',
      result: {
        toolName: 'send_instruction',
        success: true,
        metadata: {
          data: {
            presentation: {
              summary: 'Follow up parser-reviewer',
              body: 'Review the parser and tests',
            },
          },
        },
      },
    };
    const steerCall: ChatRenderToolCall = {
      callId: 'steer-1',
      name: 'redirect_worker',
      input: '{"agent_id":"agt_123","message":"Focus on error handling"}',
      result: {
        toolName: 'redirect_worker',
        success: true,
        metadata: {
          data: {
            presentation: {
              summary: 'Steer parser-reviewer',
              body: 'Focus on error handling',
            },
          },
        },
      },
    };
    const waitCall: ChatRenderToolCall = {
      callId: 'wait-1',
      name: 'observe_worker',
      input: '{"agent_id":"agt_123"}',
      result: {
        toolName: 'observe_worker',
        success: true,
        metadata: {
          data: { presentation: { summary: 'Wait for parser-reviewer' } },
        },
      },
    };
    const cancelCall: ChatRenderToolCall = {
      callId: 'cancel-1',
      name: 'stop_worker',
      input: '{"agent_id":"agt_123"}',
      result: {
        toolName: 'stop_worker',
        success: true,
        metadata: {
          data: {
            presentation: {
              summary: 'Cancel parser-reviewer',
              body: 'The agent is permanently canceled.',
            },
          },
        },
      },
    };
    const runningWaitCall: ChatRenderToolCall = {
      ...waitCall,
      result: {
        toolName: 'observe_worker',
        success: true,
        metadata: {
          data: {
            taskRun: {
              version: 1,
              revision: 1,
              kind: 'worker',
              status: 'running',
              phase: 'starting',
              title: 'Wait for parser-reviewer',
              elapsedMs: 10,
              counts: { succeeded: 0, failed: 0, running: 0 },
              activities: [],
            },
          },
        },
      },
      inProgress: true,
    };

    expect(getToolSummary(spawnCall)).toBe('Spawn parser-reviewer');
    expect(getToolSummary(listCall)).toBe('List agents');
    expect(getToolSummary(followupCall)).toBe('Follow up parser-reviewer');
    expect(getToolSummary(steerCall)).toBe('Steer parser-reviewer');
    expect(getToolSummary(waitCall)).toBe('Wait for parser-reviewer');
    expect(getToolSummary(cancelCall)).toBe('Cancel parser-reviewer');
    expect(getToolSummary(runningWaitCall)).toBe('Wait for parser-reviewer');
    expect(getToolSummary({ ...followupCall, result: undefined })).toBe('Send Instruction');
    expect(
      getToolSummary({
        ...steerCall,
        result: { toolName: 'redirect_worker', success: false },
      })
    ).toBe('Redirect Worker');
    expect(getToolSummary({ ...waitCall, result: undefined })).toBe('Observe Worker');
    expect(
      getToolSummary({
        ...followupCall,
        result: {
          toolName: 'send_instruction',
          success: true,
          metadata: {
            data: { presentation: { summary: 'Follow up \u202eparser-reviewer' } },
          },
        },
      })
    ).toBe('Send Instruction');

    const { container } = render(
      <ChatToolActivity
        tools={[spawnCall, listCall, followupCall, steerCall, waitCall, cancelCall]}
      />
    );
    const summaries = container.querySelectorAll('summary');
    expect(summaries[0]).toHaveTextContent('Spawn parser-reviewer');
    expect(summaries[0]).not.toHaveTextContent('Review the parser');
    expect(summaries[1]).toHaveTextContent('List agents');
    expect(summaries[1]).not.toHaveTextContent('completed');
    expect(summaries[2]).toHaveTextContent('Follow up parser-reviewer');
    expect(summaries[2]).not.toHaveTextContent('Review the parser');
    expect(summaries[3]).toHaveTextContent('Steer parser-reviewer');
    expect(summaries[3]).not.toHaveTextContent('Focus on error handling');
    expect(summaries[4]).toHaveTextContent('Wait for parser-reviewer');
    expect(summaries[5]).toHaveTextContent('Cancel parser-reviewer');
    expect(summaries[5]).not.toHaveTextContent('permanently canceled');
  });

  it('exposes tool status and preview helpers for focused formatting tests', () => {
    expect(
      getToolActivityStatus({
        callId: 'bash-1',
        name: 'bash',
        input: '{"command":"pwd"}',
        result: {
          toolName: 'bash',
          success: true,
          metadata: {
            command: 'pwd',
            executionTime: 119000000,
          },
        },
      })
    ).toBe('119ms');

    expect(formatToolInputPreview('{"command":"pwd"}')).toBe('{\n  "command": "pwd"\n}');
  });

  it('summarizes and opens a live code-search agent run', () => {
    const { container } = render(
      <ChatToolActivity
        tools={[
          {
            callId: 'search-1',
            name: 'code_search',
            input: '{"query":"Trace tool updates"}',
            inProgress: true,
            result: {
              toolName: 'code_search',
              success: true,
              metadataType: 'extension_tool',
              metadata: {
                extensionId: 'code-search',
                toolName: 'code_search',
                output: 'Searching code',
                data: {
                  taskRun: {
                    version: 1,
                    revision: 1,
                    kind: 'code_search',
                    status: 'running',
                    phase: 'starting',
                    title: 'Searching code',
                    detail: 'starting task',
                    elapsedMs: 1000,
                    counts: { succeeded: 0, failed: 0, running: 0 },
                    activities: [],
                  },
                },
              },
            },
          },
        ]}
      />
    );

    expect(screen.getByText('Searching code')).toBeInTheDocument();
    expect(screen.queryByText('Trace tool updates')).not.toBeInTheDocument();
    expect(container.querySelector('.activity-card[open]')).toBeInTheDocument();
  });

  it('uses a live task-run title without reconstructing extension-specific labels', () => {
    const { container } = render(
      <ChatToolActivity
        tools={[
          {
            callId: 'subagent-1',
            name: 'subagent',
            input: '{"task":"Review the task progress renderer"}',
            inProgress: true,
            result: {
              toolName: 'subagent',
              success: true,
              metadataType: 'extension_tool',
              metadata: {
                extensionId: 'subagent',
                toolName: 'subagent',
                output: 'Delegated task - Review the task progress renderer',
                data: {
                  taskRun: {
                    version: 1,
                    revision: 1,
                    kind: 'subagent',
                    status: 'running',
                    phase: 'starting',
                    title: 'Delegated task',
                    detail: 'Review the task progress renderer',
                    elapsedMs: 1000,
                    counts: { succeeded: 0, failed: 0, running: 0 },
                    activities: [],
                  },
                },
              },
            },
          },
        ]}
      />
    );

    expect(screen.getByText('Delegated task')).toBeInTheDocument();
    expect(container.querySelector('.activity-detail-content')).not.toHaveTextContent(
      'Review the task progress renderer'
    );
    expect(container.querySelector('.task-run-headline')).not.toBeInTheDocument();
  });

  it.each([
    { success: true, status: 'done', marker: '.lucide-check' },
    { success: false, status: 'failed', marker: '.lucide-x' },
  ])('uses a TUI glyph and accessible $status status for a collapsed extension result', async ({
    success,
    status,
    marker,
  }) => {
    const user = userEvent.setup();
    const { container } = render(
      <ChatToolActivity
        tools={[
          {
            callId: 'external-1',
            name: 'repository_search',
            input: '{}',
            result: {
              toolName: 'repository_search',
              success,
              metadataType: 'extension_tool',
              metadata: {
                extensionId: 'search',
                toolName: 'repository_search',
                output: 'Search results',
                data: { presentation: { summary: 'Search repository' } },
              },
            },
          },
        ]}
      />
    );

    expect(screen.getByText('Search repository')).toBeVisible();
    expect(screen.getByLabelText(`Tool ${status}`)).toBeVisible();
    expect(container.querySelector(`summary ${marker}`)).toBeInTheDocument();
    expect(
      container.querySelector('.tool-summary-text svg, .tool-summary-icon')
    ).not.toBeInTheDocument();
    expect(container.querySelector('.spinner-glyph')).not.toBeInTheDocument();
    expect(container.querySelector('details')).not.toHaveAttribute('open');
    expect(screen.getByText('Search results')).not.toBeVisible();

    await user.click(screen.getByText('Search repository'));

    expect(screen.getByText('Search results')).toBeVisible();
  });

  it('uses the shared TUI spinner for a pending extension without inferring an icon from its name or input', () => {
    const { container } = render(
      <ChatToolActivity
        tools={[
          {
            callId: 'runner-1',
            name: 'repository_search',
            input: '{"task":"search the repository"}',
          },
        ]}
      />
    );

    expect(container.querySelector('details')).toHaveAttribute('open');
    expect(container.querySelector('summary .spinner-glyph')).toHaveTextContent('⣾');
    expect(
      container.querySelector('.tool-summary-text svg, .tool-summary-icon')
    ).not.toBeInTheDocument();
    expect(screen.getByLabelText('Tool running')).toBeVisible();
  });

  it('preserves extension presentation and attachments through live updates and completion', async () => {
    const user = userEvent.setup();
    const tool: ChatRenderToolCall = {
      callId: 'review-1',
      name: 'review_repository',
      input: '{}',
      inProgress: true,
      result: {
        toolName: 'review_repository',
        success: true,
        metadataType: 'extension_tool',
        metadata: {
          extensionId: 'review',
          toolName: 'review_repository',
          output: 'Raw extension output',
          data: {
            presentation: {
              summary: 'Review repository',
              body: 'Checking **parser**',
              format: 'markdown',
            },
          },
        },
      },
    };
    const { container, rerender } = render(<ChatToolActivity tools={[tool]} />);

    expect(screen.getByText('Review repository')).toBeVisible();
    expect(screen.getByText('parser')).toBeVisible();
    expect(screen.getByText('parser').tagName).toBe('STRONG');
    expect(container.querySelector('details')).toHaveAttribute('open');
    expect(container.querySelector('summary .spinner-glyph')).toHaveTextContent('⣾');
    expect(screen.queryByText('Raw extension output')).not.toBeInTheDocument();

    const complete: ChatRenderToolCall = {
      ...tool,
      inProgress: false,
      result: {
        toolName: 'review_repository',
        success: true,
        metadataType: 'extension_tool',
        metadata: {
          extensionId: 'review',
          toolName: 'review_repository',
          output: 'Raw final output',
          data: {
            presentation: {
              summary: 'Repository reviewed',
              body: '**Parser looks good.**',
              format: 'markdown',
            },
          },
        },
        attachments: [
          {
            type: 'image',
            artifactId: 'review_1',
            shortCode: 'review',
            mimeType: 'image/png',
            alt: 'Review diagram',
          },
        ],
      },
    };
    rerender(<ChatToolActivity tools={[complete]} />);

    expect(screen.getByText('Repository reviewed')).toBeVisible();
    expect(container.querySelector('details')).not.toHaveAttribute('open');
    expect(container.querySelector('summary .lucide-check')).toBeInTheDocument();
    expect(screen.getByLabelText('Tool done')).toBeVisible();
    expect(screen.queryByLabelText('Tool running')).not.toBeInTheDocument();
    expect(container.querySelector('.spinner-glyph')).not.toBeInTheDocument();
    expect(screen.getByText('Parser looks good.')).not.toBeVisible();
    expect(screen.getByRole('img', { name: 'Review diagram' })).toBeVisible();
    expect(screen.getByRole('img', { name: 'Review diagram' }).closest('details')).toBeNull();

    await user.click(screen.getByText('Repository reviewed'));
    expect(screen.getByText('Parser looks good.')).toBeVisible();
    expect(screen.getByText('Parser looks good.').tagName).toBe('STRONG');
    expect(screen.queryByText('Raw final output')).not.toBeInTheDocument();
    expect(screen.queryByText('parser')).not.toBeInTheDocument();

    rerender(<ChatToolActivity tools={[{ ...complete }]} />);
    expect(container.querySelector('details')).toHaveAttribute('open');
    expect(screen.getByText('Parser looks good.')).toBeVisible();
  });

  it('groups consecutive non-bash builtins with their summaries and results in one disclosure', async () => {
    const user = userEvent.setup();
    const { container } = render(
      <ChatToolActivity
        tools={[
          {
            callId: 'fetch-1',
            name: 'web_fetch',
            input: '{"url":"https://example.com/news"}',
            result: {
              toolName: 'web_fetch',
              success: true,
              metadata: {
                url: 'https://example.com/news',
                content: 'Latest headlines',
                processedType: 'text',
              },
            },
          },
          {
            callId: 'fetch-2',
            name: 'web_fetch',
            input: '{"url":"https://example.com/docs"}',
            result: {
              toolName: 'web_fetch',
              success: true,
              metadata: {
                url: 'https://example.com/docs',
                content: 'Project instructions',
                processedType: 'text',
              },
            },
          },
        ]}
      />
    );
    const group = container.querySelector('details.activity-card.activity-tool-group');

    expect(container.querySelectorAll('details')).toHaveLength(1);
    expect(group).not.toHaveAttribute('open');
    expect(screen.getByText('Ran 2 tools')).toBeVisible();
    expect(screen.getByText('Fetch URL: https://example.com/news')).not.toBeVisible();
    expect(screen.getByText('Fetch URL: https://example.com/docs')).not.toBeVisible();

    await user.click(screen.getByText('Ran 2 tools'));

    expect(group).toHaveAttribute('open');
    expect(screen.getByText('Fetch URL: https://example.com/news')).toBeVisible();
    expect(screen.getByText('Fetch URL: https://example.com/docs')).toBeVisible();
    expect(screen.getByText('Latest headlines')).toBeVisible();
    expect(screen.getByText('Project instructions')).toBeVisible();
    expect(container.querySelector('details details')).not.toBeInTheDocument();
    expect(
      container.querySelector('.tool-summary-text svg, .tool-summary-icon')
    ).not.toBeInTheDocument();
  });

  it.each([
    'skill',
    'get_goal',
    'update_goal',
    'todo_read',
    'todo_write',
    'glob_tool',
    'grep_tool',
    'view_image',
    'openai_web_search',
    'web_fetch',
    'read_conversation',
  ])('groups the %s builtin as a tool rather than an extension', (name) => {
    const { container } = render(
      <ChatToolActivity
        tools={[
          {
            callId: name,
            name,
            input: '{}',
            result: { toolName: name, success: true },
          },
        ]}
      />
    );

    expect(screen.getByText('Ran 1 tool')).toBeVisible();
    expect(container.querySelector('details.activity-tool-group')).not.toHaveAttribute('open');
    expect(container.querySelectorAll('details')).toHaveLength(1);
  });

  it.each([
    true,
    false,
  ])('collapses builtin groups on completion regardless of success (%s)', (success) => {
    const completed: ChatRenderToolCall = {
      callId: 'fetch-1',
      name: 'web_fetch',
      input: '{"url":"https://example.com/news"}',
      result: { toolName: 'web_fetch', success: true },
    };
    const pending: ChatRenderToolCall = {
      callId: 'fetch-2',
      name: 'web_fetch',
      input: '{"url":"https://example.com/docs"}',
    };
    const { container, rerender } = render(<ChatToolActivity tools={[completed, pending]} />);

    expect(screen.getByText('Running 2 tools')).toBeVisible();
    expect(container.querySelector('.activity-tool-group')).toHaveAttribute('open');
    expect(screen.getByText('Fetch URL: https://example.com/news')).toBeVisible();
    expect(screen.getByText('Fetch URL: https://example.com/docs')).toBeVisible();
    expect(screen.getByLabelText('Tool done')).toBeVisible();
    expect(screen.getByLabelText('Tool running')).toBeVisible();
    expect(container.querySelector('details details')).not.toBeInTheDocument();

    rerender(
      <ChatToolActivity
        tools={[completed, { ...pending, result: { toolName: 'web_fetch', success } }]}
      />
    );

    expect(screen.getByText('Ran 2 tools')).toBeVisible();
    expect(container.querySelector('.activity-tool-group')).not.toHaveAttribute('open');
    expect(screen.getByText('Fetch URL: https://example.com/news')).not.toBeVisible();
    expect(screen.getByText('Fetch URL: https://example.com/docs')).not.toBeVisible();
    expect(screen.queryByLabelText('Tool running')).not.toBeInTheDocument();
  });

  it('collapses failed builtin groups with a visible failure count and expandable error', async () => {
    const user = userEvent.setup();
    const { container } = render(
      <ChatToolActivity
        tools={[
          {
            callId: 'fetch-1',
            name: 'web_fetch',
            input: '{"url":"https://example.com/news"}',
            result: { toolName: 'web_fetch', success: false, error: 'Page unavailable.' },
          },
        ]}
      />
    );

    expect(screen.getByText('Ran 1 tool')).toBeVisible();
    expect(container.querySelector('.activity-tool-group')).not.toHaveAttribute('open');
    expect(container.querySelector('summary .lucide-x')).toBeInTheDocument();
    expect(screen.getByText('1 failed')).toBeVisible();
    expect(screen.getByText('Page unavailable.')).not.toBeVisible();

    await user.click(screen.getByText('Ran 1 tool'));

    expect(screen.getByLabelText('Tool failed')).toBeVisible();
    expect(screen.getByText('Page unavailable.')).toBeVisible();
  });

  it('keeps file rows, builtin, command, and extension groups in transcript order', () => {
    const read = (callId: string): ChatRenderToolCall => ({
      callId,
      name: 'file_read',
      input: JSON.stringify({ file_path: `${callId}.md` }),
      result: { toolName: 'file_read', success: true },
    });
    const { container } = render(
      <ChatToolActivity
        tools={[
          read('first'),
          bashTool('pwd'),
          read('second'),
          {
            callId: 'extension-1',
            name: 'review_repository',
            input: '{}',
            result: {
              toolName: 'review_repository',
              success: true,
              metadataType: 'extension_tool',
              metadata: { data: { presentation: { summary: 'Review repository' } } },
            },
          },
          read('third'),
          {
            callId: 'skill-1',
            name: 'skill',
            input: '{}',
            result: { toolName: 'skill', success: true },
          },
        ]}
      />
    );

    const rows = container.querySelectorAll('details');
    expect(rows).toHaveLength(6);
    expect(rows[0]).toHaveClass('activity-file');
    expect(rows[1]).toHaveClass('activity-command-group');
    expect(rows[2]).toHaveClass('activity-file');
    expect(rows[3]).not.toHaveClass('activity-tool-group');
    expect(rows[4]).toHaveClass('activity-file');
    expect(rows[5]).toHaveClass('activity-tool-group');
    expect(screen.getByText('Review repository')).toBeVisible();
    expect(screen.getAllByText('Ran 1 tool')).toHaveLength(1);
    expect(within(rows[0] as HTMLElement).getByText('Read file: first.md')).toBeInTheDocument();
    expect(within(rows[2] as HTMLElement).getByText('Read file: second.md')).toBeInTheDocument();
    expect(within(rows[4] as HTMLElement).getByText('Read file: third.md')).toBeInTheDocument();
  });

  it('keeps consecutive reads visible as separate file rows with expandable content', async () => {
    const user = userEvent.setup();
    const { container } = render(
      <ChatToolActivity
        tools={['README.md', 'AGENTS.md'].map((path) => ({
          callId: path,
          name: 'file_read',
          input: JSON.stringify({ file_path: path }),
          result: {
            toolName: 'file_read',
            success: true,
            metadata: { filePath: path, lines: [`Contents of ${path}`] },
          },
        }))}
      />
    );

    expect(container.querySelectorAll('details.activity-file')).toHaveLength(2);
    expect(container.querySelector('.activity-tool-group')).not.toBeInTheDocument();
    expect(screen.getByText('Read file: README.md')).toBeVisible();
    expect(screen.getByText('Read file: AGENTS.md')).toBeVisible();
    expect(screen.getByText('Contents of README.md')).not.toBeVisible();
    expect(container.querySelector('.file-activity-counts')).not.toBeInTheDocument();

    await user.click(screen.getByText('Read file: README.md'));
    expect(screen.getByText('Contents of README.md')).toBeVisible();
    expect(screen.getByText('Contents of AGENTS.md')).not.toBeVisible();
    expect(container.querySelector('details details')).not.toBeInTheDocument();
  });

  it.each([
    ['file_edit', 'Edit'],
    ['file_write', 'Write'],
  ])('shows %s paths and real line counts without an outer tool group', async (name, label) => {
    const user = userEvent.setup();
    const tool: ChatRenderToolCall = {
      callId: name,
      name,
      input: '{"file_path":"src/app.ts"}',
      result: {
        toolName: name,
        success: true,
        metadata: {
          filePath: '/workspace/src/app.ts',
          unifiedDiff:
            '--- old\n+++ new\n@@ -10 +10,2 @@\n-old\n+new\n+extra\n@@ -20 +21 @@\n--- literal\n+++ literal\n',
        },
      },
    };
    const { container, rerender } = render(<ChatToolActivity tools={[tool]} />);
    const summary = container.querySelector('summary') as HTMLElement;

    expect(screen.getByText(`${label} file: /workspace/src/app.ts`)).toBeVisible();
    expect(within(summary).getByText('+3')).toBeVisible();
    expect(within(summary).getByText('-2')).toBeVisible();
    expect(container.querySelectorAll('details')).toHaveLength(1);
    expect(container.querySelector('details')).not.toHaveAttribute('open');
    expect(screen.getByText('old')).not.toBeVisible();

    await user.click(summary);
    expect(screen.getByText('old')).toBeVisible();
    expect(container.querySelector('.diff-line-removed .diff-line-number')).toHaveTextContent('10');
    expect(container.querySelector('.apply-patch-change-line')).not.toBeInTheDocument();

    rerender(<ChatToolActivity tools={[{ ...tool }, bashTool('pwd')]} />);
    expect(container.querySelector('details.activity-file')).toHaveAttribute('open');
  });

  it('splits multi-file patches into independent write, edit, delete, and move rows', async () => {
    const user = userEvent.setup();
    const { container } = render(
      <ChatToolActivity
        tools={[
          {
            callId: 'patch-1',
            name: 'apply_patch',
            input: '{}',
            result: {
              toolName: 'apply_patch',
              success: true,
              metadata: {
                changes: [
                  { path: 'new.txt', operation: 'add', unifiedDiff: '@@ -0,0 +1 @@\n+created\n' },
                  {
                    path: 'edit.txt',
                    operation: 'update',
                    unifiedDiff: '@@ -1 +1 @@\n-old\n+updated\n',
                  },
                  {
                    path: 'deleted.txt',
                    operation: 'delete',
                    unifiedDiff: '@@ -1 +0,0 @@\n-removed\n',
                  },
                  {
                    path: 'before.txt',
                    movePath: 'after.txt',
                    operation: 'update',
                    unifiedDiff: '',
                  },
                ],
              },
              attachments: [
                {
                  type: 'image',
                  artifactId: 'patch_1',
                  shortCode: 'patch',
                  mimeType: 'image/png',
                  alt: 'Patch preview',
                },
              ],
            },
          },
        ]}
      />
    );

    const summaries = container.querySelectorAll('summary');
    expect(summaries).toHaveLength(4);
    for (const text of [
      'Write file: new.txt',
      'Edit file: edit.txt',
      'Delete file: deleted.txt',
      'Move file: before.txt → after.txt',
    ]) {
      expect(screen.getByText(text)).toBeVisible();
    }
    expect(
      Array.from(summaries).map(
        (summary) => summary.querySelector('.file-activity-counts')?.textContent
      )
    ).toEqual(['(+1 -0)', '(+1 -1)', '(+0 -1)', '(+0 -0)']);
    expect(screen.queryByText(/Ran .* tools?/)).not.toBeInTheDocument();
    await user.click(screen.getByText('Delete file: deleted.txt'));
    expect(screen.getByText('removed')).toBeVisible();
    expect(screen.getByText('created')).not.toBeVisible();
    expect(container.querySelectorAll('details[open]')).toHaveLength(1);
    expect(container.querySelector('details details')).not.toBeInTheDocument();
    expect(screen.getAllByRole('img', { name: 'Patch preview' })).toHaveLength(1);
    expect(screen.getByRole('img', { name: 'Patch preview' }).closest('details')).toBeNull();
  });

  it.each([
    true,
    false,
  ])('keeps pending patch paths visible and collapses on completion (success %s)', async (success) => {
    const user = userEvent.setup();
    const pending: ChatRenderToolCall = {
      callId: 'patch-1',
      name: 'apply_patch',
      input: JSON.stringify({
        input:
          '*** Begin Patch\n*** Update File: old.txt\n*** Move to: new.txt\n@@\n-old\n+new\n*** End Patch',
      }),
    };
    const { container, rerender } = render(<ChatToolActivity tools={[pending]} />);

    expect(screen.getByText('Move file: old.txt → new.txt')).toBeVisible();
    expect(container.querySelector('details')).toHaveAttribute('open');
    expect(container.querySelector('summary .spinner-glyph')).toBeInTheDocument();
    expect(container.querySelector('.file-activity-counts')).not.toBeInTheDocument();

    rerender(
      <ChatToolActivity
        tools={[
          {
            ...pending,
            result: {
              toolName: 'apply_patch',
              success,
              error: success ? undefined : 'Partial patch failed',
              metadata: {
                changes: [
                  {
                    path: 'old.txt',
                    movePath: 'new.txt',
                    operation: 'update',
                    unifiedDiff: '@@ -1 +1 @@\n-old\n+new\n',
                  },
                ],
              },
            },
          },
        ]}
      />
    );

    expect(container.querySelector('details')).not.toHaveAttribute('open');
    expect(screen.getByText('Move file: old.txt → new.txt')).toBeVisible();
    expect(screen.getByText('+1')).toBeVisible();
    expect(container.querySelector('summary .spinner-glyph')).not.toBeInTheDocument();
    if (!success) expect(screen.getByLabelText('Tool failed')).toBeVisible();

    await user.click(screen.getByText('Move file: old.txt → new.txt'));
    expect(screen.getByText('old')).toBeVisible();
    expect(screen.getByText('new')).toBeVisible();
    if (!success) expect(screen.getByRole('alert')).toHaveTextContent('Partial patch failed');
  });

  it.each([
    ['file_read', 'Read'],
    ['file_edit', 'Edit'],
    ['file_write', 'Write'],
  ])('preserves %s paths and errors when results have no metadata', async (name, label) => {
    const user = userEvent.setup();
    const tool: ChatRenderToolCall = { callId: name, name, input: '{"file_path":"README.md"}' };
    const { container, rerender } = render(<ChatToolActivity tools={[tool]} />);
    expect(screen.getByText(`${label} file: README.md`)).toBeVisible();
    expect(screen.getByLabelText('Tool running')).toBeInTheDocument();
    expect(container.querySelector('.file-activity-counts')).not.toBeInTheDocument();

    rerender(
      <ChatToolActivity
        tools={[
          { ...tool, result: { toolName: name, success: false, error: 'Permission denied' } },
        ]}
      />
    );
    expect(screen.getByLabelText('Tool failed')).toBeVisible();
    expect(screen.getByText(`${label} file: README.md`)).toBeVisible();
    await user.click(screen.getByText(`${label} file: README.md`));
    expect(screen.getByText('Permission denied')).toBeVisible();
    expect(container.querySelector('.file-activity-counts')).not.toBeInTheDocument();
  });

  it('does not claim attempted patch files were changed when the result reports no changes', () => {
    const { container } = render(
      <ChatToolActivity
        tools={[
          {
            callId: 'patch-1',
            name: 'apply_patch',
            input: JSON.stringify({
              input: '*** Begin Patch\n*** Update File: README.md\n*** End Patch',
            }),
            result: { toolName: 'apply_patch', success: true, metadata: { changes: [] } },
          },
        ]}
      />
    );
    expect(screen.queryByText('Edit file: README.md')).not.toBeInTheDocument();
    expect(screen.getByText('No files were modified.')).toBeInTheDocument();
    expect(container.querySelector('details')).toHaveClass('activity-file');
    expect(container.querySelector('.file-activity-counts')).not.toBeInTheDocument();
  });

  it('preserves extension-owned presentations even when their tool name matches a file tool', () => {
    const { container } = render(
      <ChatToolActivity
        tools={[
          {
            callId: 'extension-edit',
            name: 'file_edit',
            input: '{"file_path":"README.md"}',
            result: {
              toolName: 'file_edit',
              success: true,
              metadataType: 'extension_tool',
              metadata: {
                data: {
                  presentation: { summary: 'Review proposed edit', body: 'Awaiting approval' },
                },
              },
            },
          },
        ]}
      />
    );
    expect(screen.getByText('Review proposed edit')).toBeVisible();
    expect(container.querySelector('.activity-file')).not.toBeInTheDocument();
  });
});
