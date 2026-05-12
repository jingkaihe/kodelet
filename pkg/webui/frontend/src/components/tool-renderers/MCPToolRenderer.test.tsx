import { describe, expect, it } from 'vitest';
import { render, screen } from '@testing-library/react';
import MCPToolRenderer from './MCPToolRenderer';
import { ToolResult } from '../../types';

describe('MCPToolRenderer', () => {
  it('renders MCP parameters and content with the compact tool style', () => {
    const toolResult: ToolResult = {
      toolName: 'mcp__lsp_definition',
      success: true,
      timestamp: '2026-05-12T00:00:00Z',
      metadata: {
        mcpToolName: 'definition',
        serverName: 'lsp',
        parameters: { symbol: 'main' },
        content: [{ type: 'text', text: 'main is defined in main.go' }],
      },
    };

    const { container } = render(<MCPToolRenderer toolResult={toolResult} />);

    expect(screen.getByText('definition')).toBeInTheDocument();
    expect(screen.getByText('lsp')).toBeInTheDocument();
    expect(container.querySelector('.tool-code-block code')?.textContent).toBe(
      '{\n  "symbol": "main"\n}'
    );
    expect(screen.getByText('main is defined in main.go')).toBeInTheDocument();
  });
});
