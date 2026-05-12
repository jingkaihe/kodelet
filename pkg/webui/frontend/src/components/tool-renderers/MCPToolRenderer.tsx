import React from 'react';
import { MCPContent, MCPToolMetadata, ToolResult } from '../../types';
import {
  formatReferenceDuration,
  ReferenceCodeBlock,
  ReferenceCodeList,
  ReferenceTerminal,
} from './reference';
import { safeStringify } from './shared';

interface MCPToolRendererProps {
  toolResult: ToolResult;
}

const getContentText = (content: MCPContent): string => {
  if (content.type === 'image') {
    return `[Image: ${content.mimeType || 'image'}, ${content.data?.length || 0} bytes]`;
  }
  if (content.type === 'resource') {
    return `[Resource: ${content.uri || 'resource'}${content.mimeType ? ` (${content.mimeType})` : ''}]`;
  }
  return content.text || content.data || '';
};

const MCPToolRenderer: React.FC<MCPToolRendererProps> = ({ toolResult }) => {
  const meta = toolResult.metadata as MCPToolMetadata;
  if (!meta) return null;

  const durationText = formatReferenceDuration(meta.executionTime);
  const contentItems = meta.content || [];
  const contentText = contentItems.length > 0
    ? contentItems.map(getContentText).filter(Boolean).join('\n\n')
    : meta.contentText || '';

  const parameters = meta.parameters ? safeStringify(meta.parameters) : '';

  return (
    <div className="quiet-tool-detail">
      <div className="quiet-tool-line">
        <span className="quiet-tool-emphasis">{meta.mcpToolName || toolResult.toolName}</span>
        {meta.serverName ? <span className="quiet-tool-muted">{meta.serverName}</span> : null}
        {durationText ? <span className="quiet-tool-muted">{durationText}</span> : null}
      </div>

      {parameters ? (
        <div className="quiet-tool-sections">
          <div className="quiet-tool-section-title">Parameters</div>
          <ReferenceCodeBlock content={parameters} language="json" />
        </div>
      ) : null}

      {contentItems.length > 0 && contentItems.some((item) => item.type && item.type !== 'text') ? (
        <ReferenceCodeList
          items={contentItems.map((item, index) => item.type || `content ${index + 1}`)}
        />
      ) : null}

      {contentText ? (
        <div className="quiet-tool-sections">
          <div className="quiet-tool-section-title">Content</div>
          <ReferenceTerminal output={contentText} />
        </div>
      ) : (
        <div className="quiet-tool-empty">MCP tool completed without content.</div>
      )}
    </div>
  );
};

export default MCPToolRenderer;
