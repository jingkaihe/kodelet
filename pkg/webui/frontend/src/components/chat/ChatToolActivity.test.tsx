import { render, screen } from '@testing-library/react';
import { describe, expect, it } from 'vitest';
import type { ChatRenderToolCall } from '../../types';
import ChatToolActivity, {
  formatToolInputPreview,
  getToolActivityStatus,
  getToolSummary,
} from './ChatToolActivity';

describe('ChatToolActivity', () => {
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

  it('renders a successful bash result with duration status and tool details', () => {
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

    expect(screen.getByText('Bash: pwd')).toBeInTheDocument();
    expect(screen.getByLabelText('Tool 119ms')).toHaveTextContent('119ms');
    expect(screen.getByText('Print working directory')).toBeInTheDocument();
    expect(screen.getByText('/workspace/kodelet')).toBeInTheDocument();
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

    expect(screen.getByLabelText('Tool running')).toHaveTextContent('running');
    expect(screen.getByText('partial output')).toBeInTheDocument();
    expect(screen.queryByText('Awaiting tool result…')).not.toBeInTheDocument();
    expect(container.querySelector('.activity-card-live')).toBeInTheDocument();
  });

  it('renders failed tool results with error styling and failed status', () => {
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

    expect(screen.getByText('Bash: false')).toBeInTheDocument();
    expect(screen.getByLabelText('Tool failed')).toHaveTextContent('failed');
    expect(screen.getByText('Command exited with status 1.')).toBeInTheDocument();
    expect(container.querySelector('.activity-card-error')).toBeInTheDocument();
    expect(container.querySelector('.tool-summary-icon-error')).toBeInTheDocument();
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
    ['repository_search', '.lucide-search'],
    ['reviewAgent', '.lucide-file-cog'],
    ['review-task', '.lucide-file-cog'],
    ['remote_command', '.lucide-square-terminal'],
    ['URLFetch', '.lucide-globe'],
    ['config-edit', '.lucide-file-pen'],
    ['XMLReader', '.lucide-file-text'],
  ])('infers the semantic icon for external tool %s', (name, expectedIcon) => {
    const { container } = render(
      <ChatToolActivity
        tools={[
          {
            callId: 'external-1',
            name,
            input: '{}',
            result: {
              toolName: name,
              success: true,
              metadataType: 'extension_tool',
            },
          },
        ]}
      />
    );

    expect(container.querySelector(expectedIcon)).toBeInTheDocument();
  });

  it.each([
    ['http_search', '.lucide-globe'],
    ['https_search', '.lucide-globe'],
    ['fetch_search_results', '.lucide-globe'],
    ['web_code_search', '.lucide-search'],
    ['web_preview', '.lucide-globe'],
  ])('applies semantic category priority for external tool %s', (name, expectedIcon) => {
    const { container } = render(
      <ChatToolActivity
        tools={[
          {
            callId: 'external-1',
            name,
            input: '{}',
            result: {
              toolName: name,
              success: true,
              metadataType: 'extension_tool',
            },
          },
        ]}
      />
    );

    expect(container.querySelector(expectedIcon)).toBeInTheDocument();
  });

  it.each([
    ['openai_image_generation', '.lucide-file-text'],
    ['OpenAIImageGeneration', '.lucide-file-text'],
    ['openAIImageGeneration', '.lucide-file-text'],
    ['OpenAPIValidator', '.lucide-file-text'],
    ['OpenSSLScan', '.lucide-file-text'],
    ['executive_summary', '.lucide-square-terminal'],
    ['editorial_calendar', '.lucide-file-pen'],
    ['readiness_probe', '.lucide-file-text'],
  ])('avoids prefix false positives for external tool %s', (name, incorrectIcon) => {
    const { container } = render(
      <ChatToolActivity
        tools={[
          {
            callId: 'external-1',
            name,
            input: '{}',
            result: {
              toolName: name,
              success: true,
              metadataType: 'extension_tool',
            },
          },
        ]}
      />
    );

    expect(container.querySelector('.lucide-wrench')).toBeInTheDocument();
    expect(container.querySelector(incorrectIcon)).not.toBeInTheDocument();
  });

  it.each([
    ['file_write', '{"file_path":"README.md"}', '.lucide-file-plus', '.lucide-file-pen'],
    [
      'apply_patch',
      '{"input":"*** Begin Patch\\n*** Update File: README.md\\n*** End Patch"}',
      '.lucide-pencil',
      '.lucide-file-pen',
    ],
    ['view_image', '{"path":"screenshot.png"}', '.lucide-file-image', '.lucide-file-text'],
    ['openai_web_search', '{}', '.lucide-globe', '.lucide-search'],
  ])('preserves the explicit built-in icon for %s', (name, input, expectedIcon, inferredIcon) => {
    const { container } = render(
      <ChatToolActivity
        tools={[
          {
            callId: 'builtin-1',
            name,
            input,
          },
        ]}
      />
    );

    expect(container.querySelector(expectedIcon)).toBeInTheDocument();
    expect(container.querySelector(inferredIcon)).not.toBeInTheDocument();
  });

  it('uses a generic icon for a running unmatched external tool without inspecting its input', () => {
    const { container } = render(
      <ChatToolActivity
        tools={[
          {
            callId: 'runner-1',
            name: 'runner',
            input: '{"task":"search the repository"}',
          },
        ]}
      />
    );

    expect(container.querySelector('.lucide-wrench')).toBeInTheDocument();
    expect(container.querySelector('.lucide-search')).not.toBeInTheDocument();
    expect(container.querySelector('.lucide-file-cog')).not.toBeInTheDocument();
  });
});
