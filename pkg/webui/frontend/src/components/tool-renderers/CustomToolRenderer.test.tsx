import { describe, expect, it } from 'vitest';
import { render, screen } from '@testing-library/react';
import CustomToolRenderer from './CustomToolRenderer';
import { ToolResult } from '../../types';

describe('CustomToolRenderer', () => {
  it('pretty-prints JSON output from custom tools', () => {
    const toolResult: ToolResult = {
      toolName: 'custom_tool_git_info',
      success: true,
      timestamp: '2026-05-12T00:00:00Z',
      metadata: {
        output: '{"branch":"main","changes":0}',
      },
    };

    const { container } = render(<CustomToolRenderer toolResult={toolResult} />);

    expect(screen.getByText('git_info')).toBeInTheDocument();
    expect(container.querySelector('.tool-code-block code')?.textContent).toBe(
      '{\n  "branch": "main",\n  "changes": 0\n}'
    );
  });
});
