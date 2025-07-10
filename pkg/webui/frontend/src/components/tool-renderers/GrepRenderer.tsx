import React from 'react';
import { ToolResult, GrepMetadata, GrepResult, GrepMatch } from '../../types';
import { ToolCard, MetadataRow, Collapsible } from './shared';

interface GrepRendererProps {
  toolResult: ToolResult;
}

const GrepRenderer: React.FC<GrepRendererProps> = ({ toolResult }) => {
  const meta = toolResult.metadata as GrepMetadata;
  if (!meta) return null;

  const results = meta.results || [];
  const totalMatches = results.reduce((sum, result) => sum + (result.matches ? result.matches.length : 1), 0);

  const badges = [];
  badges.push({ text: `${totalMatches} matches in ${results.length} files`, className: 'badge-info' });
  if (meta.truncated) badges.push({ text: 'Truncated', className: 'badge-warning' });

  const groupResultsByFile = (results: GrepResult[]) => {
    const grouped: Record<string, GrepMatch[]> = {};
    results.forEach(result => {
      const file = result.file || result.filename || 'Unknown';
      if (!grouped[file]) {
        grouped[file] = [];
      }

      if (result.matches) {
        // Multiple matches per file
        grouped[file].push(...result.matches);
      } else {
        // Single match
        grouped[file].push({
          lineNumber: result.lineNumber || result.line_number || 0,
          content: result.content || result.line || ''
        });
      }
    });
    return grouped;
  };

  const highlightPattern = (text: string, pattern: string): string => {
    if (!pattern || !text) return text;

    try {
      const regex = new RegExp(`(${pattern.replace(/[.*+?^${}()|[\]\\]/g, '\\$&')})`, 'gi');
      return text.replace(regex, '<mark class="bg-yellow-200 text-black">$1</mark>');
    } catch (e) {
      return text;
    }
  };

  const renderSearchResults = (results: GrepResult[], pattern: string) => {
    const fileGroups = groupResultsByFile(results);

    return Object.entries(fileGroups).map(([file, matches]) => {
      const matchCount = matches.length;
      const fileContent = matches.map((match, index) => {
        const highlightedContent = highlightPattern(match.content, pattern);
        return (
          <div key={index} className="flex items-start gap-2 py-1 hover:bg-base-100 rounded px-2">
            <span className="text-xs text-base-content/50 font-mono min-w-[3rem]">
              {match.lineNumber || '?'}:
            </span>
            <span 
              className="text-sm font-mono flex-1" 
              dangerouslySetInnerHTML={{ __html: highlightedContent }}
            />
          </div>
        );
      });

      return (
        <Collapsible
          key={file}
          title={`📄 ${file}`}
          collapsed={matchCount > 5} // Collapse if more than 5 matches
          badge={{ text: `${matchCount} matches`, className: 'badge-info' }}
        >
          <div>{fileContent}</div>
        </Collapsible>
      );
    });
  };

  return (
    <ToolCard
      title="🔍 Search Results"
      badge={badges[0]}
    >
      <div className="text-xs text-base-content/60 mb-3 font-mono">
        <div className="flex items-center gap-4 flex-wrap">
          <MetadataRow label="Pattern" value={meta.pattern} monospace />
          {meta.path && <MetadataRow label="Path" value={meta.path} monospace />}
          {meta.include && <MetadataRow label="Include" value={meta.include} monospace />}
        </div>
      </div>

      {results.length > 0 ? (
        <div>{renderSearchResults(results, meta.pattern)}</div>
      ) : (
        <div className="text-sm text-base-content/60">No matches found</div>
      )}
    </ToolCard>
  );
};

export default GrepRenderer;