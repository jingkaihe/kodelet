import React from 'react';
import { ExtensionToolMetadata, ToolRenderProps } from '../../types';
import {
  formatReferenceDuration,
  ReferenceCodeBlock,
  ReferenceTerminal,
  ReferenceToolNote,
} from './reference';
import { formatJsonObjectOrArray } from './shared';
import TaskRunRenderer, { getTaskRunSnapshot } from './TaskRunRenderer';

const getDisplayName = (toolName: string, meta: ExtensionToolMetadata): string =>
  meta.toolName || toolName;

const ExtensionToolRenderer: React.FC<ToolRenderProps> = ({ toolResult, toolInput, isPartial }) => {
  const meta = toolResult.metadata as ExtensionToolMetadata;
  if (!meta) return null;

  if (getTaskRunSnapshot(toolResult)) {
    return (
      <TaskRunRenderer
        isPartial={isPartial}
        toolInput={toolInput}
        toolResult={toolResult}
      />
    );
  }

  const formattedJsonOutput = formatJsonObjectOrArray(meta.output);
  const output = formattedJsonOutput?.formatted || meta.output || '';
  const durationText = formatReferenceDuration(meta.executionTime);

  return (
    <div className="quiet-tool-detail">
      <div className="quiet-tool-line">
        <span className="quiet-tool-emphasis">{getDisplayName(toolResult.toolName, meta)}</span>
        {durationText ? <span className="quiet-tool-muted">{durationText}</span> : null}
      </div>

      {!toolResult.success && toolResult.error ? <ReferenceToolNote text={toolResult.error} /> : null}

      {output ? (
        formattedJsonOutput ? (
          <ReferenceCodeBlock content={output} language="json" />
        ) : (
          <ReferenceTerminal output={output} />
        )
      ) : toolResult.success || isPartial ? (
        <div className="quiet-tool-empty">
          {isPartial ? 'Waiting for extension output…' : 'Extension tool completed without output.'}
        </div>
      ) : null}
    </div>
  );
};

export default ExtensionToolRenderer;
