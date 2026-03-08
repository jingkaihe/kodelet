import React, { useState } from 'react';
import { ToolResult } from '../../types';
import { StatusBadge } from './shared';

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
  const [showRaw, setShowRaw] = useState(false);

  return (
    <div className="space-y-2">
      <div className="flex items-center gap-2 text-xs">
        <StatusBadge text="Completed" variant="neutral" />
      </div>

      {!showRaw ? (
        <button
          onClick={() => setShowRaw(true)}
          className="tool-action-link"
        >
          Show raw data
        </button>
      ) : (
        <pre className="tool-code-block max-h-48 overflow-y-auto text-xs">
          <code>{safeStringify(toolResult.metadata)}</code>
        </pre>
      )}
    </div>
  );
};

export default FallbackRenderer;
