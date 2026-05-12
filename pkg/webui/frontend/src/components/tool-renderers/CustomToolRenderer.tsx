import React from 'react';
import { CustomToolMetadata, ToolResult } from '../../types';
import { formatReferenceDuration, ReferenceCodeBlock, ReferenceTerminal } from './reference';
import { formatJsonObjectOrArray } from './shared';

interface CustomToolRendererProps {
  toolResult: ToolResult;
}

const getDisplayName = (toolName: string): string => toolName.replace(/^custom_tool_/, '');

const CustomToolRenderer: React.FC<CustomToolRendererProps> = ({ toolResult }) => {
  const meta = toolResult.metadata as CustomToolMetadata;
  if (!meta) return null;

  const formattedJsonOutput = formatJsonObjectOrArray(meta.output);
  const output = formattedJsonOutput?.formatted || meta.output || '';
  const durationText = formatReferenceDuration(meta.executionTime);

  return (
    <div className="quiet-tool-detail">
      <div className="quiet-tool-line">
        <span className="quiet-tool-emphasis">{getDisplayName(toolResult.toolName)}</span>
        {durationText ? <span className="quiet-tool-muted">{durationText}</span> : null}
      </div>

      {output ? (
        formattedJsonOutput ? (
          <ReferenceCodeBlock content={output} language="json" />
        ) : (
          <ReferenceTerminal output={output} />
        )
      ) : (
        <div className="quiet-tool-empty">Custom tool completed without output.</div>
      )}
    </div>
  );
};

export default CustomToolRenderer;
