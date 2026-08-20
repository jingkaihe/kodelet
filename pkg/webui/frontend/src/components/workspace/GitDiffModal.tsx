import React, { useMemo, useState } from 'react';
import {
  ChevronRight,
  Copy,
  FoldVertical,
  RefreshCw,
  UnfoldVertical,
} from 'lucide-react';
import { copyToClipboard } from '../../utils';
import { parseUnifiedDiff, ReferenceDiffBlock } from '../tool-renderers/reference';
import type { GitDiffResponse } from '../../types';

interface GitDiffModalProps {
  cwdLabel?: string;
  error: string | null;
  gitDiff: GitDiffResponse | null;
  loading: boolean;
  onClose?: () => void;
  open: boolean;
  onRefresh: () => void;
}

interface GitFileDiff {
  additions: number;
  deletions: number;
  diff: string;
  id: string;
  path: string;
  status: string | null;
}

interface GitDiffSection {
  diff: string;
  pathHint: string | null;
  submodule: boolean;
}

interface AbsolutePathParts {
  root: string;
  segments: string[];
}

interface PendingSubmodule {
  lineIndex: number;
  pathHint: string | null;
}

const gitEscapeBytes: Record<string, number> = {
  a: 7,
  b: 8,
  f: 12,
  n: 10,
  r: 13,
  t: 9,
  v: 11,
  '\\': 92,
  '"': 34,
};

const decodeGitPath = (value: string): string => {
  if (!value.startsWith('"') || !value.endsWith('"')) {
    return value;
  }

  const source = value.slice(1, -1);
  const bytes: number[] = [];
  const encoder = new TextEncoder();

  for (let index = 0; index < source.length; index += 1) {
    const character = source[index];
    if (character !== '\\') {
      bytes.push(...encoder.encode(character));
      continue;
    }

    const escaped = source[index + 1];
    if (!escaped) {
      bytes.push(92);
      continue;
    }

    const octalMatch = source.slice(index + 1).match(/^[0-7]{1,3}/);
    if (octalMatch) {
      bytes.push(Number.parseInt(octalMatch[0], 8));
      index += octalMatch[0].length;
      continue;
    }

    bytes.push(gitEscapeBytes[escaped] ?? escaped.charCodeAt(0));
    index += 1;
  }

  return new TextDecoder().decode(Uint8Array.from(bytes));
};

const extractPathLine = (
  lines: string[],
  prefix: string,
  stripGitPrefix: boolean
): string | null => {
  const line = lines.find((candidate) => candidate.startsWith(prefix));
  if (!line) {
    return null;
  }

  const pathField = line.slice(prefix.length);
  const tabIndex = pathField.lastIndexOf('\t');
  const decodedPath = decodeGitPath(
    tabIndex >= 0 ? pathField.slice(0, tabIndex) : pathField
  );
  if (decodedPath === '/dev/null') {
    return null;
  }

  return stripGitPrefix && /^[ab]\//.test(decodedPath)
    ? decodedPath.slice(2)
    : decodedPath;
};

