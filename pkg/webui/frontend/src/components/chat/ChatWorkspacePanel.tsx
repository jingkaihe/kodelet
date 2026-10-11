import { GitCompareArrows, Globe, PanelRight, SquareTerminal } from 'lucide-react';
import { type CSSProperties, lazy, Suspense } from 'react';
import type { useChatLayout } from '../../features/chat/useChatLayout';
import type { useWorkspaceResize } from '../../features/chat/useWorkspaceResize';
import type { BrowserTarget, GitDiffResponse, WorkspaceTarget } from '../../types';
import { cn } from '../../utils';

const GitDiffModal = lazy(() => import('../workspace/GitDiffModal'));
const TerminalModal = lazy(() => import('../workspace/TerminalModal'));
const BrowserPanel = lazy(() => import('../workspace/BrowserPanel'));

interface ChatWorkspacePanelProps {
  layout: Pick<
    ReturnType<typeof useChatLayout>,
    | 'workspaceOverlayOpen'
    | 'workspacePanelOpen'
    | 'workspacePanelView'
    | 'workspaceOverlayLayout'
    | 'workspaceToolsRef'
    | 'sidebarVisible'
    | 'sidebarOverlayOpen'
  >;
  resize: ReturnType<typeof useWorkspaceResize>;
  terminalAvailable: boolean;
  gitDiffAvailable: boolean;
  browserAvailable: boolean;
  workspaceTarget: WorkspaceTarget;
  workspaceTargetKey: string;
  browserTarget: BrowserTarget;
  browserTargetKey: string;
  cwdLabel: string;
  gitDiff: GitDiffResponse | null;
  gitDiffError: string | null;
  gitDiffLoading: boolean;
  onToggle: () => void;
  onSelectTerminal: () => void;
  onSelectGitDiff: () => void;
  onSelectBrowser: () => void;
  onRefreshGitDiff: () => void;
}

const ChatWorkspacePanel = ({
  layout: {
    workspaceOverlayOpen,
    workspacePanelOpen,
    workspacePanelView,
    workspaceOverlayLayout,
    workspaceToolsRef,
    sidebarVisible,
    sidebarOverlayOpen,
  },
  resize: {
    workspaceSize,
    isResizingWorkspace,
    workspaceResizerRef,
    handleWorkspaceResizeStart,
    handleWorkspaceResizeKeyDown,
  },
  terminalAvailable,
  gitDiffAvailable,
  browserAvailable,
  workspaceTarget,
  workspaceTargetKey,
  browserTarget,
  browserTargetKey,
  cwdLabel,
  gitDiff,
  gitDiffError,
  gitDiffLoading,
  onToggle,
  onSelectTerminal,
  onSelectGitDiff,
  onSelectBrowser,
  onRefreshGitDiff,
}: ChatWorkspacePanelProps) => {
  const workspaceViewButtons = [
    {
      view: 'terminal',
      label: 'Terminal',
      Icon: SquareTerminal,
      available: terminalAvailable,
      onClick: onSelectTerminal,
    },
    {
      view: 'diff',
      label: 'Changes',
      Icon: GitCompareArrows,
      available: gitDiffAvailable,
      onClick: onSelectGitDiff,
    },
    {
      view: 'browser',
      label: 'Browser',
      Icon: Globe,
      available: browserAvailable,
      onClick: onSelectBrowser,
    },
  ].map(({ view, label, Icon, available, onClick }) =>
    available ? (
      <button
        aria-controls="workspace-tools"
        aria-label={`Show ${label.toLowerCase()}`}
        aria-pressed={workspacePanelView === view}
        className={cn('sidebar-toggle-button', workspacePanelView === view && 'is-active')}
        data-testid={`workspace-tools-${view}-tab`}
        key={view}
        onClick={onClick}
        title={label}
        type="button"
      >
        <Icon aria-hidden="true" className="h-4 w-4" strokeWidth={1.9} />
      </button>
    ) : null
  );

  return (
    <aside
      aria-label="Workspace tools"
      {...(workspaceOverlayOpen ? { role: 'dialog', 'aria-modal': true } : {})}
      className={cn(
        'workspace-tools-shell',
        workspacePanelOpen && 'is-open',
        sidebarVisible && 'is-obscured'
      )}
      data-testid="workspace-tools-shell"
      id="workspace-tools"
      inert={sidebarOverlayOpen || undefined}
      ref={workspaceToolsRef}
      style={
        workspacePanelOpen && !workspaceOverlayLayout && workspaceSize.width > 0
          ? ({ '--workspace-width': `${workspaceSize.width}px` } as CSSProperties)
          : undefined
      }
      tabIndex={workspaceOverlayOpen ? -1 : undefined}
    >
      {workspacePanelOpen && !workspaceOverlayLayout ? (
        <hr
          aria-controls="workspace-tools"
          aria-label="Resize workspace panel"
          aria-orientation="vertical"
          aria-valuemax={workspaceSize.max}
          aria-valuemin={workspaceSize.min}
          aria-valuenow={workspaceSize.width}
          aria-valuetext={`${workspaceSize.width} pixels`}
          className="workspace-tools-resizer"
          data-testid="workspace-tools-resizer"
          onKeyDown={handleWorkspaceResizeKeyDown}
          onPointerDown={handleWorkspaceResizeStart}
          ref={workspaceResizerRef}
          tabIndex={0}
        />
      ) : null}
      {isResizingWorkspace && !workspaceOverlayLayout ? (
        <div aria-hidden="true" className="workspace-resize-shield" />
      ) : null}
      {workspacePanelOpen ? (
        <div
          className={cn('workspace-tools-dock', workspacePanelView === 'terminal' && 'is-terminal')}
          data-testid="workspace-tools-dock"
        >
          <div className="workspace-tools-content">
            <Suspense
              fallback={
                <output className="workspace-modal-placeholder">Loading workspace tool…</output>
              }
            >
              {workspacePanelView === 'terminal' ? (
                <TerminalModal
                  key={workspaceTargetKey}
                  cwdLabel={cwdLabel}
                  open
                  onClose={onToggle}
                  target={workspaceTarget}
                />
              ) : workspacePanelView === 'browser' ? (
                <BrowserPanel key={browserTargetKey} target={browserTarget} />
              ) : (
                <GitDiffModal
                  error={gitDiffError}
                  gitDiff={gitDiff}
                  loading={gitDiffLoading}
                  open
                  onRefresh={onRefreshGitDiff}
                />
              )}
            </Suspense>
          </div>
        </div>
      ) : null}

      <div className="workspace-tools-rail" data-testid="workspace-tools-rail">
        <button
          aria-label={workspacePanelOpen ? 'Hide workspace panel' : 'Show workspace panel'}
          aria-pressed={workspacePanelOpen}
          className="sidebar-toggle-button workspace-tools-toggle"
          data-testid="workspace-tools-toggle"
          onClick={onToggle}
          type="button"
        >
          <PanelRight aria-hidden="true" className="h-4 w-4" strokeWidth={1.9} />
        </button>
        {workspacePanelOpen || !workspaceOverlayLayout ? workspaceViewButtons : null}
      </div>
    </aside>
  );
};

export default ChatWorkspacePanel;
