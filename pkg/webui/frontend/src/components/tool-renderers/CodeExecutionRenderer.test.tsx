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
    render(<CodeExecutionRenderer toolResult={restored} />);
    expect(screen.getByText('Selected output')).toBeInTheDocument();
    expect(screen.getByText('search')).toBeInTheDocument();
    expect(screen.getByText('write')).toBeInTheDocument();
    expect(codeExecutionSummary(restored)).toBe(
      'Code execution · 1 succeeded · 1 failed · 0 running'
    );
  });

  it('renders parent errors and live snapshots', () => {
    render(
      <CodeExecutionRenderer
        isPartial
        toolResult={{ ...result, success: false, error: 'Timed out' }}
      />
    );
    expect(screen.getByText('Running')).toBeInTheDocument();
    expect(screen.getByText('Timed out')).toBeInTheDocument();
  });

  it('labels a pending invocation without metadata', () => {
    expect(codeExecutionSummary()).toBe('Code execution');
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
    const restored = JSON.parse(JSON.stringify(typedResult));
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
    expect(screen.queryByText('Selected output')).not.toBeInTheDocument();
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
    expect(preview.closest('details')).toBe(details);
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
});
