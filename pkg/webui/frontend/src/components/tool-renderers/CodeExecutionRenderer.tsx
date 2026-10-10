import { Check, ChevronRight, X } from 'lucide-react';
import Prism from 'prismjs';
import type React from 'react';
import { useMemo } from 'react';
import type { CodeExecutionMetadata, ToolRenderProps, ToolResult } from '../../types';
import { cn } from '../../utils';
import ChatToolActivity from '../chat/ChatToolActivity';
import Spinner from '../Spinner';
import { ReferenceCodeBlock, ReferenceToolNote } from './reference';
import { ImageAttachment } from './ToolImageAttachments';

export const codeExecutionSummary = (result?: ToolResult): string => {
  const meta = result?.metadata as CodeExecutionMetadata | undefined;
  if (!Array.isArray(meta?.calls)) return 'Code execution';
  const succeeded = meta.calls.filter((call) => call.status === 'completed').length;
  const running = meta.calls.filter((call) => ['queued', 'running'].includes(call.status)).length;
  const failed = meta.calls.length - succeeded - running;
  return [
    'Code execution',
    succeeded && `${succeeded} succeeded`,
    failed && `${failed} failed`,
    running && `${running} running`,
  ]
    .filter(Boolean)
    .join(' · ');
};

const Section: React.FC<
  React.PropsWithChildren<{ title: string; open?: boolean; status?: string }>
> = ({ title, open, status, children }) => {
  const running = status === 'queued' || status === 'running';
  const failed = status !== undefined && status !== 'completed' && !running;
  return (
    <details
      className={cn(
        'activity-card',
        running && 'activity-card-live',
        failed && 'activity-card-error'
      )}
      open={open}
    >
      <summary className="tool-summary activity-summary">
        {status !== undefined ? (
          <span className="activity-marker" aria-hidden="true">
            {running ? <Spinner /> : failed ? <X size={14} /> : <Check size={14} />}
          </span>
        ) : null}
        <span className="tool-summary-label">{title}</span>
        <ChevronRight className="tool-summary-chevron" size={12} aria-hidden="true" />
      </summary>
      <div className="activity-detail-content">{children}</div>
    </details>
  );
};

export default function CodeExecutionRenderer({
  toolResult,
  toolInput,
  isPartial,
}: ToolRenderProps) {
  const code = useMemo(() => {
    try {
      const value = JSON.parse(toolInput || '{}').code;
      return typeof value === 'string'
        ? Prism.highlight(value, Prism.languages.javascript, 'javascript')
        : '';
    } catch {
      return '';
    }
  }, [toolInput]);
  const meta = toolResult.metadata as CodeExecutionMetadata | undefined;
  const items = meta?.items ?? meta?.outputs?.map((value) => ({ type: 'json' as const, value }));
  const hasMedia = items?.some((item) => item.type === 'image' || item.type === 'artifact');
  return (
    <div className="activity-stack">
      <Section title="Code" open={hasMedia || undefined}>
        {code ? (
          <pre className="tool-code-block chat-prose">
            {/* biome-ignore lint/security/noDangerouslySetInnerHtml: Prism escapes the JavaScript source before adding token markup. */}
            <code className="language-javascript" dangerouslySetInnerHTML={{ __html: code }} />
          </pre>
        ) : (
          <ReferenceToolNote text="Code is unavailable for this invocation." />
        )}
        {items?.length ? (
          <div className="quiet-tool-detail mt-3">
            {items.map((item, index) => {
              if (item.type === 'json') {
                return (
                  <ReferenceCodeBlock
                    content={
                      typeof item.value === 'string'
                        ? item.value
                        : JSON.stringify(item.value, null, 2)
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
                      {item.type === 'image' && item.detail === 'original'
                        ? ' · Original detail'
                        : ''}
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
          </div>
        ) : null}
      </Section>
      {meta?.calls?.map((call) => {
        const running = call.status === 'running' || call.status === 'queued';
        return call.result || (running && !call.detailsOmitted) ? (
          <ChatToolActivity
            key={call.callId}
            nested
            tools={[
              {
                callId: call.callId,
                name: call.toolName,
                input: JSON.stringify(call.input ?? {}),
                inProgress: running,
                result: call.result && {
                  ...call.result,
                  success: call.result.success && (running || call.status === 'completed'),
                },
              },
            ]}
          />
        ) : (
          <Section
            key={call.callId}
            title={`${call.toolName} · ${call.status}`}
            status={call.status}
          >
            <ReferenceToolNote
              text={
                call.detailsOmitted
                  ? 'Child tool details exceeded the storage limit.'
                  : 'Child tool details were not saved for this invocation.'
              }
            />
          </Section>
        );
      })}
      {toolResult.error ? <ReferenceToolNote text={toolResult.error} /> : null}
    </div>
  );
}
