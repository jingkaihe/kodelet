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

const OpenAIWebSearchRenderer: React.FC<OpenAIWebSearchRendererProps> = ({ toolResult }) => {
  const meta = toolResult.metadata as OpenAIWebSearchMetadata
  if (!meta) {
    return null
  }

  const status = prettifyLabel(meta.status || (toolResult.success ? 'completed' : 'failed'))
  const action = ACTION_LABELS[meta.action || ''] || prettifyLabel(meta.action) || 'Search'
  const queries = uniqueItems(meta.queries)
  const sources = uniqueItems(meta.sources)
  const results = uniqueItems(meta.results)
  const subtitle = queries[0] || meta.url || undefined
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
          { label: 'URL', value: meta.url, monospace: true },
          { label: 'Pattern', value: meta.pattern, monospace: true },
        ]}
      />

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
