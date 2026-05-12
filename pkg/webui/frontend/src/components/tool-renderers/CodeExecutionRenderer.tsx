import React from 'react';
import { CodeExecutionMetadata, ToolResult } from '../../types';
import { ReferenceCodeBlock, ReferenceTerminal } from './reference';
import { formatJsonObjectOrArray } from './shared';

interface CodeExecutionRendererProps {
  toolResult: ToolResult;
}

const CodeExecutionRenderer: React.FC<CodeExecutionRendererProps> = ({ toolResult }) => {
  const meta = toolResult.metadata as CodeExecutionMetadata;
  if (!meta) return null;

  const formattedJsonOutput = formatJsonObjectOrArray(meta.output);
  const output = formattedJsonOutput?.formatted || meta.output || '';

  return (
    <div className="quiet-tool-detail">
      <div className="quiet-tool-line">
        <span className={toolResult.success ? 'quiet-tool-emphasis' : 'quiet-tool-warning'}>
          {toolResult.success ? 'executed' : 'failed'}
        </span>
        {meta.runtime ? <span className="quiet-tool-muted">{meta.runtime}</span> : null}
      </div>

      {!toolResult.success && toolResult.error ? (
        <div className="tool-note">{toolResult.error}</div>
      ) : null}

      {meta.code ? (
        <div className="quiet-tool-sections">
          <div className="quiet-tool-section-title">Code</div>
          <ReferenceCodeBlock content={meta.code} language="typescript" />
        </div>
      ) : null}

      {output ? (
        <div className="quiet-tool-sections">
          <div className="quiet-tool-section-title">Output</div>
          {formattedJsonOutput ? (
            <ReferenceCodeBlock content={output} language="json" />
          ) : (
            <ReferenceTerminal output={output} />
          )}
        </div>
      ) : (
        <div className="quiet-tool-empty">Execution completed without output.</div>
      )}
    </div>
  );
};

export default CodeExecutionRenderer;
