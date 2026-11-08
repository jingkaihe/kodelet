import { describe, expect, it } from 'vitest';
import { sampleToolInventory, toolInventoryWithIssues } from '../../stories/toolInventoryFixtures';
import {
  buildInventoryTree,
  defaultExpandedIds,
  filterInventoryTree,
  flattenInventoryTree,
  summarizeInventory,
} from './toolInventory';

describe('tool inventory tree', () => {
  it('groups built-in tools, plugins, and standalone items', () => {
    const [builtin, plugins, standalone] = buildInventoryTree(sampleToolInventory);

    expect(builtin.meta).toBe('8 tools · 4 off');
    expect(builtin.children[builtin.children.length - 1]).toMatchObject({
      label: 'grep_tool',
      status: 'off',
    });
    expect(plugins.children.map((node) => node.label)).toEqual([
      'jingkaihe@kodelet',
      'jingkaihe@skills',
      'jingkaihe@thirdspace',
      'typesafe-ai@skills',
    ]);
    const skills = plugins.children[1];
    expect(skills.meta).toBe('7 extensions · 11 skills');
    expect(skills.children[0]).toMatchObject({
      kind: 'extension',
      label: 'caffeinate',
      meta: '4 hooks',
    });
    expect(skills.children[0].scope).toBeUndefined();
    expect(standalone.children.map((node) => [node.kind, node.label, node.scope])).toEqual([
      ['extension', 'subagent', 'path'],
      ['skill', 'self-test', 'repo'],
    ]);
  });

  it('shows only sections and their direct children by default', () => {
    const rows = flattenInventoryTree(
      buildInventoryTree(sampleToolInventory),
      defaultExpandedIds()
    );

    expect(rows.filter((row) => row.level === 1)).toHaveLength(3);
    expect(rows.some((row) => row.level > 2)).toBe(false);
    expect(rows.find((row) => row.node.label === 'jingkaihe@skills')).toMatchObject({
      expandable: true,
      expanded: false,
      level: 2,
    });
  });

  it('keeps matching branches and expands their ancestors', () => {
    const tree = buildInventoryTree(sampleToolInventory);
    const { nodes, expanded } = filterInventoryTree(tree, 'Web_Search');
    const rows = flattenInventoryTree(nodes, expanded);

    expect(rows.map((row) => row.node.label)).toEqual([
      'Plugins',
      'jingkaihe@skills',
      'web-search',
      'web_search',
    ]);
  });

  it('expands a matching container when its children also match', () => {
    const { nodes, expanded } = filterInventoryTree(
      buildInventoryTree(sampleToolInventory),
      'search'
    );
    const labels = flattenInventoryTree(nodes, expanded).map((row) => row.node.label);

    expect(labels).toEqual(expect.arrayContaining(['web-search', 'web_search', 'code_search']));
    expect(labels).not.toContain('bash');
  });

  it('keeps the whole subtree of a matching container collapsed', () => {
    const { nodes, expanded } = filterInventoryTree(
      buildInventoryTree(sampleToolInventory),
      'subagent'
    );
    const [standalone] = nodes;

    expect(standalone.children).toHaveLength(1);
    expect(standalone.children[0].children).toHaveLength(10);
    expect(expanded.has(standalone.children[0].id)).toBe(false);
  });

  it('counts only usable tools and skills', () => {
    expect(summarizeInventory(sampleToolInventory)).toEqual({
      tools: 24,
      extensions: 8,
      skills: 15,
      failedExtensions: 0,
    });
    expect(summarizeInventory(toolInventoryWithIssues)).toMatchObject({
      extensions: 9,
      skills: 14,
      failedExtensions: 1,
    });
  });

  it('keeps repo and global copies of a plugin apart', () => {
    const plugin = sampleToolInventory.plugins[0];
    const [, plugins] = buildInventoryTree({
      ...sampleToolInventory,
      plugins: [plugin, { ...plugin, scope: 'repo', path: '.kodelet/plugins/jingkaihe@kodelet' }],
    });
    const ids = plugins.children.map((node) => node.id);

    expect(new Set(ids).size).toBe(2);
    expect(plugins.children.map((node) => node.scope)).toEqual(['global', 'repo']);
  });

  it('describes failed extensions with an error detail row', () => {
    const standalone = buildInventoryTree(toolInventoryWithIssues)[2];
    const failed = standalone.children.find((node) => node.label === 'drawing-board');

    expect(failed).toMatchObject({ status: 'failed', meta: 'Not loaded' });
    expect(failed?.children[0]).toMatchObject({ kind: 'detail', label: 'Error' });
  });
});
