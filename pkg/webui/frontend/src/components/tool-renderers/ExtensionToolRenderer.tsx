import React from 'react';
import { ExtensionToolMetadata, ToolRenderProps } from '../../types';
import {
  formatReferenceDuration,
  getExtensionToolPresentation,
  ReferenceCodeBlock,
  ReferenceTerminal,
  ReferenceToolNote,
  renderSafeMarkdown,
} from './reference';
import { formatJsonObjectOrArray } from './shared';
import TaskRunRenderer, { getTaskRunSnapshot } from './TaskRunRenderer';

const getDisplayName = (toolName: string, meta: ExtensionToolMetadata): string =>
  meta.toolName || toolName;

const ExtensionToolRenderer: React.FC<ToolRenderProps> = ({ toolResult, toolInput, isPartial }) => {
  const meta = toolResult.metadata as ExtensionToolMetadata;
  if (!meta) return null;

  if (getTaskRunSnapshot(toolResult)) {
    return <TaskRunRenderer isPartial={isPartial} toolInput={toolInput} toolResult={toolResult} />;
  }

  const presentation = getExtensionToolPresentation(toolResult);
  if (presentation) {
    const hasPresentationBody = presentation.body !== undefined;
    const fallbackBody = meta.output ?? '';
    let body = presentation.body ?? fallbackBody;
    if (
      !hasPresentationBody &&
      !toolResult.success &&
      toolResult.error?.trim() === fallbackBody.trim()
    ) {
      body = '';
    }
    const formattedFallback = hasPresentationBody ? undefined : formatJsonObjectOrArray(body);

    return (
      <div className="quiet-tool-detail extension-presentation-detail">
        {!toolResult.success && toolResult.error ? (
          <ReferenceToolNote text={toolResult.error} />
        ) : null}

        {body ? (
          hasPresentationBody && presentation.format === 'markdown' ? (
            <div
              className="tool-compact-markdown extension-presentation-body"
              dangerouslySetInnerHTML={{ __html: renderSafeMarkdown(body) }}
            />
          ) : hasPresentationBody ? (
            <div className="extension-presentation-body">{body}</div>
          ) : formattedFallback ? (
            <ReferenceCodeBlock content={formattedFallback.formatted} language="json" />
          ) : (
            <ReferenceTerminal output={body} />
          )
        ) : toolResult.success || isPartial ? (
          <div className="quiet-tool-empty">
            Extension tool completed without presentation details.
          </div>
        ) : null}
      </div>
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

      {!toolResult.success && toolResult.error ? (
        <ReferenceToolNote text={toolResult.error} />
      ) : null}

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
