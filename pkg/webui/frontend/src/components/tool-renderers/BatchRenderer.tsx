import React from 'react';
import { ToolResult, BatchMetadata } from '../../types';
import { ToolCard, Collapsible } from './shared';
import { escapeHtml } from './utils';

interface BatchRendererProps {
  toolResult: ToolResult;
}

const BatchRenderer: React.FC<BatchRendererProps> = ({ toolResult }) => {
  const meta = toolResult.metadata as BatchMetadata;
  if (!meta) return null;

  const description = meta.description || 'Batch operation';
  const subResults = meta.subResults || meta.results || [];
  const successCount = meta.successCount || subResults.filter(r => r.success).length;
  const failureCount = meta.failureCount || subResults.filter(r => !r.success).length;

  const renderSubResults = (subResults: ToolResult[]) => {
    return subResults.map((result, index) => {
      const statusIcon = result.success ? '✅' : '❌';
      const statusClass = result.success ? 'text-green-600' : 'text-red-600';

      return (
        <div key={index} className="border rounded p-3 mb-2">
          <div className="flex items-center gap-2 mb-2">
            <span className={statusClass} aria-label={result.success ? 'Success' : 'Failed'}>
              {statusIcon}
            </span>
            <span className="font-medium text-sm">Operation {index + 1}</span>
            {result.toolName && (
              <div className="badge badge-xs badge-outline">{result.toolName}</div>
            )}
          </div>
          {result.error && (
            <div className="text-xs text-red-600">{escapeHtml(result.error)}</div>
          )}
        </div>
      );
    });
  };

  return (
    <ToolCard
      title="📦 Batch Operation"
      badge={{ text: description, className: 'badge-info' }}
    >
      <div className="text-xs text-base-content/60 mb-3">
        <div className="flex items-center gap-4">
          <span><strong>Total:</strong> {subResults.length} operations</span>
          <span className="text-green-600"><strong>Success:</strong> {successCount}</span>
          {failureCount > 0 && (
            <span className="text-red-600"><strong>Failed:</strong> {failureCount}</span>
          )}
        </div>
      </div>

      {subResults.length > 0 && (
        <Collapsible
          title="Sub-operations"
          collapsed={true}
          badge={{ text: `${subResults.length} operations`, className: 'badge-info' }}
        >
          <div>{renderSubResults(subResults)}</div>
        </Collapsible>
      )}
    </ToolCard>
  );
};

export default BatchRenderer;