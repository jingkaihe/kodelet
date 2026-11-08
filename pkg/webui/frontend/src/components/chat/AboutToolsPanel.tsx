import {
  BookOpen,
  ChevronRight,
  ChevronsDownUp,
  ChevronsUpDown,
  CircleAlert,
  Package,
  Puzzle,
  Search,
  Wrench,
} from 'lucide-react';
import React from 'react';
import {
  buildInventoryTree,
  defaultExpandedIds,
  expandableIds,
  filterInventoryTree,
  flattenInventoryTree,
  type InventoryNode,
  type InventoryRow,
  matchesQuery,
  plural,
  scopeLabel,
  summarizeInventory,
  type ToolInventory,
} from './toolInventory';

interface AboutToolsPanelProps {
  inventory: ToolInventory;
}

const kindIcons = {
  plugin: Package,
  extension: Puzzle,
  skill: BookOpen,
  tool: Wrench,
} as const;

const Highlight: React.FC<{ text: string; query: string }> = ({ text, query }) => {
  const index = query ? text.toLowerCase().indexOf(query) : -1;
  if (index < 0) return <>{text}</>;
  return (
    <>
      {text.slice(0, index)}
      <mark>{text.slice(index, index + query.length)}</mark>
      {text.slice(index + query.length)}
    </>
  );
};

const rowTitle = (node: InventoryNode) =>
  [node.description, node.path].filter(Boolean).join('\n') || undefined;

/** Sections are headings and detail rows are already expanded text; neither opens details. */
const hasDetails = (node: InventoryNode) =>
  node.kind !== 'section' && node.kind !== 'detail' && Boolean(node.description || node.path);