const extractHeaderPath = (line: string): string | null => {
  const header = line.slice('diff --git '.length);
  const quotedPaths = header.match(/"(?:\\.|[^"\\])*"/g);
  if (quotedPaths && quotedPaths.length >= 2) {
    return decodeGitPath(quotedPaths[1]).replace(/^b\//, '');
  }

  if (header.startsWith('a/')) {
    let targetPrefixIndex = header.indexOf(' b/');
    while (targetPrefixIndex >= 0) {
      const oldPath = header.slice(2, targetPrefixIndex);
      const newPath = header.slice(targetPrefixIndex + 3);
      if (oldPath === newPath) {
        return newPath;
      }
      targetPrefixIndex = header.indexOf(' b/', targetPrefixIndex + 1);
    }
  }

  const targetPrefixIndex = header.lastIndexOf(' b/');
  if (targetPrefixIndex >= 0) {
    return decodeGitPath(header.slice(targetPrefixIndex + 1)).replace(/^b\//, '');
  }

  return null;
};

const extractSubmodulePath = (line: string): string | null => {
  const value = line.slice('Submodule '.length);
  const rangeIndex = value.search(/ [0-9a-f]{7,}\.\.[0-9a-f]{7,}/i);
  if (rangeIndex >= 0) {
    return value.slice(0, rangeIndex);
  }

  const containsIndex = value.lastIndexOf(' contains ');
  if (containsIndex >= 0) {
    return value.slice(0, containsIndex);
  }

  return null;
};

const splitGitDiffSections = (diffText: string): GitDiffSection[] => {
  const sections: GitDiffSection[] = [];
  let currentLines: string[] = [];
  let currentPathHint: string | null = null;
  let currentSubmodule = false;
  let pendingSubmodule: PendingSubmodule | null = null;

  const pushSection = (
    lines: string[],
    pathHint: string | null,
    submodule: boolean
  ) => {
    if (lines.length === 0) {
      return;
    }
    sections.push({
      diff: lines.join('\n'),
      pathHint,
      submodule,
    });
  };

  const flushSection = () => {
    pushSection(currentLines, currentPathHint, currentSubmodule);
    currentLines = [];
    currentPathHint = null;
    currentSubmodule = false;
    pendingSubmodule = null;
  };

  const promotePendingSubmodule = () => {
    if (!pendingSubmodule) {
      return;
    }

    pushSection(
      currentLines.slice(0, pendingSubmodule.lineIndex),
      currentPathHint,
      currentSubmodule
    );
    currentLines = currentLines.slice(pendingSubmodule.lineIndex);
    currentPathHint = pendingSubmodule.pathHint;
    currentSubmodule = true;
    pendingSubmodule = null;
  };

  const appendDiffHeader = (line: string, headerPath: string | null) => {
    const nestedSubmoduleDiff =
      currentSubmodule &&
      currentPathHint !== null &&
      headerPath !== null &&
      headerPath.startsWith(`${currentPathHint}/`);
    if (nestedSubmoduleDiff) {
      pendingSubmodule = null;
      currentLines.push(line);
      return;
    }

    if (currentSubmodule && pendingSubmodule) {
      promotePendingSubmodule();
      const promotedSubmoduleDiff =
        currentPathHint !== null &&
        headerPath !== null &&
        headerPath.startsWith(`${currentPathHint}/`);
      if (promotedSubmoduleDiff) {
        currentLines.push(line);
        return;
      }
    }

    flushSection();
    currentPathHint = headerPath;
    currentLines.push(line);
  };

  diffText.split('\n').forEach((line) => {
    if (line.startsWith('Submodule ')) {
      const submodulePath = extractSubmodulePath(line);
      if (currentSubmodule) {
        pendingSubmodule ??= {
          lineIndex: currentLines.length,
          pathHint: submodulePath,
        };
      } else {
        flushSection();
        currentPathHint = submodulePath;
        currentSubmodule = true;
      }
      currentLines.push(line);
      return;
    }

    if (line.startsWith('diff --git ')) {
      appendDiffHeader(line, extractHeaderPath(line));
      return;
    }

    const combinedMatch = line.match(/^diff --(?:cc|combined) (.+)$/);
    if (combinedMatch) {
      appendDiffHeader(line, decodeGitPath(combinedMatch[1]));
      return;
    }

    if (currentLines.length > 0) {
      currentLines.push(line);
    }
  });

  flushSection();
  if (sections.length === 0 && diffText) {
    return [{ diff: diffText, pathHint: null, submodule: false }];
  }
  return sections;
};

const splitAbsolutePath = (value: string): AbsolutePathParts | null => {
  const windowsPath = /^[a-zA-Z]:/.test(value) || value.startsWith('\\\\');
  const withForwardSlashes = windowsPath ? value.replace(/\\/g, '/') : value;
  const normalized = withForwardSlashes.replace(/\/+$/, '') || '/';
  const driveMatch = normalized.match(/^([a-zA-Z]:)(?:\/|$)/);
  if (driveMatch) {
    return {
      root: driveMatch[1].toLowerCase(),
      segments: normalized.slice(driveMatch[0].length).split('/').filter(Boolean),
    };
  }

  if (!normalized.startsWith('/')) {
    return null;
  }

  return {
    root: '/',
    segments: normalized.slice(1).split('/').filter(Boolean),
  };
};

const normalizeSegments = (segments: string[]): string[] => {
  const normalized: string[] = [];
  segments.forEach((segment) => {
    if (!segment || segment === '.') {
      return;
    }
    if (segment === '..') {
      normalized.pop();
      return;
    }
    normalized.push(segment);
  });
  return normalized;
};

const makePathRelativeToCWD = (
  repoPath: string,
  cwd?: string,
  gitRoot?: string
): string => {
  if (!cwd || !gitRoot) {
    return repoPath;
  }

  const cwdParts = splitAbsolutePath(cwd);
  const rootParts = splitAbsolutePath(gitRoot);
  if (!cwdParts || !rootParts || cwdParts.root !== rootParts.root) {
    return repoPath;
  }

  const targetSegments = normalizeSegments([
    ...rootParts.segments,
    ...repoPath.split('/'),
  ]);
  const cwdSegments = normalizeSegments(cwdParts.segments);
  const caseInsensitive = cwdParts.root !== '/';
  let commonSegments = 0;

  while (
    commonSegments < cwdSegments.length &&
    commonSegments < targetSegments.length &&
    (caseInsensitive
      ? cwdSegments[commonSegments].toLowerCase() ===
        targetSegments[commonSegments].toLowerCase()
      : cwdSegments[commonSegments] === targetSegments[commonSegments])
  ) {
    commonSegments += 1;
  }

  const relativeSegments = [
    ...Array.from({ length: cwdSegments.length - commonSegments }, () => '..'),
    ...targetSegments.slice(commonSegments),
  ];
  return relativeSegments.join('/') || '.';
};

const countChangedLines = (diff: string) => {
  let additions = 0;
  let deletions = 0;
  let inHunk = false;

  diff.split('\n').forEach((line) => {
    if (
      line.startsWith('diff --git ') ||
      line.startsWith('diff --cc ') ||
      line.startsWith('diff --combined ') ||
      line.startsWith('Submodule ')
    ) {
      inHunk = false;
      return;
    }
    if (line.startsWith('@@')) {
      inHunk = true;
      return;
    }
    if (!inHunk) {
      return;
    }
    if (line.startsWith('+')) {
      additions += 1;
    } else if (line.startsWith('-')) {
      deletions += 1;
    }
  });

  return { additions, deletions };
};

const getFileStatus = (diff: string, additions: number, deletions: number) => {
  if (additions > 0 || deletions > 0) {
    return null;
  }
  if (/^(?:GIT binary patch|Binary files )/m.test(diff)) {
    return 'Binary';
  }
  if (/^rename (?:from|to) /m.test(diff)) {
    return 'Renamed';
  }
  if (/^copy (?:from|to) /m.test(diff)) {
    return 'Copied';
  }
  if (/^new file mode /m.test(diff)) {
    return 'Added';
  }
  if (/^deleted file mode /m.test(diff)) {
    return 'Deleted';
  }
  if (/^(?:old|new) mode /m.test(diff)) {
    return 'Mode changed';
  }
  if (/^Submodule /m.test(diff)) {
    return 'Submodule';
  }
  return null;
};

const formatDisplayPath = (path: string): string => {
  if (!/(\\|^\s|\s$| {2,}|[\t\r\n])/.test(path)) {
    return path;
  }

  return path
    .replace(/\\/g, '\\\\')
    .replace(/ /g, '\\x20')
    .replace(/\t/g, '\\t')
    .replace(/\r/g, '\\r')
    .replace(/\n/g, '\\n');
};

const parseGitFileDiffs = (
  diffText: string,
  cwd?: string,
  gitRoot?: string
): GitFileDiff[] => {
  return splitGitDiffSections(diffText).map((section, index) => {
    const { diff } = section;
    const lines = diff.split('\n');
    const targetPath =
      (section.submodule ? section.pathHint : null) ??
      extractPathLine(lines, 'rename to ', false) ??
      extractPathLine(lines, 'copy to ', false) ??
      extractPathLine(lines, '+++ ', true) ??
      extractPathLine(lines, '--- ', true) ??
      section.pathHint ??
      `Changed file ${index + 1}`;
    const { additions, deletions } = countChangedLines(diff);
    const path = makePathRelativeToCWD(targetPath, cwd, gitRoot);

    return {
      additions,
      deletions,
      diff,
      id: `${index}:${targetPath}`,
      path,
      status: getFileStatus(diff, additions, deletions),
    };
  });
};

const GitDiffModal: React.FC<GitDiffModalProps> = ({
  cwdLabel,
  error,
  gitDiff,
  loading,
  open,
  onRefresh,
}) => {
  const [expandedFileIds, setExpandedFileIds] = useState<Set<string>>(
    () => new Set()
  );
  const diffText = gitDiff?.diff || '';
  const fileDiffs = useMemo(
    () =>
      parseGitFileDiffs(
        diffText,
        gitDiff?.cwd || cwdLabel,
        gitDiff?.git_root
      ),
    [cwdLabel, diffText, gitDiff?.cwd, gitDiff?.git_root]
  );
  const allFilesExpanded =
    fileDiffs.length > 0 &&
    fileDiffs.every((file) => expandedFileIds.has(file.id));

  if (!open) {
    return null;
  }

  const toggleFile = (id: string) => {
    setExpandedFileIds((current) => {
      const next = new Set(current);
      if (next.has(id)) {
        next.delete(id);
      } else {
        next.add(id);
      }
      return next;
    });
  };

  const toggleAllFiles = () => {
    setExpandedFileIds(
      allFilesExpanded
        ? new Set()
        : new Set(fileDiffs.map((file) => file.id))
    );
  };

  return (
    <section
      aria-label="Changes"
      className="workspace-side-panel workspace-diff-panel surface-panel"
      data-testid="git-diff-panel"
      role="complementary"
    >
      <div className="workspace-modal-body workspace-side-panel-body">
        <div className="workspace-diff-toolbar" aria-label="Diff actions">
          {gitDiff?.has_diff && !loading ? (
            <>
              <button
                aria-label={allFilesExpanded ? 'Collapse all file diffs' : 'Expand all file diffs'}
                aria-pressed={allFilesExpanded}
                className="workspace-diff-icon-button"
                onClick={toggleAllFiles}
                title={allFilesExpanded ? 'Collapse all file diffs' : 'Expand all file diffs'}
                type="button"
              >
                {allFilesExpanded ? (
                  <FoldVertical aria-hidden="true" className="h-4 w-4" strokeWidth={1.9} />
                ) : (
                  <UnfoldVertical aria-hidden="true" className="h-4 w-4" strokeWidth={1.9} />
                )}
              </button>
              <button
                aria-label="Copy diff"
                className="workspace-diff-icon-button"
                onClick={() => void copyToClipboard(diffText)}
                title="Copy diff"
                type="button"
              >
                <Copy aria-hidden="true" className="h-4 w-4" strokeWidth={1.9} />
              </button>
            </>
          ) : null}
          <button
            aria-label="Refresh diff"
            className="workspace-diff-icon-button"
            disabled={loading}
            onClick={onRefresh}
            title="Refresh diff"
            type="button"
          >
            <RefreshCw
              aria-hidden="true"
              className="workspace-diff-refresh-icon"
              strokeWidth={1.9}
            />
          </button>
        </div>

        {error ? (
          <div className="surface-panel rounded-2xl border-kodelet-orange/20 px-4 py-3 text-sm text-kodelet-dark" role="alert">
            {error}
          </div>
        ) : null}

        {gitDiff?.truncated && !loading && !error ? (
          <div
            className="surface-panel rounded-2xl border-kodelet-orange/20 px-4 py-3 text-sm text-kodelet-dark"
            role="status"
          >
            Showing a partial diff because the runner output exceeded the display limit.
          </div>
        ) : null}

        {loading ? (
          <div className="workspace-modal-placeholder">Loading diff…</div>
        ) : gitDiff?.has_diff ? (
          <div className="workspace-modal-scroll-region" data-testid="git-diff-content">
            <div className="workspace-diff-file-list">
              {fileDiffs.map((file, index) => {
                const expanded = expandedFileIds.has(file.id);
                const displayPath = formatDisplayPath(file.path);
                const statsId = `git-diff-file-stats-${index}`;
                const bodyId = `git-diff-file-body-${index}`;
                const hasStats =
                  file.additions > 0 || file.deletions > 0 || file.status !== null;
                return (
                  <article className="workspace-diff-file" key={file.id}>
                    <button
                      aria-controls={bodyId}
                      aria-describedby={hasStats ? statsId : undefined}
                      aria-expanded={expanded}
                      aria-label={`${expanded ? 'Collapse' : 'Expand'} ${displayPath}`}
                      className="workspace-diff-file-toggle"
                      onClick={() => toggleFile(file.id)}
                      type="button"
                    >
                      <ChevronRight
                        aria-hidden="true"
                        className={`workspace-diff-file-chevron${expanded ? ' is-expanded' : ''}`}
                        strokeWidth={2}
                      />
                      <span className="workspace-diff-file-path" title={file.path}>
                        {displayPath}
                      </span>
                      <span className="workspace-diff-file-stats" id={statsId}>
                        {file.additions > 0 ? (
                          <span className="workspace-diff-stat-added">+{file.additions}</span>
                        ) : null}
                        {file.deletions > 0 ? (
                          <span className="workspace-diff-stat-removed">-{file.deletions}</span>
                        ) : null}
                        {file.status ? (
                          <span className="workspace-diff-stat-status">{file.status}</span>
                        ) : null}
                      </span>
                    </button>
                    {expanded ? (
                      <div className="workspace-diff-file-body" id={bodyId}>
                        <ReferenceDiffBlock lines={parseUnifiedDiff(file.diff)} />
                      </div>
                    ) : null}
                  </article>
                );
              })}
            </div>
          </div>
        ) : (
          <div className="workspace-modal-placeholder">No working tree changes in this repository.</div>
        )}
      </div>
    </section>
  );
};

export default GitDiffModal;
