import type React from 'react';
import type { CodeExecutionMetadata, ToolRenderProps, ToolResult } from '../../types';
import { ReferenceCodeBlock, ReferenceToolNote } from './reference';
import { ImageAttachment } from './ToolImageAttachments';

export const codeExecutionSummary = (result?: ToolResult): string => {
  const meta = result?.metadata as CodeExecutionMetadata | undefined;
  if (!Array.isArray(meta?.calls)) return 'Code execution';
  const succeeded = meta.calls.filter((call) => call.status === 'completed').length;
  const running = meta.calls.filter((call) => ['queued', 'running'].includes(call.status)).length;
  const failed = meta.calls.length - succeeded - running;
  return `Code execution · ${succeeded} succeeded · ${failed} failed · ${running} running`;
};

const CodeExecutionRenderer: React.FC<ToolRenderProps> = ({ toolResult, isPartial }) => {
  const meta = toolResult.metadata as CodeExecutionMetadata | undefined;
  if (!meta) return toolResult.error ? <ReferenceToolNote text={toolResult.error} /> : null;
  const items = meta.items ?? meta.outputs?.map((value) => ({ type: 'json' as const, value }));
  return (
    <div className="quiet-tool-detail">
      <div className="quiet-tool-line">
        <span>{isPartial ? 'Running' : meta.status}</span>
        <span className="quiet-tool-muted">{meta.durationMs} ms</span>
      </div>
      {meta.calls?.map((call) => (
        <div className="quiet-tool-line" key={call.callId}>
          <span>{call.toolName}</span>
          <span className="quiet-tool-muted">
            {call.status} · {call.durationMs} ms{call.errorKind ? ` · ${call.errorKind}` : ''}
          </span>
        </div>
      ))}
      {items?.map((item, index) => {
        if (item.type === 'json') {
          return (
            <ReferenceCodeBlock
              content={
                typeof item.value === 'string' ? item.value : JSON.stringify(item.value, null, 2)
              }
              // biome-ignore lint/suspicious/noArrayIndexKey: output entries are immutable and append-only.
              key={index}
              language={typeof item.value === 'string' ? 'text' : 'json'}
            />
          );
        }
        if (item.type !== 'image' && item.type !== 'artifact') return null;
        const attachment = toolResult.attachments?.find(
          (candidate) =>
            candidate.type === 'image' &&
            candidate.artifactId === item.artifactId &&
            !!candidate.artifactId &&
            !candidate.error
        );
        return (
          // biome-ignore lint/suspicious/noArrayIndexKey: the same artifact may be explicitly selected more than once.
          <div key={index}>
            <div className="quiet-tool-line">
              <span>
                {item.type === 'image'
                  ? 'Image sent to model'
                  : 'Retained artifact (not sent to model)'}
                {item.type === 'image' && item.detail === 'original' ? ' · Original detail' : ''}
              </span>
              <code className="quiet-tool-muted">{item.artifactId}</code>
            </div>
            {!isPartial ? (
              attachment ? (
                <div className="tool-image-attachments">
                  <ImageAttachment attachment={attachment} viewed={item.type === 'image'} />
                </div>
              ) : (
                <p className="quiet-tool-warning">Image preview unavailable.</p>
              )
            ) : null}
          </div>
        );
      })}
      {toolResult.error ? <ReferenceToolNote text={toolResult.error} /> : null}
      {!isPartial && !toolResult.error && !items?.length ? (
        <p className="quiet-tool-empty">Code completed without selected output.</p>
      ) : null}
    </div>
  );
};

export default CodeExecutionRenderer;
