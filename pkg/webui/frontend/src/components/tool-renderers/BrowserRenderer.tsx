import type React from 'react';
import type { BrowserMetadata, ToolResult } from '../../types';
import { ReferenceCodeBlock, ReferenceToolKVGrid } from './reference';
import { formatJsonObjectOrArray } from './shared';

export const getBrowserMetadata = (
  toolResult?: ToolResult,
  toolInput?: string
): BrowserMetadata => {
  let metadata = toolResult?.metadata as Partial<BrowserMetadata> | undefined;
  if (!metadata) {
    try {
      metadata = JSON.parse(toolInput || '{}');
    } catch {
      // Streaming arguments can be incomplete.
    }
  }
  const field = (key: keyof BrowserMetadata): string | undefined => {
    const value = metadata?.[key];
    return typeof value === 'string' ? value : undefined;
  };

  return {
    action: field('action') || '',
    url: field('url'),
    expression: field('expression'),
    path: field('path'),
    sessionId: field('sessionId'),
    output: toolResult?.metadata ? field('output') : undefined,
  };
};

const BrowserRenderer: React.FC<{
  toolResult?: ToolResult;
  toolInput?: string;
  isPartial?: boolean;
}> = ({ toolResult, toolInput, isPartial }) => {
  const meta = getBrowserMetadata(toolResult, toolInput);
  const running = isPartial || !toolResult;
  const failed = toolResult && !toolResult.success;
  const output = meta.action === 'screenshot' ? undefined : meta.output;
  const formattedOutput = formatJsonObjectOrArray(output);

  return (
    <div className="quiet-tool-detail">
      {meta.action === 'evaluate' && meta.expression ? (
        <ReferenceCodeBlock content={meta.expression} language="javascript" />
      ) : null}

      <ReferenceToolKVGrid
        items={[
          {
            label: 'Path',
            value: meta.action === 'screenshot' ? meta.path : undefined,
            monospace: true,
          },
          {
            label: 'Session',
            value: meta.action === 'stop' ? meta.sessionId : undefined,
            monospace: true,
          },
        ]}
      />

      {failed ? (
        <div className="quiet-tool-warning" role="alert">
          {toolResult.error || 'Browser action failed.'}
        </div>
      ) : null}

      {output ? (
        <ReferenceCodeBlock
          content={formattedOutput?.formatted || output}
          language={formattedOutput ? 'json' : 'text'}
        />
      ) : running ? (
        <p className="tool-awaiting">Awaiting browser result…</p>
      ) : !failed && meta.action !== 'screenshot' ? (
        <p className="quiet-tool-empty">Browser action completed without output.</p>
      ) : null}
    </div>
  );
};

export default BrowserRenderer;
