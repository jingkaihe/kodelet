import { describe, it, expect } from 'vitest';
import { render, screen, fireEvent } from '@testing-library/react';
import FallbackRenderer from './FallbackRenderer';
import { ToolResult } from '../../types';

describe('FallbackRenderer', () => {
  const createToolResult = (toolName: string, metadata: Record<string, unknown> | null | undefined): ToolResult => ({
    toolName,
    success: true,
    error: undefined,
    timestamp: '2023-01-01T00:00:00Z',
    metadata: metadata as Record<string, unknown> | undefined,
  });

  it('shows completed status text', () => {
    const toolResult = createToolResult('arbitrary-tool', { key: 'value' });
    render(<FallbackRenderer toolResult={toolResult} />);
    expect(screen.getByText('completed')).toBeInTheDocument();
  });

  it('shows "Show raw data" button', () => {
    const toolResult = createToolResult('test-tool', {});
    render(<FallbackRenderer toolResult={toolResult} />);
    expect(screen.getByText('Show raw data')).toBeInTheDocument();
  });

  it('reveals raw JSON when button is clicked', () => {
    const metadata = { key: 'value', number: 42 };
    const toolResult = createToolResult('test-tool', metadata);
    render(<FallbackRenderer toolResult={toolResult} />);

    fireEvent.click(screen.getByText('Show raw data'));

    const pre = document.querySelector('pre');
    expect(pre?.textContent).toBe(JSON.stringify(metadata, null, 2));
  });

  it('handles null metadata', () => {
    const toolResult = createToolResult('null-tool', null);
    render(<FallbackRenderer toolResult={toolResult} />);

    fireEvent.click(screen.getByText('Show raw data'));

    const pre = document.querySelector('pre code');
    expect(pre?.textContent).toBe('null');
  });

  it('handles empty object metadata', () => {
    const toolResult = createToolResult('empty-tool', {});
    render(<FallbackRenderer toolResult={toolResult} />);

    fireEvent.click(screen.getByText('Show raw data'));

    const pre = document.querySelector('pre code');
    expect(pre?.textContent).toBe('{}');
  });

  it('handles circular reference gracefully', () => {
    const metadata: Record<string, unknown> = { key: 'value' };
    metadata.circular = metadata;

    const toolResult = createToolResult('circular-tool', metadata);
    render(<FallbackRenderer toolResult={toolResult} />);

    fireEvent.click(screen.getByText('Show raw data'));
    expect(screen.getByText(/\[Circular\]/)).toBeInTheDocument();
  });
});