const AboutToolsPanel: React.FC<AboutToolsPanelProps> = ({ inventory }) => {
  const tree = React.useMemo(() => buildInventoryTree(inventory), [inventory]);
  const summary = React.useMemo(() => summarizeInventory(inventory), [inventory]);
  const allExpandable = React.useMemo(() => expandableIds(tree), [tree]);
  const [query, setQuery] = React.useState('');
  const [expanded, setExpanded] = React.useState<Set<string>>(defaultExpandedIds);
  const [filterExpanded, setFilterExpanded] = React.useState<Set<string>>(() => new Set());
  const [activeId, setActiveId] = React.useState<string>();
  // Details open only on click, Enter, or Space, and close when the row is clicked or folded again.
  const [detailId, setDetailId] = React.useState<string>();
  const treeRef = React.useRef<HTMLDivElement>(null);
  const focusPending = React.useRef(false);

  const normalizedQuery = query.trim().toLowerCase();
  const filtering = normalizedQuery !== '';
  const visibleTree = React.useMemo(
    () => (filtering ? filterInventoryTree(tree, normalizedQuery).nodes : tree),
    [filtering, normalizedQuery, tree]
  );
  const currentExpanded = filtering ? filterExpanded : expanded;
  const setCurrentExpanded = filtering ? setFilterExpanded : setExpanded;
  const rows = React.useMemo(
    () => flattenInventoryTree(visibleTree, currentExpanded),
    [visibleTree, currentExpanded]
  );
  const focusId = rows.some((row) => row.node.id === activeId) ? activeId : rows[0]?.node.id;
  const allExpanded = allExpandable.every((id) => currentExpanded.has(id));

  React.useEffect(() => {
    if (!focusPending.current || !focusId) return;
    focusPending.current = false;
    treeRef.current?.querySelector<HTMLElement>(`[data-node-id="${CSS.escape(focusId)}"]`)?.focus();
  }, [focusId]);

  const toggle = (id: string, open = !currentExpanded.has(id)) => {
    setCurrentExpanded((current) => {
      const next = new Set(current);
      if (open) next.add(id);
      else next.delete(id);
      return next;
    });
    if (!open) setDetailId((current) => (current === id ? undefined : current));
    return open;
  };

  const activate = (row: InventoryRow) => {
    const { node } = row;
    setActiveId(node.id);
    if (row.expandable) {
      if (toggle(node.id) && hasDetails(node)) setDetailId(node.id);
      return;
    }
    if (hasDetails(node)) setDetailId((current) => (current === node.id ? undefined : node.id));
  };

  const moveTo = (row: InventoryRow | undefined) => {
    if (!row) return;
    focusPending.current = true;
    setActiveId(row.node.id);
  };

  const onTreeKeyDown = (event: React.KeyboardEvent<HTMLElement>) => {
    const index = rows.findIndex((row) => row.node.id === focusId);
    const row = rows[index];
    if (!row) return;
    switch (event.key) {
      case 'ArrowDown':
        moveTo(rows[index + 1]);
        break;
      case 'ArrowUp':
        moveTo(rows[index - 1]);
        break;
      case 'Home':
        moveTo(rows[0]);
        break;
      case 'End':
        moveTo(rows[rows.length - 1]);
        break;
      case 'ArrowRight':
        if (row.expandable && !row.expanded) toggle(row.node.id, true);
        else if (row.expanded) moveTo(rows[index + 1]);
        break;
      case 'ArrowLeft':
        if (row.expanded) toggle(row.node.id, false);
        else moveTo(rows.find((candidate) => candidate.node.id === row.parentId));
        break;
      case 'Enter':
      case ' ':
        activate(row);
        break;
      default:
        return;
    }
    event.preventDefault();
  };

  const stats = [
    { label: 'tool', value: summary.tools },
    { label: 'extension', value: summary.extensions },
    { label: 'skill', value: summary.skills },
  ];

  return (
    <div className="about-tools-panel">
      <section aria-label="Summary">
        <p className="about-tools-total">
          {stats.map(({ label, value }, index) => (
            <React.Fragment key={label}>
              {index > 0 ? <span aria-hidden="true"> · </span> : null}
              {value} <span>{plural(value, label).replace(/^\d+ /, '')}</span>
            </React.Fragment>
          ))}
        </p>
        {summary.failedExtensions > 0 ? (
          <output className="about-tools-alert">
            <CircleAlert aria-hidden="true" className="h-3.5 w-3.5" strokeWidth={2} />
            {plural(summary.failedExtensions, 'extension')} failed to load. Their tools are
            unavailable.
          </output>
        ) : null}
      </section>

      <div className="about-tools-toolbar">
        <label className="about-tools-filter">
          <Search aria-hidden="true" className="h-3.5 w-3.5" strokeWidth={1.8} />
          <span className="sr-only">Filter tools, plugins, and skills</span>
          <input
            onChange={(event) => {
              const value = event.target.value;
              setQuery(value);
              setFilterExpanded(filterInventoryTree(tree, value).expanded);
            }}
            placeholder="Filter tools, plugins, and skills"
            type="search"
            value={query}
          />
        </label>
        <button
          aria-label={allExpanded ? 'Collapse all' : 'Expand all'}
          className="about-tools-expand"
          onClick={() => setCurrentExpanded(allExpanded ? new Set() : new Set(allExpandable))}
          title={allExpanded ? 'Collapse all' : 'Expand all'}
          type="button"
        >
          {allExpanded ? (
            <ChevronsDownUp aria-hidden="true" className="h-4 w-4" strokeWidth={1.8} />
          ) : (
            <ChevronsUpDown aria-hidden="true" className="h-4 w-4" strokeWidth={1.8} />
          )}
        </button>
      </div>

      {rows.length === 0 ? (
        <p className="about-kodelet-description about-tools-empty">
          Nothing matches “{query.trim()}”.
        </p>
      ) : (
        <div
          aria-label="Configured tools, plugins, and skills"
          className="about-tools-tree"
          ref={treeRef}
          role="tree"
        >
          {rows.map((row) => {
            const { node } = row;
            const Icon =
              node.kind in kindIcons ? kindIcons[node.kind as keyof typeof kindIcons] : null;
            const active = node.id === activeId;
            const showDetails =
              node.id === detailId && hasDetails(node) && (!row.expandable || row.expanded);
            // Descriptions stay out of the way unless they explain a filter match.
            const descriptionMatch =
              filtering && Boolean(node.description?.toLowerCase().includes(normalizedQuery));
            const leaf = node.kind === 'tool' || node.kind === 'skill' || node.kind === 'detail';
            const inlineDescription =
              node.kind === 'detail' || (leaf && descriptionMatch && !showDetails);
            return (
              <div
                aria-expanded={row.expandable ? row.expanded : undefined}
                aria-level={row.level}
                aria-posinset={row.posInSet}
                aria-selected={active}
                aria-setsize={row.setSize}
                className={[
                  'about-tools-row',
                  `is-${node.kind}`,
                  node.status ? `is-${node.status}` : '',
                  filtering && matchesQuery(node, normalizedQuery) ? 'is-match' : '',
                ]
                  .filter(Boolean)
                  .join(' ')}
                data-node-id={node.id}
                key={node.id}
                onClick={(event) => {
                  event.stopPropagation();
                  activate(row);
                }}
                onKeyDown={onTreeKeyDown}
                onFocus={(event) => {
                  if (event.target === event.currentTarget) setActiveId(node.id);
                }}
                role="treeitem"
                style={{ '--tree-level': row.level } as React.CSSProperties}
                tabIndex={node.id === focusId ? 0 : -1}
                title={showDetails ? undefined : rowTitle(node)}
              >
                <div className="about-tools-line">
                  <span aria-hidden="true" className="about-tools-chevron">
                    {row.expandable ? (
                      <ChevronRight className="h-3.5 w-3.5" strokeWidth={2} />
                    ) : null}
                  </span>
                  {Icon ? (
                    <Icon aria-hidden="true" className="about-tools-icon" strokeWidth={1.8} />
                  ) : null}
                  <span className="about-tools-label">
                    <Highlight query={normalizedQuery} text={node.label} />
                  </span>
                  {node.version ? (
                    <span className="about-tools-version">v{node.version}</span>
                  ) : null}
                  {node.scope ? (
                    <span className="about-tools-badge">{scopeLabel(node.scope)}</span>
                  ) : null}
                  {node.status === 'failed' && node.kind !== 'detail' ? (
                    <span className="about-tools-badge is-failed">Failed</span>
                  ) : null}
                  {node.status === 'off' ? <span className="about-tools-badge">Off</span> : null}
                  {inlineDescription && node.description ? (
                    <span className="about-tools-description">
                      <Highlight query={normalizedQuery} text={node.description} />
                    </span>
                  ) : (
                    <span className="about-tools-meta">{leaf ? null : node.meta}</span>
                  )}
                </div>
                {showDetails ? (
                  <div className="about-tools-details">
                    {node.description ? <p>{node.description}</p> : null}
                    {node.path ? <code>{node.path}</code> : null}
                  </div>
                ) : null}
              </div>
            );
          })}
        </div>
      )}
    </div>
  );
};

export default AboutToolsPanel;
