import type React from 'react';
import { useState } from 'react';
import type { ToolResult } from '../../types';
import { safeStringify } from './shared';

interface FallbackRendererProps {
  toolResult: ToolResult;
}

const FallbackRenderer: React.FC<FallbackRendererProps> = ({ toolResult }) => {
  const [showRaw, setShowRaw] = useState(false);

  return (
    <div className="quiet-tool-detail">
      <div className="quiet-tool-line">
        <span className="quiet-tool-emphasis">completed</span>
      </div>

      {!showRaw ? (
        <button type="button" onClick={() => setShowRaw(true)} className="tool-action-link">
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
