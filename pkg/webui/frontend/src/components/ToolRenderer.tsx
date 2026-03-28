import React from 'react';
import { ToolResult } from '../types';
import FallbackRenderer from './tool-renderers/FallbackRenderer';
import { getToolRendererRegistration } from './tool-renderers/registry';
import { normalizeToolName } from './tool-renderers/reference';

interface ToolRendererProps {
  toolResult: ToolResult;
}

const ToolRenderer: React.FC<ToolRendererProps> = ({ toolResult }) => {
  const renderTool = () => {
    const normalizedToolName = normalizeToolName(toolResult.toolName);
    const rendererRegistration = getToolRendererRegistration(toolResult.toolName);

    if (!toolResult.success && !(rendererRegistration?.supportsFailureRendering && toolResult.metadata)) {
      return (
        <div className="surface-panel rounded-2xl border-kodelet-orange/20 p-4" role="alert">
          <div className="flex items-center gap-2 mb-2">
            <svg
              xmlns="http://www.w3.org/2000/svg"
              className="h-5 w-5 text-kodelet-orange"
              fill="none"
              viewBox="0 0 24 24"
              stroke="currentColor"
              aria-hidden="true"
            >
              <path
                strokeLinecap="round"
                strokeLinejoin="round"
                strokeWidth="2"
                d="M12 8v4m0 4h.01M21 12a9 9 0 11-18 0 9 9 0 0118 0z"
              />
            </svg>
            <strong className="font-heading text-sm font-semibold text-kodelet-orange">Error ({normalizedToolName}):</strong>
          </div>
          <div className="text-sm font-body text-kodelet-dark">{toolResult.error || 'Unknown error'}</div>
        </div>
      );
    }

    if (rendererRegistration) {
      const Renderer = rendererRegistration.component;
      return <Renderer toolResult={toolResult} />;
    }

    return <FallbackRenderer toolResult={toolResult} />;
  };

  try {
    return renderTool();
  } catch (error) {
    console.error('Error rendering tool result:', error, toolResult);
    return (
      <div className="surface-panel rounded-2xl border-kodelet-orange/20 p-4">
        <strong className="font-heading font-semibold text-sm text-kodelet-orange">Renderer Error ({normalizeToolName(toolResult.toolName)}):</strong>
        <div className="text-sm font-body text-kodelet-dark mt-1">Failed to render tool result</div>
      </div>
    );
  }
};

export default ToolRenderer;
