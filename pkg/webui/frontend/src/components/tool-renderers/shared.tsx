import React from 'react';
import { copyToClipboard, escapeHtml, detectLanguageFromPath, formatFileSize, formatTimestamp, formatDuration, escapeUrl } from '../../utils';

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
  className = 'bg-base-200'
}) => {
  return (
    <div className={`card ${className} border`} role="article">
      <div className="card-body">
        <div className="flex items-center justify-between mb-3">
          <div className="flex items-center gap-2">
            <h4 className="font-semibold">{title}</h4>
            {badge && (
              <div className={`badge badge-sm ${badge.className || 'badge-info'}`}>
                {badge.text}
              </div>
            )}
          </div>
          {actions && <div className="card-actions">{actions}</div>}
        </div>
        {children}
      </div>
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
    <div className="collapse collapse-arrow bg-base-100 mt-2" role="region">
      <input
        type="checkbox"
        id={collapseId}
        defaultChecked={!collapsed}
        aria-expanded={!collapsed}
        aria-controls={`${collapseId}-content`}
      />
      <label
        htmlFor={collapseId}
        className="collapse-title text-sm font-medium flex items-center justify-between cursor-pointer"
      >
        <span>{title}</span>
        {badge && (
          <div
            className={`badge badge-sm ${badge.className || 'badge-info'}`}
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
      className="mockup-code bg-base-300 text-sm"
      style={heightStyle}
      role="region"
      aria-label="Code block"
    >
      <pre>
        <code className={`language-${language}`}>
          {showLineNumbers
            ? lines.map((line, index) => {
                const lineNumber = (index + 1).toString().padStart(3, ' ');
                return (
                  <div key={index}>
                    <span className="line-number text-base-content/50 select-none mr-2" aria-hidden="true">
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
    <div className="flex items-center gap-4">
      <strong>{label}:</strong>
      <span className={monospace ? 'font-mono' : ''}>{String(value)}</span>
    </div>
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

// Helper functions for tool renderers
export const getMetadata = (toolResult: any, ...paths: string[]): any => {
  let value = toolResult.metadata;
  for (const path of paths) {
    if (!value) return null;
    value = value[path];
  }
  return value;
};

export const getMetadataAny = (toolResult: any, paths: string[]): any => {
  for (const path of paths) {
    const value = getMetadata(toolResult, ...path.split('.'));
    if (value !== null && value !== undefined) return value;
  }
  return null;
};

// File icon utility
export const getFileIcon = (path: string): string => {
  if (!path) return '📄';
  const ext = path.split('.').pop()?.toLowerCase();
  const iconMap: Record<string, string> = {
    'js': '📜', 'ts': '📜', 'py': '🐍', 'go': '🐹', 'java': '☕',
    'html': '🌐', 'css': '🎨', 'json': '📋', 'xml': '📄',
    'md': '📝', 'txt': '📄', 'log': '📊',
    'jpg': '🖼️', 'jpeg': '🖼️', 'png': '🖼️', 'gif': '🖼️',
    'pdf': '📕', 'doc': '📘', 'docx': '📘',
    'zip': '📦', 'tar': '📦', 'gz': '📦'
  };
  return iconMap[ext || ''] || '📄';
};

// Check if image file
export const isImageFile = (path: string): boolean => {
  const imageExts = ['.png', '.jpg', '.jpeg', '.gif', '.bmp', '.webp'];
  return imageExts.some(ext => path.toLowerCase().endsWith(ext));
};

// Export utility functions for use in individual renderers
export {
  escapeHtml,
  detectLanguageFromPath,
  formatFileSize,
  formatTimestamp,
  formatDuration,
  escapeUrl
};