import { render, screen } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { describe, expect, it } from 'vitest';
import type { CodeExecutionMetadata, ToolResult } from '../../types';
import ChatToolActivity from '../chat/ChatToolActivity';
import ToolRenderer from '../ToolRenderer';
import CodeExecutionRenderer, { codeExecutionSummary } from './CodeExecutionRenderer';

const result: ToolResult = {
  toolName: 'code_execute',
  metadataType: 'code_execute',
  success: true,
  metadata: {
    status: 'completed',
    durationMs: 12,
    outputs: ['Selected output'],
    calls: [
      { callId: 'one', toolName: 'search', status: 'completed', durationMs: 4 },
      { callId: 'two', toolName: 'write', status: 'blocked', durationMs: 0, errorKind: 'blocked' },
    ],
  },
};

const imageShapedJSON = { type: 'image', artifactId: 'art_unselected' };
const typedResult: ToolResult = {
  ...result,
  metadata: {
    ...(result.metadata as CodeExecutionMetadata),
    items: [
      { type: 'json', value: 'Before' },
      { type: 'image', artifactId: 'art_viewed', detail: 'original' },
      { type: 'json', value: imageShapedJSON },
      { type: 'artifact', artifactId: 'art_retained' },
      { type: 'json', value: 'After' },
    ],
  },
  attachments: ['viewed', 'retained', 'unselected'].map((name) => ({
    type: 'image',
    artifactId: `art_${name}`,
    shortCode: name,
    mimeType: 'image/png',
    alt: name,
  })),
};

