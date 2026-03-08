import { describe, expect, it } from 'vitest';
import { render, screen } from '@testing-library/react';
import FileWriteRenderer from './FileWriteRenderer';
import { ToolResult } from '../../types';

describe('FileWriteRenderer', () => {
  const createToolResult = (
    metadata: Record<string, unknown> | null | undefined
  ): ToolResult => ({
    toolName: 'file_write',
    success: true,
    timestamp: '2023-01-01T00:00:00Z',
    metadata: metadata as Record<string, unknown> | undefined,
  });

  it('returns null when metadata is missing', () => {
    const toolResult = createToolResult(undefined);
    const { container } = render(<FileWriteRenderer toolResult={toolResult} />);
    expect(container.firstChild).toBeNull();
  });

  it('renders file metadata and the written badge', () => {
    const toolResult = createToolResult({
      filePath: '/src/app.js',
      size: 1536,
    });

    render(<FileWriteRenderer toolResult={toolResult} />);

    expect(screen.getByText('/src/app.js')).toBeInTheDocument();
    expect(screen.getByText('Written')).toBeInTheDocument();
    expect(screen.getByText('javascript')).toBeInTheDocument();
    expect(screen.getByText('1.5 KB')).toBeInTheDocument();
  });

  it('renders written content in the shared code block', () => {
    const toolResult = createToolResult({
      filePath: '/test.js',
      content: 'const x = 1;\nconst y = 2;',
    });

    const { container } = render(<FileWriteRenderer toolResult={toolResult} />);

    expect(container.querySelector('.tool-code-block')).toBeInTheDocument();
    expect(container.querySelector('.tool-code-block code')?.textContent).toBe(
      'const x = 1;\nconst y = 2;'
    );
  });

  it('omits the code block when content is unavailable', () => {
    const toolResult = createToolResult({
      filePath: '/test.txt',
    });

    const { container } = render(<FileWriteRenderer toolResult={toolResult} />);

    expect(container.querySelector('.tool-code-block')).not.toBeInTheDocument();
  });
});
