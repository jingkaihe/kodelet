import { describe, expect, it } from 'vitest';
import { render, screen } from '@testing-library/react';
import CodeExecutionRenderer from './CodeExecutionRenderer';
import { ToolResult } from '../../types';

describe('CodeExecutionRenderer', () => {
  it('renders code and terminal output without raw JSON blobs', () => {
    const toolResult: ToolResult = {
      toolName: 'code_execution',
      success: true,
      timestamp: '2026-05-12T00:00:00Z',
      metadata: {
        runtime: 'Node.js v22',
        code: "console.log('hello')",
        output: 'hello\n',
      },
    };

    const { container } = render(<CodeExecutionRenderer toolResult={toolResult} />);

    expect(screen.getByText('executed')).toBeInTheDocument();
    expect(screen.getByText('Node.js v22')).toBeInTheDocument();
    expect(screen.getByText("console.log('hello')")).toBeInTheDocument();
    expect(screen.getByText('hello')).toBeInTheDocument();
    expect(container.querySelector('.tool-terminal')).toBeInTheDocument();
  });

  it('formats JSON object output exactly when output parses as JSON', () => {
    const toolResult: ToolResult = {
      toolName: 'code_execution',
      success: true,
      timestamp: '2026-05-12T00:00:00Z',
      metadata: {
        output: '{"ok":true,"count":2}',
      },
    };

    const { container } = render(<CodeExecutionRenderer toolResult={toolResult} />);

    expect(container.querySelector('.tool-code-block code')?.textContent).toBe(
      '{\n  "ok": true,\n  "count": 2\n}'
    );
  });
});
