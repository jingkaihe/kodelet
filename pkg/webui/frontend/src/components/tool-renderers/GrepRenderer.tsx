import React from 'react';
import { ToolResult, GrepMetadata, GrepResult, GrepMatch } from '../../types';
import {
  highlightPattern,
  ReferenceToolKVGrid,
  ReferenceToolNote,
} from './reference';

interface GrepRendererProps {
  toolResult: ToolResult;
}

const GrepRenderer: React.FC<GrepRendererProps> = ({ toolResult }) => {
  const meta = toolResult.metadata as GrepMetadata;
  if (!meta) return null;

  const results = meta.results || [];
  const totalMatches = results.reduce(
    (sum, result) => sum + (result.matches?.length || (result.content ? 1 : 0)),
    0
  );

  const getTruncationMessage = (): string | null => {
    if (!meta.truncated) return null;
    switch (meta.truncationReason) {
      case 'file_limit':
        return `Truncated: max ${meta.maxResults || 100} files`;
      case 'output_size':
        return 'Truncated: output size limit (50KB)';
      default:
        return 'Results truncated';
    }
  };

  const truncationMessage = getTruncationMessage();

  return (
    <div className="quiet-tool-detail">
      <div className="quiet-tool-line">
        <span className="quiet-tool-emphasis">{totalMatches} matches</span>
        <span className="quiet-tool-muted">{results.length} files</span>
        {meta.truncated ? <span className="quiet-tool-warning">truncated</span> : null}
      </div>
      <div className="quiet-tool-path">{meta.pattern}</div>

      <ReferenceToolKVGrid
        items={[
          { label: 'Path', value: meta.path, monospace: true },
          { label: 'Include', value: meta.include, monospace: true },
          { label: 'Truncation', value: meta.truncationReason },
        ]}
      />

      {results.length > 0 ? (
        <div className="space-y-1">
          {results.map((result: GrepResult, resultIndex: number) => {
            const file = result.filePath || 'Unknown';
            const matches: GrepMatch[] =
              result.matches && result.matches.length > 0
                ? result.matches
                : [
                    {
                      lineNumber: result.lineNumber || 0,
                      content: result.content || '',
                    },
                  ];

            return (
              <div className="grep-block" key={`${file}-${resultIndex}`}>
                <div className="grep-file-header">{file}</div>
                {matches.slice(0, 12).map((match, index) => (
                  <div
                    className={match.isContext ? 'grep-line context' : 'grep-line'}
                    key={index}
                  >
                    <span className="grep-line-number">{match.lineNumber}</span>
                    <span
                      dangerouslySetInnerHTML={{
                        __html: match.isContext
                          ? highlightPattern(match.content, '')
                          : highlightPattern(match.content, meta.pattern),
                      }}
                    />
                  </div>
                ))}
                {matches.length > 12 ? (
                  <ReferenceToolNote
                    text={`Showing first 12 of ${matches.length} matches in this file.`}
                  />
                ) : null}
              </div>
            );
          })}
        </div>
      ) : (
        <div className="quiet-tool-empty">No matches found</div>
      )}

      {truncationMessage ? <ReferenceToolNote text={truncationMessage} /> : null}
    </div>
  );
};

export default GrepRenderer;
