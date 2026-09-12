import React from 'react';
import { Copy, ExternalLink as ExternalLinkIcon } from 'lucide-react';
import { copyToClipboard, escapeUrl } from '../../utils';

// Shared components for tool renderers

interface CopyButtonProps {
  content: string;
  className?: string;
}

export const CopyButton: React.FC<CopyButtonProps> = ({
  content,
  className = ''
}) => {
  const handleCopy = () => {
    copyToClipboard(content);
  };

  return (
    <button
      className={`panel-action-button ${className}`.trim()}
      onClick={handleCopy}
      title="Copy to clipboard"
      aria-label="Copy to clipboard"
    >
      <Copy aria-hidden="true" className="h-4 w-4" strokeWidth={2} />
    </button>
  );
};

type JsonObjectOrArray = Record<string, unknown> | unknown[];

export const safeStringify = (obj: unknown): string => {
  const seen = new WeakSet();
  return JSON.stringify(obj, (_key, val) => {
    if (val != null && typeof val === 'object') {
      if (seen.has(val)) {
        return '[Circular]';
      }
      seen.add(val);
    }
    return val;
  }, 2);
};

export interface FormattedJson {
  formatted: string;
  parsed: JsonObjectOrArray;
}

export const formatJsonObjectOrArray = (value?: string | null): FormattedJson | null => {
  if (!value?.trim()) {
    return null;
  }

  try {
    const parsed = JSON.parse(value);
    if (!parsed || typeof parsed !== 'object') {
      return null;
    }

    return {
      formatted: JSON.stringify(parsed, null, 2),
      parsed: parsed as JsonObjectOrArray,
    };
  } catch {
    return null;
  }
};

interface ExternalLinkProps {
  href: string;
  children: React.ReactNode;
  className?: string;
}

export const ExternalLink: React.FC<ExternalLinkProps> = ({ href, children, className = '' }) => {
  const safeUrl = escapeUrl(href);

  if (safeUrl === '#') {
    return <span className="text-base-content/60">Invalid URL</span>;
  }

  return (
    <a
      href={safeUrl}
      target="_blank"
      rel="noopener noreferrer"
      className={`tool-action-link normal-case tracking-normal ${className}`.trim()}
      aria-label="Open in new tab"
    >
      {children}
      <ExternalLinkIcon aria-hidden="true" className="ml-1 inline h-4 w-4" strokeWidth={2} />
    </a>
  );
};
