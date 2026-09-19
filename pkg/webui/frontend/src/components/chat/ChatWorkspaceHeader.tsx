import { ChevronDown, FolderOpen } from 'lucide-react';

interface ChatWorkspaceHeaderProps {
  cwd: string;
  loading?: boolean;
  disabled?: boolean;
  onWorkspaceOpen?: () => void;
}

const ChatWorkspaceHeader = ({
  cwd,
  loading,
  disabled,
  onWorkspaceOpen,
}: ChatWorkspaceHeaderProps) => {
  const path = cwd.replace(/\/$/, '') || cwd;
  const lastSlash = path.lastIndexOf('/');
  const label = loading
    ? 'Loading workspace…'
    : onWorkspaceOpen
      ? 'Select workspace'
      : 'Workspace unavailable';
  const content = (
    <>
      <FolderOpen aria-hidden="true" className="h-4 w-4 shrink-0" strokeWidth={1.6} />
      <span className="chat-workspace-path" title={cwd || undefined}>
        {path ? (
          <>
            <span className="chat-workspace-path-prefix">{path.slice(0, lastSlash + 1)}</span>
            <span className="chat-workspace-path-name">{path.slice(lastSlash + 1)}</span>
          </>
        ) : (
          label
        )}
      </span>
    </>
  );

  return (
    <section
      aria-label="Workspace"
      className="chat-workspace-header"
      data-testid="chat-workspace-header"
    >
      {onWorkspaceOpen ? (
        <button
          aria-haspopup="dialog"
          aria-label={`Change workspace: ${cwd || label}`}
          className="chat-workspace-location"
          disabled={disabled || loading}
          onClick={onWorkspaceOpen}
          type="button"
        >
          {content}
          <ChevronDown aria-hidden="true" className="h-3 w-3 shrink-0" />
        </button>
      ) : (
        <div className="chat-workspace-location">{content}</div>
      )}
    </section>
  );
};

export default ChatWorkspaceHeader;
