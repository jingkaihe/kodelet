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

  const getBadge = () => {
    if (meta.truncated) {
      return { 
        text: 'Truncated', 
        className: 'px-2 py-0.5 rounded text-xs font-heading font-medium bg-kodelet-orange/10 text-kodelet-orange border border-kodelet-orange/20' 
      };
    }
    return { 
      text: `${totalMatches} in ${results.length} files`, 
      className: 'px-2 py-0.5 rounded text-xs font-heading font-medium bg-kodelet-green/10 text-kodelet-green border border-kodelet-green/20' 
    };
  };

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

  const highlightPattern = (text: string, pattern: string): string => {
    if (!pattern || !text) return text;

    try {
      const regex = new RegExp(`(${pattern.replace(/[.*+?^${}()|[\]\\]/g, '\\$&')})`, 'gi');
      return text.replace(regex, '<mark class="bg-kodelet-orange/30 text-kodelet-dark px-0.5 rounded">$1</mark>');
    } catch {
      return text;
    }
  };

  const renderSearchResults = (results: GrepResult[], pattern: string) => {
    const fileGroups = groupResultsByFile(results);

    return Object.entries(fileGroups).map(([file, matches]) => {
      const matchCount = matches.length;
      const fileContent = matches.map((match, index) => {
        const highlightedContent = match.isContext ? match.content : highlightPattern(match.content, pattern);
        const isContext = match.isContext;
        return (
          <div key={index} className={`flex items-start gap-2 py-1 hover:bg-kodelet-light-gray/30 rounded px-2 ${isContext ? 'opacity-60' : ''}`}>
            <span className="text-xs text-kodelet-mid-gray font-mono min-w-[3rem]">
              {match.lineNumber || '?'}{isContext ? '-' : ':'}
            </span>
            <span 
              className="text-sm font-mono flex-1 text-kodelet-dark" 
              dangerouslySetInnerHTML={{ __html: highlightedContent }}
            />
          </div>
        );
      });

      return (
        <Collapsible
          key={file}
          title={file}
          collapsed={matchCount > 5}
          badge={{ text: `${matchCount} matches`, className: 'px-2 py-0.5 rounded text-xs font-heading font-medium bg-kodelet-blue/10 text-kodelet-blue border border-kodelet-blue/20' }}
        >
          <div>{fileContent}</div>
        </Collapsible>
      );
    });
  };

  return (
    <ToolCard
      title="Search Results"
      badge={getBadge()}
    >
      <div className="text-xs text-kodelet-dark/60 mb-3 font-mono">
        <div className="flex items-center gap-4 flex-wrap">
          <MetadataRow label="Pattern" value={meta.pattern} monospace />
          {meta.path && <MetadataRow label="Path" value={meta.path} monospace />}
          {meta.include && <MetadataRow label="Include" value={meta.include} monospace />}
        </div>
      </div>

      {results.length > 0 ? (
        <div>{renderSearchResults(results, meta.pattern)}</div>
      ) : (
        <div className="text-sm text-kodelet-dark/50 font-body">No matches found</div>
      )}
    </ToolCard>
  );
};

export default GrepRenderer;