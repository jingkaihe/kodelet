import React from 'react';
import { ToolResult, GrepMetadata, GrepResult, GrepMatch } from '../../types';
import {
  highlightPattern,
  ReferenceToolHeader,
  ReferenceToolKVGrid,
  ReferenceToolNote,
  TOOL_ICONS,
} from './reference';

interface GrepRendererProps {
  toolResult: ToolResult;
}

const GrepRenderer: React.FC<GrepRendererProps> = ({ toolResult }) => {
  const meta = toolResult.metadata as GrepMetadata;
  if (!meta) return null;

  const results = meta.results || [];
  const totalMatches = results.reduce((sum, result) => sum + (result.matches ? result.matches.length : 1), 0);

  const groupResultsByFile = (results: GrepResult[]) => {
    const grouped: Record<string, GrepMatch[]> = {};
    results.forEach(result => {
      const file = result.filePath || 'Unknown';
      if (!grouped[file]) {
        grouped[file] = [];
      }
      if (result.matches) {
        grouped[file].push(...result.matches);
      } else {
        grouped[file].push({
          lineNumber: result.lineNumber || 0,
          content: result.content || ''
        });
      }
    });
    return grouped;
  };

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

  const fileGroups = groupResultsByFile(results);
  const truncationMessage = getTruncationMessage();

  return (
    <div className="space-y-2">
      <ReferenceToolHeader
        badges={[
          {
            text: `${totalMatches} matches`,
            variant: meta.truncated ? 'warning' : 'success',
          },
          {
            text: `${results.length} files`,
            variant: 'info',
          },
        ]}
        subtitle={meta.pattern}
        title={`${TOOL_ICONS.grep_tool} Search Results`}
      />

      <ReferenceToolKVGrid
        items={[
          { label: 'Path', value: meta.path, monospace: true },
          { label: 'Include', value: meta.include, monospace: true },
          { label: 'Truncation', value: meta.truncationReason },
        ]}
      />

      {results.length > 0 ? (
        <div className="space-y-1">
          {Object.entries(fileGroups).map(([file, matches]) => {
            return (
              <div className="grep-block" key={file}>
                <div className="grep-file-header">{file || 'Unknown'}</div>
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
        <div className="text-xs text-kodelet-mid-gray">No matches found</div>
      )}

      {truncationMessage ? <ReferenceToolNote text={truncationMessage} /> : null}
    </div>
  );
};

export default GrepRenderer;
