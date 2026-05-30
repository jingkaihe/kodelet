import React from 'react';
import { ExtensionToolMetadata, ToolResult } from '../../types';
import { formatReferenceDuration, ReferenceCodeBlock, ReferenceTerminal } from './reference';
import { formatJsonObjectOrArray } from './shared';

interface ExtensionToolRendererProps {
  toolResult: ToolResult;
}

const getDisplayName = (toolName: string, meta: ExtensionToolMetadata): string =>
  meta.toolName || toolName;

const ExtensionToolRenderer: React.FC<ExtensionToolRendererProps> = ({ toolResult }) => {
  const meta = toolResult.metadata as ExtensionToolMetadata;
  if (!meta) return null;

  const formattedJsonOutput = formatJsonObjectOrArray(meta.output);
  const output = formattedJsonOutput?.formatted || meta.output || '';
  const durationText = formatReferenceDuration(meta.executionTime);

  return (
    <div className="quiet-tool-detail">
      <div className="quiet-tool-line">
        <span className="quiet-tool-emphasis">{getDisplayName(toolResult.toolName, meta)}</span>
        {durationText ? <span className="quiet-tool-muted">{durationText}</span> : null}
      </div>

      {output ? (
        formattedJsonOutput ? (
          <ReferenceCodeBlock content={output} language="json" />
        ) : (
          <ReferenceTerminal output={output} />
        )
      ) : (
        <div className="quiet-tool-empty">Extension tool completed without output.</div>
      )}
    </div>
  );
};

export default ExtensionToolRenderer;
