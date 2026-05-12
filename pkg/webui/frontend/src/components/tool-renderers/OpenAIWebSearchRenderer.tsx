import React from 'react';
import { OpenAIWebSearchMetadata, ToolResult } from '../../types';
import {
  ReferenceCodeList,
  ReferenceToolKVGrid,
  ReferenceToolNote,
} from './reference';
import { ExternalLink } from './shared';

interface OpenAIWebSearchRendererProps {
  toolResult: ToolResult;
  toolInput?: string;
}

const ACTION_LABELS: Record<string, string> = {
  search: 'Search',
  open_page: 'Open page',
  find_in_page: 'Find in page',
}

const prettifyLabel = (value?: string): string => {
  if (!value) {
    return ''
  }

  return value.replace(/_/g, ' ')
}

const uniqueItems = (items?: string[]): string[] =>
  Array.from(new Set((items || []).filter((item) => item && item.trim().length > 0)))

const parseToolInput = (toolInput?: string): Record<string, unknown> | null => {
  if (!toolInput) {
    return null
  }

  try {
    const parsed = JSON.parse(toolInput)
    return parsed && typeof parsed === 'object' && !Array.isArray(parsed)
      ? (parsed as Record<string, unknown>)
      : null
  } catch {
    return null
  }
}

const stringField = (
  source: Record<string, unknown> | null,
  ...keys: string[]
): string | undefined => {
  for (const key of keys) {
    const value = source?.[key]
    if (typeof value === 'string' && value.trim().length > 0) {
      return value.trim()
    }
  }

  return undefined
}

const stringArrayField = (
  source: Record<string, unknown> | null,
  ...keys: string[]
): string[] => {
  for (const key of keys) {
    const value = source?.[key]
    if (!Array.isArray(value)) {
      continue
    }

    const items = value
      .filter((item): item is string => typeof item === 'string')
      .map((item) => item.trim())
      .filter(Boolean)

    if (items.length > 0) {
      return items
    }
  }

  return []
}

const OpenAIWebSearchRenderer: React.FC<OpenAIWebSearchRendererProps> = ({ toolResult, toolInput }) => {
  const meta = toolResult.metadata as OpenAIWebSearchMetadata
  const input = parseToolInput(toolInput)
  if (!meta && !input) {
    return null
  }

  const actionType = meta?.action || stringField(input, 'type', 'action') || 'search'
  const status = prettifyLabel(
    meta?.status || stringField(input, 'status') || (toolResult.success ? 'completed' : 'failed')
  )
  const action = ACTION_LABELS[actionType] || prettifyLabel(actionType) || 'Search'
  const queries = uniqueItems([
    ...(meta?.queries || []),
    ...stringArrayField(input, 'queries'),
    stringField(input, 'query', 'content'),
  ].filter((item): item is string => Boolean(item)))
  const sources = uniqueItems(meta?.sources)
  const results = uniqueItems(meta?.results)
  const url = meta?.url || stringField(input, 'url', 'URL')
  const pattern = meta?.pattern || stringField(input, 'pattern')
  const subtitle = actionType === 'find_in_page' && pattern && url
    ? `${pattern} in ${url}`
    : queries[0] || url || pattern || undefined
  const missingTargetMessage = !subtitle && actionType === 'open_page'
    ? 'OpenAI did not include the page URL for this open-page action.'
    : !subtitle && actionType === 'find_in_page'
      ? 'OpenAI did not include the page URL or pattern for this find-in-page action.'
      : ''
  const visibleSources = sources.slice(0, 6)
  const visibleResults = results.slice(0, 6)
  const linkCount = sources.length + results.length

  return (
    <div className="quiet-tool-detail">
      <div className="quiet-tool-line">
        <span className={toolResult.success ? 'quiet-tool-emphasis' : 'quiet-tool-warning'}>
          {status || 'completed'}
        </span>
        <span className="quiet-tool-muted">{action}</span>
        {linkCount > 0 ? <span className="quiet-tool-muted">{linkCount} links</span> : null}
      </div>
      {subtitle ? <div className="quiet-tool-path">{subtitle}</div> : null}

      <ReferenceToolKVGrid
        items={[
          { label: 'URL', value: url, monospace: true },
          { label: 'Pattern', value: pattern, monospace: true },
        ]}
      />

      {missingTargetMessage ? <ReferenceToolNote text={missingTargetMessage} /> : null}

      {queries.length > 0 ? (
        <div className="space-y-2">
          <div className="quiet-tool-section-title">Queries</div>
          <ReferenceCodeList items={queries} />
        </div>
      ) : null}

      {visibleSources.length > 0 ? (
        <div className="space-y-2">
          <div className="quiet-tool-section-title">Sources</div>
          <div className="flex flex-col gap-1">
            {visibleSources.map((url) => (
              <ExternalLink key={url} href={url} className="break-all">
                {url}
              </ExternalLink>
            ))}
          </div>
          {sources.length > visibleSources.length ? (
            <ReferenceToolNote text={`Showing first ${visibleSources.length} of ${sources.length} sources.`} />
          ) : null}
        </div>
      ) : null}

      {visibleResults.length > 0 ? (
        <div className="space-y-2">
          <div className="quiet-tool-section-title">Results</div>
          <div className="flex flex-col gap-1">
            {visibleResults.map((url) => (
              <ExternalLink key={url} href={url} className="break-all">
                {url}
              </ExternalLink>
            ))}
          </div>
          {results.length > visibleResults.length ? (
            <ReferenceToolNote text={`Showing first ${visibleResults.length} of ${results.length} results.`} />
          ) : null}
        </div>
      ) : null}

      {!toolResult.success && toolResult.error ? (
        <ReferenceToolNote text={toolResult.error} />
      ) : null}
    </div>
  )
}

export default OpenAIWebSearchRenderer
