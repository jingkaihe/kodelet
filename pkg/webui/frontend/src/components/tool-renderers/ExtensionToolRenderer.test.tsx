import { describe, expect, it } from 'vitest';
import { render, screen } from '@testing-library/react';
import ExtensionToolRenderer from './ExtensionToolRenderer';
import { ToolResult } from '../../types';

describe('ExtensionToolRenderer', () => {
  it('pretty-prints JSON output from extension tools', () => {
    const toolResult: ToolResult = {
      toolName: 'git_info',
      success: true,
      timestamp: '2026-05-12T00:00:00Z',
      metadata: {
        type: 'extension_tool',
        extensionID: 'git',
        toolName: 'git_info',
        output: '{"branch":"main","changes":0}',
      },
    };

    const { container } = render(<ExtensionToolRenderer toolResult={toolResult} />);

    expect(screen.getByText('git_info')).toBeInTheDocument();
    expect(container.querySelector('.tool-code-block code')?.textContent).toBe(
      '{\n  "branch": "main",\n  "changes": 0\n}'
    );
  });
});
