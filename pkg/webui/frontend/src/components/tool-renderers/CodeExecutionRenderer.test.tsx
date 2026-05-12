import { describe, expect, it } from 'vitest';
import { render, screen } from '@testing-library/react';
import CodeExecutionRenderer from './CodeExecutionRenderer';
import { ToolResult } from '../../types';

describe('CodeExecutionRenderer', () => {
  it('renders full code and terminal output without raw JSON blobs', () => {
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

    const { container } = render(
      <CodeExecutionRenderer
        toolInput='{"code_path":"scripts/hello.ts","description":"Say hello"}'
        toolResult={toolResult}
      />
    );

    expect(screen.getByText('executed')).toBeInTheDocument();
    expect(screen.getByText('Node.js v22')).toBeInTheDocument();
    expect(screen.getByText('scripts/hello.ts')).toBeInTheDocument();
    expect(screen.getByText('Say hello')).toBeInTheDocument();
    expect(screen.getByText('Code')).toBeInTheDocument();
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

  it('shows full code without redundant preview controls', () => {
    const code = Array.from({ length: 30 }, (_, index) => `console.log(${index + 1})`).join('\n');
    const toolResult: ToolResult = {
      toolName: 'code_execution',
      success: true,
      timestamp: '2026-05-12T00:00:00Z',
      metadata: {
        code,
        output: 'done',
      },
    };

    const { container } = render(<CodeExecutionRenderer toolResult={toolResult} />);

    expect(container.querySelector('.code-execution-preview')?.textContent).toContain('console.log(18)');
    expect(container.querySelector('.code-execution-preview')?.textContent).toContain('console.log(30)');
    expect(screen.queryByText(/more lines hidden/i)).not.toBeInTheDocument();
    expect(screen.queryByRole('button', { name: /show/i })).not.toBeInTheDocument();
  });

  it('removes redundant markdown result headings from output', () => {
    const toolResult: ToolResult = {
      toolName: 'code_execution',
      success: true,
      timestamp: '2026-05-12T00:00:00Z',
      metadata: {
        output: '### Result\n[{"ok":true}]',
      },
    };

    const { container } = render(<CodeExecutionRenderer toolResult={toolResult} />);

    expect(container.textContent).not.toContain('### Result');
    expect(container.querySelector('.tool-code-block code')?.textContent).toBe('[\n  {\n    "ok": true\n  }\n]');
  });
});
