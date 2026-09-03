import { render, screen } from '@testing-library/react'
import { describe, expect, it } from 'vitest'
import OpenAIWebSearchRenderer from './OpenAIWebSearchRenderer'
import { ToolResult } from '../../types'

describe('OpenAIWebSearchRenderer', () => {
  it('renders only additional queries and deduplicated links without cards', () => {
    const toolResult: ToolResult = {
      toolName: 'openai_web_search',
      success: true,
      timestamp: '2026-04-03T00:00:00Z',
      metadata: {
        status: 'completed',
        action: 'search',
        queries: ['kodelet web ui search tool', 'kodelet search renderer'],
        sources: ['https://example.com/shared', 'https://example.com/source'],
        results: ['https://example.com/shared', 'https://example.com/result'],
      },
    }

    const { container } = render(
      <OpenAIWebSearchRenderer
        toolInput='{"type":"search","query":"kodelet web ui search tool"}'
        toolResult={toolResult}
      />
    )

    expect(screen.queryByText('kodelet web ui search tool')).not.toBeInTheDocument()
    expect(screen.getByText('Additional queries')).toBeInTheDocument()
    expect(screen.getByText('kodelet search renderer')).toBeInTheDocument()
    expect(screen.getByText('Links')).toBeInTheDocument()
    expect(screen.getByText('https://example.com/shared')).toBeInTheDocument()
    expect(screen.getByText('https://example.com/source')).toBeInTheDocument()
    expect(screen.getByText('https://example.com/result')).toBeInTheDocument()
    expect(screen.getAllByRole('link')).toHaveLength(3)
    expect(container.querySelector('.tool-kv-grid')).not.toBeInTheDocument()
    expect(container.querySelector('.tool-code-list')).not.toBeInTheDocument()
  })

  it('does not repeat find-in-page targets already shown in the activity title', () => {
    const toolResult: ToolResult = {
      toolName: 'openai_web_search',
      success: true,
      timestamp: '2026-04-03T00:00:00Z',
      metadata: {
        status: 'completed',
        action: 'find_in_page',
        url: 'https://example.com/docs',
        pattern: 'allowed_tools',
      },
    }

    render(<OpenAIWebSearchRenderer toolResult={toolResult} />)

    expect(screen.queryByText('allowed_tools in https://example.com/docs')).not.toBeInTheDocument()
    expect(screen.queryByText('https://example.com/docs')).not.toBeInTheDocument()
    expect(screen.queryByText('allowed_tools')).not.toBeInTheDocument()
    expect(screen.getByText('No additional details')).toBeInTheDocument()
  })

  it('does not repeat open-page URLs recovered from tool input', () => {
    const toolResult: ToolResult = {
      toolName: 'openai_web_search',
      success: true,
      timestamp: '2026-04-03T00:00:00Z',
      metadata: {
        status: 'completed',
        action: 'open_page',
      },
    }

    render(
      <OpenAIWebSearchRenderer
        toolInput='{"type":"open_page","url":"https://example.com/story"}'
        toolResult={toolResult}
      />
    )

    expect(screen.queryByText('https://example.com/story')).not.toBeInTheDocument()
    expect(screen.getByText('No additional details')).toBeInTheDocument()
  })

  it('renders an explicit note when OpenAI omits an open-page URL', () => {
    const toolResult: ToolResult = {
      toolName: 'openai_web_search',
      success: true,
      timestamp: '2026-04-03T00:00:00Z',
      metadata: {
        status: 'completed',
        action: 'open_page',
      },
    }

    render(
      <OpenAIWebSearchRenderer
        toolInput='{"type":"open_page","status":"completed"}'
        toolResult={toolResult}
      />
    )

    expect(
      screen.getByText('OpenAI did not include the page URL for this open-page action.')
    ).toBeInTheDocument()
  })
})