describe('CodeExecutionRenderer', () => {
  it('renders selected output and handled child failures after history reload', () => {
    const restored = JSON.parse(JSON.stringify(result));
    restored.metadata.calls[1].detailsOmitted = true;
    render(<CodeExecutionRenderer toolResult={restored} />);
    expect(screen.getByText('Selected output', { selector: 'code' })).toBeInTheDocument();
    expect(screen.getByText('search · completed')).toBeInTheDocument();
    expect(screen.getByText('write · blocked')).toBeInTheDocument();
    expect(
      screen.getByText('Child tool details were not saved for this invocation.')
    ).toBeInTheDocument();
    expect(screen.getByText('Child tool details exceeded the storage limit.')).toBeInTheDocument();
    expect(codeExecutionSummary(restored)).toBe('Code execution · 1 succeeded · 1 failed');
  });

  it.each([
    [[], 'Code execution'],
    [['completed'], 'Code execution · 1 succeeded'],
    [['blocked'], 'Code execution · 1 failed'],
    [['queued', 'running'], 'Code execution · 2 running'],
  ])('omits zero counts for %j', (statuses, expected) => {
    expect(
      codeExecutionSummary({
        ...result,
        metadata: {
          ...(result.metadata as CodeExecutionMetadata),
          calls: statuses.map((status, i) => ({
            callId: `${i}`,
            toolName: 'bash',
            status,
            durationMs: 0,
          })),
        },
      })
    ).toBe(expected);
  });

  it('renders parent errors and live snapshots', () => {
    render(
      <CodeExecutionRenderer
        isPartial
        toolResult={{ ...result, success: false, error: 'Timed out' }}
      />
    );
    expect(
      screen.getAllByText('Child tool details are available when code execution finishes.')
    ).toHaveLength(2);
    expect(screen.getByText('Timed out')).toBeInTheDocument();
  });

  it('shows code for a pending invocation without metadata', async () => {
    expect(codeExecutionSummary()).toBe('Code execution');
    const { container } = render(
      <ChatToolActivity
        tools={[
          {
            callId: 'pending',
            name: 'code_execute',
            input: '{"code":"return 42;"}',
          },
        ]}
      />
    );
    await userEvent.click(screen.getByText('Code', { exact: true }));
    expect(container.querySelector('code.language-javascript')).toBeVisible();
    expect(container.querySelector('code.language-javascript')).toHaveTextContent('return 42;');
  });

  it('shows a withheld result when policy strips metadata', () => {
    render(
      <CodeExecutionRenderer
        toolResult={{
          toolName: 'code_execute',
          success: false,
          error: 'Output withheld by policy',
        }}
      />
    );
    expect(screen.getByText('Output withheld by policy')).toBeInTheDocument();
  });

  it('renders ordered typed outputs once, without interpreting ordinary JSON as media', () => {
    const restored = JSON.parse(
      JSON.stringify({ ...typedResult, toolName: 'Code_execute', metadataType: undefined })
    );
    const { container } = render(<ToolRenderer toolResult={restored} />);

    expect(
      Array.from(container.querySelectorAll('pre, img'), (node) =>
        node.tagName === 'IMG' ? node.getAttribute('src') : node.textContent
      )
    ).toEqual([
      'Before',
      '/i/viewed',
      JSON.stringify(imageShapedJSON, null, 2),
      '/i/retained',
      'After',
    ]);
    expect(screen.getByText('Image sent to model · Original detail')).toBeVisible();
    expect(screen.getByText('Retained artifact (not sent to model)')).toBeVisible();
    expect(screen.getByRole('link', { name: 'Download image: retained' })).toHaveAttribute(
      'href',
      '/i/retained?download=1'
    );
    expect(screen.queryByText('Selected output', { selector: 'code' })).not.toBeInTheDocument();
  });

  it('requires matching authorized attachments and hides transient image previews', () => {
    const { rerender } = render(<ToolRenderer toolResult={typedResult} isPartial />);
    expect(screen.queryByRole('img')).not.toBeInTheDocument();
    rerender(<ToolRenderer toolResult={{ ...typedResult, attachments: [] }} />);
    expect(screen.queryByRole('img')).not.toBeInTheDocument();
    expect(screen.queryByRole('link')).not.toBeInTheDocument();
    expect(screen.getAllByText('Image preview unavailable.')).toHaveLength(2);
  });

  it('keeps a single inline gallery when code cards are expanded and collapsed', async () => {
    const user = userEvent.setup();
    const { container } = render(
      <ChatToolActivity
        tools={[{ callId: 'code-1', name: 'code_execute', input: '{}', result: typedResult }]}
      />
    );
    const details = container.querySelector('details');
    const summary = container.querySelector('summary');
    expect(details).toHaveAttribute('open');
    expect(container.querySelectorAll('img')).toHaveLength(2);
    const preview = screen.getByRole('img', { name: 'retained' });
    expect(preview.closest('details')?.querySelector('summary')).toHaveTextContent('Code');
    expect(preview).toBeVisible();
    if (!summary) throw new Error('Expected a code execution card');
    await user.click(summary);
    expect(details).not.toHaveAttribute('open');
    expect(preview).not.toBeVisible();
    expect(container.querySelectorAll('img')).toHaveLength(2);
    await user.click(summary);
    expect(preview).toBeVisible();
    expect(container.querySelectorAll('img')).toHaveLength(2);
  });

  it('nests highlighted code and ordinary child visuals without expanding text output', async () => {
    const user = userEvent.setup();
    const code = 'const reply = await tools.bash({command: "echo done"});';
    const command = {
      callId: 'command',
      toolName: 'bash',
      status: 'unknown',
      durationMs: 4,
      input: { command: 'echo done', description: 'Confirm command' },
      result: {
        toolName: 'bash',
        success: true,
        metadata: { command: 'echo done', output: 'done', exitCode: 0 },
      },
    };
    const { container } = render(
      <ChatToolActivity
        tools={[
          {
            callId: 'code',
            name: 'Code_execute',
            input: JSON.stringify({ code }),
            result: {
              ...result,
              toolName: 'Code_execute',
              metadataType: undefined,
              metadata: {
                status: 'completed',
                durationMs: 12,
                items: [{ type: 'json', value: 'Selected' }],
                calls: [
                  command,
                  { ...command, callId: 'command-2' },
                  {
                    callId: 'patch',
                    toolName: 'apply_patch',
                    status: 'completed',
                    durationMs: 4,
                    result: {
                      toolName: 'apply_patch',
                      success: true,
                      metadata: {
                        changes: [
                          {
                            path: 'test.js',
                            operation: 'update',
                            unifiedDiff: '@@ -1 +1 @@\n-old\n+new\n',
                          },
                        ],
                      },
                    },
                  },
                  { ...command, callId: 'command-3' },
                ],
              },
            },
          },
        ]}
      />
    );
    expect(screen.queryByText('Show raw data')).not.toBeInTheDocument();
    await user.click(screen.getByText(/^Code execution ·/));
    expect(screen.getByText('Selected')).not.toBeVisible();
    await user.click(screen.getByText('Code', { exact: true }));
    expect(container.querySelector('code.language-javascript')).toHaveTextContent(code);
    expect(container.querySelector('.token.keyword')).toBeVisible();
    expect(screen.getByText('Selected')).toBeVisible();
    expect(screen.queryByText('Selected output')).not.toBeInTheDocument();
    expect(screen.getByText('Selected').closest('details')).toBe(
      screen.getByText('Code', { exact: true }).closest('details')
    );
    expect(container.querySelector('.tool-terminal')).not.toBeVisible();
    expect(
      Array.from(
        container.querySelectorAll('summary .tool-summary-label'),
        (node) => node.textContent
      ).filter((text) => /^(Ran|Apply patch)/.test(text ?? ''))
    ).toEqual(['Ran 2 commands', 'Apply patch', 'Ran 1 command']);
    await user.click(screen.getByText('Ran 2 commands'));
    expect(container.querySelector('.tool-terminal')).toBeVisible();
    expect(container.querySelector('.bash-tool-badge')).toHaveClass('is-error');
    const commands = screen.getAllByText('Confirm command');
    expect(commands[0]).toBeVisible();
    expect(commands[1]).toBeVisible();
    expect(commands[2]).not.toBeVisible();
    await user.click(screen.getByText('Apply patch', { exact: true }));
    await user.click(screen.getByText('test.js'));
    expect(container.querySelector('.diff-block')).toBeVisible();
  });
});
