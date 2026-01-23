import React from 'react';
import { ToolResult } from '../../types';
import { ToolCard, Collapsible } from './shared';

interface FallbackRendererProps {
  toolResult: ToolResult;
}

const safeStringify = (obj: unknown): string => {
  const seen = new WeakSet();
  return JSON.stringify(obj, (_key, val) => {
    if (val != null && typeof val === 'object') {
      if (seen.has(val)) {
        return '[Circular]';
      }
      seen.add(val);
    }
    return val;
  }, 2);
};

const FallbackRenderer: React.FC<FallbackRendererProps> = ({ toolResult }) => {
  return (
    <ToolCard
      title={toolResult.toolName}
      badge={{ text: 'Unknown', className: 'px-2 py-0.5 rounded text-xs font-heading font-medium bg-kodelet-mid-gray/20 text-kodelet-mid-gray border border-kodelet-mid-gray/30' }}
    >
      <Collapsible
        title="Raw Data"
        collapsed={true}
        badge={{ text: 'Debug', className: 'px-2 py-0.5 rounded text-xs font-heading font-medium bg-kodelet-orange/10 text-kodelet-orange border border-kodelet-orange/20' }}
      >
        <pre className="text-xs overflow-x-auto bg-kodelet-light p-3 rounded-lg border border-kodelet-light-gray font-mono text-kodelet-dark">
          <code>{safeStringify(toolResult.metadata)}</code>
        </pre>
      </Collapsible>
    </ToolCard>
  );
};

export default FallbackRenderer;