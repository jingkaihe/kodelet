import React from 'react';
import { ToolResult } from '../../types';
import { ToolCard, Collapsible } from './shared';

interface FallbackRendererProps {
  toolResult: ToolResult;
}

const FallbackRenderer: React.FC<FallbackRendererProps> = ({ toolResult }) => {
  return (
    <ToolCard
      title={`🔧 ${toolResult.toolName}`}
      badge={{ text: 'Unknown Tool', className: 'badge-info' }}
    >
      <Collapsible
        title="Raw Metadata"
        collapsed={true}
        badge={{ text: 'Debug Info', className: 'badge-warning' }}
      >
        <pre className="text-xs overflow-x-auto bg-base-100 p-2 rounded">
          <code>{JSON.stringify(toolResult.metadata, null, 2)}</code>
        </pre>
      </Collapsible>
    </ToolCard>
  );
};

export default FallbackRenderer;