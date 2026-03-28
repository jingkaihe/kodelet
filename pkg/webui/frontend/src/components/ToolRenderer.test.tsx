import { render, screen } from '@testing-library/react';
import { describe, expect, it } from 'vitest';
import ToolRenderer from './ToolRenderer';
import { ToolResult } from '../types';

describe('ToolRenderer', () => {
  it('uses the bash renderer for failed bash commands so output is still visible', () => {
    const toolResult: ToolResult = {
      toolName: 'bash',
      success: false,
      error: 'Command exited with status 1',
      timestamp: '2023-01-01T00:00:00Z',
      metadata: {
        command: 'cat missing-file',
        exitCode: 1,
        output: 'cat: missing-file: No such file or directory',
      },
    };

    const { container } = render(<ToolRenderer toolResult={toolResult} />);

    expect(screen.getByText('cat missing-file')).toBeInTheDocument();
    expect(screen.getByText('exit 1')).toBeInTheDocument();
    expect(screen.getByText('cat: missing-file: No such file or directory')).toBeInTheDocument();
    expect(container.querySelector('.tool-terminal')).toBeInTheDocument();
    expect(screen.queryByText('Error (bash):')).not.toBeInTheDocument();
  });

  it('keeps the generic error renderer for other failed tools', () => {
    const toolResult: ToolResult = {
      toolName: 'file_read',
      success: false,
      error: 'permission denied',
      timestamp: '2023-01-01T00:00:00Z',
      metadata: {
        filePath: '/tmp/secret.txt',
      },
    };

    render(<ToolRenderer toolResult={toolResult} />);

    expect(screen.getByText('Error (file_read):')).toBeInTheDocument();
    expect(screen.getByText('permission denied')).toBeInTheDocument();
  });
});
