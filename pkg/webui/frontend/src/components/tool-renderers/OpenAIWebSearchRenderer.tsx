import React from 'react';
import { OpenAIWebSearchMetadata, ToolResult } from '../../types';
import { ReferenceToolNote } from './reference';
import { ExternalLink } from './shared';

interface OpenAIWebSearchRendererProps {
  toolResult: ToolResult;
  toolInput?: string;
}

const uniqueItems = (items?: string[]): string[] =>
  Array.from(new Set((items || []).map((item) => item.trim()).filter(Boolean)))

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

  const actionType = stringField(input, 'type', 'action') || meta?.action || 'search'
  const queries = uniqueItems([
    ...stringArrayField(input, 'queries'),
    ...(meta?.queries || []),
    stringField(input, 'query', 'content'),
  ].filter((item): item is string => Boolean(item)))
  const url = stringField(input, 'url', 'URL') || meta?.url
  const pattern = stringField(input, 'pattern') || meta?.pattern
  const primaryTarget = actionType === 'open_page'
    ? url || queries[0] || pattern
    : actionType === 'find_in_page'
      ? pattern && url
        ? `${pattern} in ${url}`
        : pattern || url || queries[0]
      : queries[0] || url || pattern
  const missingTargetMessage = !primaryTarget && actionType === 'open_page'
    ? 'OpenAI did not include the page URL for this open-page action.'
    : !primaryTarget && actionType === 'find_in_page'
      ? 'OpenAI did not include the page URL or pattern for this find-in-page action.'
      : ''
  const additionalQueries = queries.slice(1)
  const links = uniqueItems([
    ...(meta?.sources || []),
    ...(meta?.results || []),
  ]).filter((link) => link !== url)
  const visibleLinks = links.slice(0, 6)
  const errorMessage = !toolResult.success ? toolResult.error : undefined
  const hasDetails = Boolean(
    additionalQueries.length || visibleLinks.length || missingTargetMessage || errorMessage
  )

  return (
    <div className="quiet-tool-detail">
      {missingTargetMessage ? <ReferenceToolNote text={missingTargetMessage} /> : null}

      {additionalQueries.length > 0 ? (
        <div className="flex flex-col gap-2">
          <div className="quiet-tool-section-title">Additional queries</div>
          <div className="flex flex-col gap-1">
            {additionalQueries.map((query) => (
              <div className="quiet-tool-path" key={query} title={query}>
                {query}
              </div>
            ))}
          </div>
        </div>
      ) : null}

      {visibleLinks.length > 0 ? (
        <div className="flex flex-col gap-2">
          <div className="quiet-tool-section-title">Links</div>
          <div className="flex flex-col gap-1">
            {visibleLinks.map((link) => (
              <ExternalLink key={link} href={link} className="break-all">
                {link}
              </ExternalLink>
            ))}
          </div>
          {links.length > visibleLinks.length ? (
            <ReferenceToolNote text={`Showing first ${visibleLinks.length} of ${links.length} links.`} />
          ) : null}
        </div>
      ) : null}

      {errorMessage ? <ReferenceToolNote text={errorMessage} /> : null}
      {!hasDetails ? <div className="quiet-tool-empty">No additional details</div> : null}
    </div>
  )
}

export default OpenAIWebSearchRenderer
