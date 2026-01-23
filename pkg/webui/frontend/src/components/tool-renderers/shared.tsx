import React from 'react';
import { copyToClipboard, escapeUrl } from '../../utils';

// Shared components for tool renderers

interface ToolCardProps {
  title: string;
  badge?: {
    text: string;
    className?: string;
  };
  actions?: React.ReactNode;
  children: React.ReactNode;
  className?: string;
}

export const ToolCard: React.FC<ToolCardProps> = ({
  title,
  badge,
  actions,
  children,
  className = 'bg-kodelet-light-gray/20'
}) => {
  return (
    <div className={`${className} border border-kodelet-mid-gray/20 rounded p-3`} role="article">
      <div className="flex items-center justify-between mb-3">
        <div className="flex items-center gap-2">
          <h4 className="font-heading font-semibold text-sm text-kodelet-dark">{title}</h4>
          {badge && (
            <div className={`px-2 py-0.5 rounded text-xs font-heading font-medium ${badge.className || 'bg-kodelet-blue/10 text-kodelet-blue border border-kodelet-blue/20'}`}>
              {badge.text}
            </div>
          )}
        </div>
        {actions && <div>{actions}</div>}
      </div>
      {children}
    </div>
  );
};

interface CollapsibleProps {
  title: string;
  children: React.ReactNode;
  collapsed?: boolean;
  badge?: {
    text: string;
    className?: string;
  };
}

export const Collapsible: React.FC<CollapsibleProps> = ({
  title,
  children,
  collapsed = false,
  badge
}) => {
  const collapseId = `collapse-${Math.random().toString(36).substr(2, 9)}`;

  return (
    <div className="collapse collapse-arrow bg-kodelet-light/50 border border-kodelet-mid-gray/20 mt-2 rounded" role="region">
      <input
        type="checkbox"
        id={collapseId}
        defaultChecked={!collapsed}
        aria-expanded={!collapsed}
        aria-controls={`${collapseId}-content`}
      />
      <label
        htmlFor={collapseId}
        className="collapse-title text-xs font-heading font-medium flex items-center justify-between cursor-pointer"
      >
        <span className="text-kodelet-dark">{title}</span>
        {badge && (
          <div
            className={`px-2 py-0.5 rounded text-xs font-heading font-medium ${badge.className || 'bg-kodelet-blue/10 text-kodelet-blue border border-kodelet-blue/20'}`}
            aria-label={badge.text}
          >
            {badge.text}
          </div>
        )}
      </label>
      <div className="collapse-content" id={`${collapseId}-content`}>
        {children}
      </div>
    </div>
  );
};

interface CopyButtonProps {
  content: string;
  className?: string;
}

export const CopyButton: React.FC<CopyButtonProps> = ({
  content,
  className = 'btn-xs'
}) => {
  const handleCopy = () => {
    copyToClipboard(content);
  };

  return (
    <button
      className={`btn btn-ghost ${className}`}
      onClick={handleCopy}
      title="Copy to clipboard"
      aria-label="Copy to clipboard"
    >
      <svg
        xmlns="http://www.w3.org/2000/svg"
        className="h-4 w-4"
        fill="none"
        viewBox="0 0 24 24"
        stroke="currentColor"
        aria-hidden="true"
      >
        <path
          strokeLinecap="round"
          strokeLinejoin="round"
          strokeWidth="2"
          d="M8 16H6a2 2 0 01-2-2V6a2 2 0 012-2h8a2 2 0 012 2v2m-6 12h8a2 2 0 002-2v-8a2 2 0 00-2-2h-8a2 2 0 00-2 2v8a2 2 0 002 2z"
        />
      </svg>
    </button>
  );
};

interface CodeBlockProps {
  code: string;
  language?: string;
  showLineNumbers?: boolean;
  maxHeight?: number;
}

export const CodeBlock: React.FC<CodeBlockProps> = ({
  code,
  language = '',
  showLineNumbers = true,
  maxHeight
}) => {
  const lines = code.split('\n');
  const heightStyle = maxHeight ? { maxHeight: `${maxHeight}px`, overflowY: 'auto' as const } : {};

  return (
    <div
      className="bg-kodelet-light border border-kodelet-mid-gray/20 rounded text-sm font-mono"
      style={heightStyle}
      role="region"
      aria-label="Code block"
    >
      <pre className="p-3">
        <code className={`language-${language} text-kodelet-dark`}>
          {showLineNumbers
            ? lines.map((line, index) => {
                const lineNumber = (index + 1).toString().padStart(3, ' ');
                return (
                  <div key={index}>
                    <span className="line-number text-kodelet-mid-gray select-none mr-2" aria-hidden="true">
                      {lineNumber}
                    </span>
                    <span className="line-content">{line || ' '}</span>
                  </div>
                );
              })
            : code}
        </code>
      </pre>
    </div>
  );
};

interface MetadataRowProps {
  label: string;
  value: string | number | null | undefined;
  monospace?: boolean;
}

export const MetadataRow: React.FC<MetadataRowProps> = ({ label, value, monospace = false }) => {
  if (value === null || value === undefined) return null;

  return (
    <div className="flex items-center gap-2 text-xs">
      <strong className="font-heading font-medium text-kodelet-mid-gray">{label}:</strong>
      <span className={`${monospace ? 'font-mono' : 'font-body'} text-kodelet-dark`}>{String(value)}</span>
    </div>
  );
};

interface StatusBadgeProps {
  text: string;
  variant?: 'success' | 'warning' | 'info' | 'error' | 'neutral';
}

export const StatusBadge: React.FC<StatusBadgeProps> = ({ text, variant = 'neutral' }) => {
  const variantClasses = {
    success: 'bg-kodelet-green/10 text-kodelet-green border-kodelet-green/20',
    warning: 'bg-kodelet-orange/10 text-kodelet-orange border-kodelet-orange/20',
    info: 'bg-kodelet-blue/10 text-kodelet-blue border-kodelet-blue/20',
    error: 'bg-red-500/10 text-red-600 border-red-500/20',
    neutral: 'bg-kodelet-mid-gray/10 text-kodelet-mid-gray border-kodelet-mid-gray/20',
  };

  return (
    <span className={`px-1.5 py-0.5 rounded text-xs font-medium border ${variantClasses[variant]}`}>
      {text}
    </span>
  );
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
      className={`link link-hover ${className}`}
      aria-label="Open in new tab"
    >
      {children}
      <svg
        xmlns="http://www.w3.org/2000/svg"
        className="h-4 w-4 inline ml-1"
        fill="none"
        viewBox="0 0 24 24"
        stroke="currentColor"
        aria-hidden="true"
      >
        <path
          strokeLinecap="round"
          strokeLinejoin="round"
          strokeWidth="2"
          d="M10 6H6a2 2 0 00-2 2v10a2 2 0 002 2h10a2 2 0 002-2v-4M14 4h6m0 0v6m0-6L10 14"
        />
      </svg>
    </a>
  );
};

