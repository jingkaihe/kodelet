import React, { useState } from 'react';
import { ToolResult, GrepMetadata, GrepResult, GrepMatch } from '../../types';
import { StatusBadge } from './shared';

interface GrepRendererProps {
  toolResult: ToolResult;
}

const GrepRenderer: React.FC<GrepRendererProps> = ({ toolResult }) => {
  const meta = toolResult.metadata as GrepMetadata;
  const [expandedFiles, setExpandedFiles] = useState<Set<string>>(new Set());
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

  const highlightPattern = (text: string, pattern: string): string => {
    if (!pattern || !text) return text;
    try {
      const regex = new RegExp(`(${pattern.replace(/[.*+?^${}()|[\]\\]/g, '\\$&')})`, 'gi');
      return text.replace(regex, '<mark class="bg-kodelet-orange/30 text-kodelet-dark px-0.5 rounded">$1</mark>');
    } catch {
      return text;
    }
  };

  const toggleFile = (file: string) => {
    setExpandedFiles(prev => {
      const next = new Set(prev);
      if (next.has(file)) {
        next.delete(file);
      } else {
        next.add(file);
      }
      return next;
    });
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
      <div className="flex items-center gap-2 flex-wrap text-xs font-mono text-kodelet-dark/80">
        <code className="font-medium">{meta.pattern}</code>
        <StatusBadge
          text={`${totalMatches} in ${results.length} files`}
          variant={meta.truncated ? 'warning' : 'success'}
        />
        {meta.path && <span className="text-kodelet-mid-gray">in {meta.path}</span>}
        {truncationMessage && (
          <StatusBadge text={truncationMessage} variant="warning" />
        )}
      </div>

      {results.length > 0 ? (
        <div className="space-y-1">
          {Object.entries(fileGroups).map(([file, matches]) => {
            const isExpanded = expandedFiles.has(file) || matches.length <= 3;
            const displayMatches = isExpanded ? matches : matches.slice(0, 2);

            return (
              <div key={file} className="text-xs">
                <div className="font-mono text-kodelet-dark/70 font-medium">{file}</div>
                <div className="ml-2 border-l border-kodelet-light-gray pl-2">
                  {displayMatches.map((match, index) => (
                    <div key={index} className={`flex gap-2 py-0.5 ${match.isContext ? 'opacity-50' : ''}`}>
                      <span className="text-kodelet-mid-gray min-w-[3rem] text-right">{match.lineNumber}</span>
                      <span
                        className="font-mono text-kodelet-dark"
                        dangerouslySetInnerHTML={{ __html: match.isContext ? match.content : highlightPattern(match.content, meta.pattern) }}
                      />
                    </div>
                  ))}
                  {matches.length > 3 && !isExpanded && (
                    <button
                      onClick={() => toggleFile(file)}
                      className="text-kodelet-blue hover:underline"
                    >
                      +{matches.length - 2} more
                    </button>
                  )}
                </div>
              </div>
            );
          })}
        </div>
      ) : (
        <div className="text-xs text-kodelet-mid-gray">No matches found</div>
      )}
    </div>
  );
};

export default GrepRenderer;