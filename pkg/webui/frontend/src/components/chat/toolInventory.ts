// Tree model for the About dialog's tools tab. The runner resolves the inventory per
// workspace and profile, because repo-scoped plugins, extensions and skills depend on its
// working directory; pkg/tui/about_tools.go renders the same tree in the terminal.
import type {
  InventoryExtension,
  InventoryScope,
  InventorySkill,
  InventoryTool,
  ToolInventory,
} from '../../types';

export type {
  InventoryExtension,
  InventoryPlugin,
  InventoryScope,
  InventorySkill,
  InventoryTool,
  ToolInventory,
} from '../../types';

export type InventoryNodeKind = 'section' | 'plugin' | 'extension' | 'tool' | 'skill' | 'detail';

export interface InventoryNode {
  id: string;
  kind: InventoryNodeKind;
  label: string;
  description?: string;
  /** Right-aligned summary for container rows, such as "3 tools". */
  meta?: string;
  /** Shown for top-level items only; children inherit their plugin's scope. */
  scope?: InventoryScope;
  path?: string;
  version?: string;
  status?: 'failed' | 'off';
  children: InventoryNode[];
}

export interface InventoryRow {
  node: InventoryNode;
  level: number;
  parentId?: string;
  posInSet: number;
  setSize: number;
  expandable: boolean;
  expanded: boolean;
}

export interface InventorySummary {
  tools: number;
  extensions: number;
  skills: number;
  failedExtensions: number;
}

export const SECTION_IDS = {
  builtin: 'section:builtin',
  plugins: 'section:plugins',
  standalone: 'section:standalone',
} as const;

export const plural = (count: number, singular: string, pluralForm = `${singular}s`) =>
  `${count} ${count === 1 ? singular : pluralForm}`;

const byName = <T extends { name: string }>(items: T[]) =>
  [...items].sort((a, b) => a.name.localeCompare(b.name));

const toolNode = (parentId: string, tool: InventoryTool, path?: string): InventoryNode => ({
  id: `${parentId}/tool:${tool.name}`,
  kind: 'tool',
  label: tool.name,
  description: tool.description,
  path,
  status: tool.enabled === false ? 'off' : undefined,
  children: [],
});

const skillNode = (parentId: string, skill: InventorySkill, showScope: boolean): InventoryNode => ({
  id: `${parentId}/skill:${skill.name}`,
  kind: 'skill',
  label: skill.name,
  description: skill.description,
  scope: showScope ? skill.scope : undefined,
  path: skill.path,
  status: skill.enabled === false ? 'off' : undefined,
  children: [],
});

const extensionMeta = (extension: InventoryExtension) => {
  if (extension.status === 'failed') return 'Not loaded';
  const parts = [
    extension.tools.length > 0 ? plural(extension.tools.length, 'tool') : '',
    extension.commands?.length ? plural(extension.commands.length, 'command') : '',
    extension.hooks?.length ? plural(extension.hooks.length, 'hook') : '',
  ].filter(Boolean);
  return parts.length > 0 ? parts.join(' · ') : 'No tools';
};

const extensionNode = (
  parentId: string,
  extension: InventoryExtension,
  showScope: boolean
): InventoryNode => {
  const id = `${parentId}/extension:${extension.name}`;
  const details: Array<[string, string, string[] | string | undefined]> = [
    ['error', 'Error', extension.error],
    ['commands', 'Commands', extension.commands],
    ['shortcuts', 'Shortcuts', extension.shortcuts],
    ['hooks', 'Hooks', extension.hooks],
  ];
  return {
    id,
    kind: 'extension',
    label: extension.name,
    meta: extensionMeta(extension),
    scope: showScope ? extension.scope : undefined,
    path: extension.path,
    version: extension.version,
    status: extension.status === 'failed' ? 'failed' : undefined,
    children: [
      ...details.flatMap(([key, label, value]) =>
        value && value.length > 0
          ? [
              {
                id: `${id}/detail:${key}`,
                kind: 'detail' as const,
                label,
                description: Array.isArray(value) ? value.join(', ') : value,
                status: key === 'error' ? ('failed' as const) : undefined,
                children: [],
              },
            ]
          : []
      ),
      ...byName(extension.tools).map((tool) => toolNode(id, tool, extension.path)),
    ],
  };
};

const emptyNode = (parentId: string, label: string, description: string): InventoryNode => ({
  id: `${parentId}/detail:empty`,
  kind: 'detail',
  label,
  description,
  children: [],
});

const countSummary = (extensions: number, skills: number) =>
  [extensions > 0 ? plural(extensions, 'extension') : '', skills > 0 ? plural(skills, 'skill') : '']
    .filter(Boolean)
    .join(' · ') || 'Empty';

