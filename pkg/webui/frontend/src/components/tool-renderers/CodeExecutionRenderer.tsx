import React from 'react';
import { CodeExecutionMetadata, ToolResult } from '../../types';
import { ReferenceCodeBlock, ReferenceTerminal } from './reference';
import { formatJsonObjectOrArray } from './shared';

interface CodeExecutionRendererProps {
  toolResult: ToolResult;
  toolInput?: string;
}

const parseToolInput = (toolInput?: string): Record<string, unknown> | null => {
  if (!toolInput) {
    return null;
  }

  try {
    const parsed = JSON.parse(toolInput);
    return parsed && typeof parsed === 'object' && !Array.isArray(parsed)
      ? (parsed as Record<string, unknown>)
      : null;
  } catch {
    return null;
  }
};

const stringField = (source: Record<string, unknown> | null, key: string): string | undefined => {
  const value = source?.[key];
  return typeof value === 'string' && value.trim().length > 0 ? value.trim() : undefined;
};

const removeEmbeddedCodeEcho = (output: string): string => {
  const marker = '### Ran Playwright code';
  const markerIndex = output.indexOf(marker);
  if (markerIndex === -1) {
    return output;
  }

  const beforeMarker = output.slice(0, markerIndex).trimEnd();
  const afterMarker = output.slice(markerIndex + marker.length).trimStart();
  const echoedCodeFence = afterMarker.match(/^```[\s\S]*?```\s*/);
  const afterFence = echoedCodeFence
    ? afterMarker.slice(echoedCodeFence[0].length).trimStart()
    : '';
  return [beforeMarker, afterFence].filter(Boolean).join('\n\n');
};

const removeMarkdownResultHeading = (output: string): string =>
  output.replace(/^#{1,6}\s+result\s*\n+/i, '').trimStart();

const sanitizeOutput = (output: string): string =>
  removeMarkdownResultHeading(removeEmbeddedCodeEcho(output)).trimEnd();

const CodeExecutionRenderer: React.FC<CodeExecutionRendererProps> = ({ toolResult, toolInput }) => {
  const meta = toolResult.metadata as CodeExecutionMetadata;
  if (!meta) return null;

  const input = parseToolInput(toolInput);
  const codePath = stringField(input, 'code_path');
  const description = stringField(input, 'description');
  const sanitizedOutput = sanitizeOutput(meta.output || '');
  const formattedJsonOutput = formatJsonObjectOrArray(sanitizedOutput);
  const output = formattedJsonOutput?.formatted || sanitizedOutput;

  return (
    <div className="quiet-tool-detail">
      <div className="quiet-tool-line">
        <span className={toolResult.success ? 'quiet-tool-emphasis' : 'quiet-tool-warning'}>
          {toolResult.success ? 'executed' : 'failed'}
        </span>
        {meta.runtime ? <span className="quiet-tool-muted">{meta.runtime}</span> : null}
        {codePath ? <span className="quiet-tool-muted mono">{codePath}</span> : null}
      </div>

      {description ? <div className="quiet-tool-path">{description}</div> : null}

      {!toolResult.success && toolResult.error ? (
        <div className="tool-note">{toolResult.error}</div>
      ) : null}

      {meta.code ? (
        <div className="quiet-tool-sections">
          <div className="quiet-tool-section-title">Code</div>
          <div className="code-execution-preview">
            <ReferenceCodeBlock content={meta.code} language="typescript" />
          </div>
        </div>
      ) : null}

      {output ? (
        <div className="quiet-tool-sections">
          <div className="quiet-tool-section-title">Output</div>
          {formattedJsonOutput ? (
            <div className="code-execution-output">
              <ReferenceCodeBlock content={output} language="json" />
            </div>
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
