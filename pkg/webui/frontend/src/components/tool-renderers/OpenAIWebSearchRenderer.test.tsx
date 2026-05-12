import { render, screen } from '@testing-library/react'
import { describe, expect, it } from 'vitest'
import OpenAIWebSearchRenderer from './OpenAIWebSearchRenderer'
import { ToolResult } from '../../types'

describe('OpenAIWebSearchRenderer', () => {
  it('renders queries and discovered links', () => {
    const toolResult: ToolResult = {
      toolName: 'openai_web_search',
      success: true,
      timestamp: '2026-04-03T00:00:00Z',
      metadata: {
        status: 'completed',
        action: 'search',
        queries: ['kodelet web ui search tool'],
        sources: ['https://example.com/source'],
        results: ['https://example.com/result'],
      },
    }

    render(<OpenAIWebSearchRenderer toolResult={toolResult} />)

    expect(screen.getByText('completed')).toBeInTheDocument()
    expect(screen.getByText('Search')).toBeInTheDocument()
    expect(screen.getAllByText('kodelet web ui search tool')).toHaveLength(2)
    expect(screen.getByText('https://example.com/source')).toBeInTheDocument()
    expect(screen.getByText('https://example.com/result')).toBeInTheDocument()
  })

  it('renders URL and pattern metadata for find-in-page actions', () => {
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

    expect(screen.getByText('allowed_tools in https://example.com/docs')).toBeInTheDocument()
    expect(screen.getByText('https://example.com/docs')).toBeInTheDocument()
    expect(screen.getByText('allowed_tools')).toBeInTheDocument()
    expect(screen.getByText('Find in page')).toBeInTheDocument()
  })

  it('uses tool input as a fallback for open-page details', () => {
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

    expect(screen.getByText('Open page')).toBeInTheDocument()
    expect(screen.getAllByText('https://example.com/story')).toHaveLength(2)
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
