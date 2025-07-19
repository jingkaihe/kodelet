import React from 'react';
import { ToolResult } from '../types';
import FileReadRenderer from './tool-renderers/FileReadRenderer';
import FileWriteRenderer from './tool-renderers/FileWriteRenderer';
import FileEditRenderer from './tool-renderers/FileEditRenderer';
import BashRenderer from './tool-renderers/BashRenderer';
import GrepRenderer from './tool-renderers/GrepRenderer';
import GlobRenderer from './tool-renderers/GlobRenderer';
import WebFetchRenderer from './tool-renderers/WebFetchRenderer';
import ThinkingRenderer from './tool-renderers/ThinkingRenderer';
import TodoRenderer from './tool-renderers/TodoRenderer';
import SubagentRenderer from './tool-renderers/SubagentRenderer';
import ImageRecognitionRenderer from './tool-renderers/ImageRecognitionRenderer';
import BrowserRenderer from './tool-renderers/BrowserRenderer';
import BackgroundProcessesRenderer from './tool-renderers/BackgroundProcessesRenderer';
import FallbackRenderer from './tool-renderers/FallbackRenderer';

interface ToolRendererProps {
  toolResult: ToolResult;
}

const ToolRenderer: React.FC<ToolRendererProps> = ({ toolResult }) => {
  const renderTool = () => {
    if (!toolResult.success) {
      return (
        <div className="alert alert-error" role="alert">
          <div className="flex items-center gap-2">
            <svg
              xmlns="http://www.w3.org/2000/svg"
              className="h-5 w-5"
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
            <strong>Error ({toolResult.toolName}):</strong>
          </div>
          <div className="mt-2 text-sm">{toolResult.error || 'Unknown error'}</div>
        </div>
      );
    }

    // Route to specific renderer based on tool name
    switch (toolResult.toolName) {
      case 'file_read':
        return <FileReadRenderer toolResult={toolResult} />;
      case 'file_write':
        return <FileWriteRenderer toolResult={toolResult} />;
      case 'file_edit':
        return <FileEditRenderer toolResult={toolResult} />;
      case 'bash':
        return <BashRenderer toolResult={toolResult} />;
      case 'grep_tool':
        return <GrepRenderer toolResult={toolResult} />;
      case 'glob_tool':
        return <GlobRenderer toolResult={toolResult} />;
      case 'web_fetch':
        return <WebFetchRenderer toolResult={toolResult} />;
      case 'thinking':
        return <ThinkingRenderer toolResult={toolResult} />;
      case 'todo_read':
      case 'todo_write':
        return <TodoRenderer toolResult={toolResult} />;
      case 'subagent':
        return <SubagentRenderer toolResult={toolResult} />;
      case 'image_recognition':
        return <ImageRecognitionRenderer toolResult={toolResult} />;
      case 'browser_navigate':
      case 'browser_screenshot':
        return <BrowserRenderer toolResult={toolResult} />;
      case 'view_background_processes':
        return <BackgroundProcessesRenderer toolResult={toolResult} />;
      default:
        return <FallbackRenderer toolResult={toolResult} />;
    }
  };

  try {
    return renderTool();
  } catch (error) {
    console.error('Error rendering tool result:', error, toolResult);
    return (
      <div className="alert alert-error">
        <strong>Renderer Error ({toolResult.toolName}):</strong>
        <div className="text-sm">Failed to render tool result</div>
      </div>
    );
  }
};

export default ToolRenderer;