export const buildInventoryTree = (inventory: ToolInventory): InventoryNode[] => {
  const builtinEnabled = inventory.builtinTools.filter((tool) => tool.enabled !== false);
  const builtinOff = inventory.builtinTools.filter((tool) => tool.enabled === false);
  const plugins = byName(inventory.plugins).map((plugin): InventoryNode => {
    const id = `plugin:${plugin.name}`;
    return {
      id,
      kind: 'plugin',
      label: plugin.name,
      meta: countSummary(plugin.extensions.length, plugin.skills.length),
      scope: plugin.scope,
      path: plugin.path,
      children: [
        ...byName(plugin.extensions).map((extension) => extensionNode(id, extension, false)),
        ...byName(plugin.skills).map((skill) => skillNode(id, skill, false)),
      ],
    };
  });
  const standalone = [
    ...byName(inventory.extensions).map((extension) =>
      extensionNode(SECTION_IDS.standalone, extension, true)
    ),
    ...byName(inventory.skills).map((skill) => skillNode(SECTION_IDS.standalone, skill, true)),
  ];

  return [
    {
      id: SECTION_IDS.builtin,
      kind: 'section',
      label: 'Built-in tools',
      description:
        'Tools compiled into Kodelet. Off tools are excluded by the active profile’s allowed tools.',
      meta: `${plural(builtinEnabled.length, 'tool')}${builtinOff.length > 0 ? ` · ${builtinOff.length} off` : ''}`,
      children: [
        ...byName(builtinEnabled).map((tool) => toolNode(SECTION_IDS.builtin, tool)),
        ...byName(builtinOff).map((tool) => toolNode(SECTION_IDS.builtin, tool)),
      ],
    },
    {
      id: SECTION_IDS.plugins,
      kind: 'section',
      label: 'Plugins',
      description: 'Installed plugin packages and the extensions and skills they provide.',
      meta: plugins.length > 0 ? plural(plugins.length, 'plugin') : 'None',
      children:
        plugins.length > 0
          ? plugins
          : [emptyNode(SECTION_IDS.plugins, 'No plugins', 'kodelet plugin add owner/repo')],
    },
    {
      id: SECTION_IDS.standalone,
      kind: 'section',
      label: 'Standalone',
      description: 'Extensions and skills installed outside a plugin.',
      meta:
        standalone.length > 0
          ? countSummary(inventory.extensions.length, inventory.skills.length)
          : 'None',
      children:
        standalone.length > 0
          ? standalone
          : [
              emptyNode(
                SECTION_IDS.standalone,
                'Nothing here',
                'Add extensions or skills under .kodelet/ or ~/.kodelet/'
              ),
            ],
    },
  ];
};

export const summarizeInventory = (inventory: ToolInventory): InventorySummary => {
  const extensions = [
    ...inventory.extensions,
    ...inventory.plugins.flatMap((plugin) => plugin.extensions),
  ];
  const skills = [...inventory.skills, ...inventory.plugins.flatMap((plugin) => plugin.skills)];
  const extensionTools = extensions
    .filter((extension) => extension.status === 'ready')
    .flatMap((extension) => extension.tools);
  return {
    tools: [...inventory.builtinTools, ...extensionTools].filter((tool) => tool.enabled !== false)
      .length,
    extensions: extensions.length,
    skills: skills.filter((skill) => skill.enabled !== false).length,
    failedExtensions: extensions.filter((extension) => extension.status === 'failed').length,
  };
};

export const defaultExpandedIds = (): Set<string> => new Set(Object.values(SECTION_IDS));

export const expandableIds = (nodes: InventoryNode[]): string[] =>
  nodes.flatMap((node) =>
    node.children.length > 0 ? [node.id, ...expandableIds(node.children)] : []
  );

export const matchesQuery = (node: InventoryNode, query: string) =>
  query !== '' &&
  node.kind !== 'section' &&
  [node.label, node.description].some((value) => value?.toLowerCase().includes(query));

/**
 * Keeps nodes that match the query with their whole subtree, and ancestors of matches with
 * only their matching branches. The returned set expands every ancestor of a match, including
 * matching containers, so nested matches stay visible.
 */
export const filterInventoryTree = (
  nodes: InventoryNode[],
  rawQuery: string
): { nodes: InventoryNode[]; expanded: Set<string> } => {
  const query = rawQuery.trim().toLowerCase();
  const expanded = new Set<string>();
  if (!query) return { nodes, expanded: defaultExpandedIds() };
  const visit = (items: InventoryNode[]): InventoryNode[] =>
    items.flatMap((node) => {
      const children = visit(node.children);
      const matched = matchesQuery(node, query);
      if (!matched && children.length === 0) return [];
      if (children.length > 0) expanded.add(node.id);
      return [{ ...node, children: matched ? node.children : children }];
    });
  return { nodes: visit(nodes), expanded };
};

export const flattenInventoryTree = (
  nodes: InventoryNode[],
  expanded: ReadonlySet<string>
): InventoryRow[] => {
  const rows: InventoryRow[] = [];
  const visit = (items: InventoryNode[], level: number, parentId?: string) => {
    items.forEach((node, index) => {
      const expandable = node.children.length > 0;
      const isExpanded = expandable && expanded.has(node.id);
      rows.push({
        node,
        level,
        parentId,
        posInSet: index + 1,
        setSize: items.length,
        expandable,
        expanded: isExpanded,
      });
      if (isExpanded) visit(node.children, level + 1, node.id);
    });
  };
  visit(nodes, 1);
  return rows;
};

export const scopeLabel = (scope: InventoryScope) =>
  ({ repo: 'repo', global: 'global', path: 'PATH', session: 'session' })[scope];

export const kindLabel = (node: InventoryNode) =>
  ({
    section: 'Section',
    plugin: 'Plugin',
    extension: 'Extension',
    tool: 'Tool',
    skill: 'Skill',
    detail: node.label,
  })[node.kind];
