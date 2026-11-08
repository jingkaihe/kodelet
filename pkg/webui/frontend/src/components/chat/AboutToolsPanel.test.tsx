import { act, render, screen, within } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { describe, expect, it } from 'vitest';
import { sampleToolInventory } from '../../stories/toolInventoryFixtures';
import AboutToolsPanel from './AboutToolsPanel';

// Match rows by their visible label: jsdom names highlighted rows with extra spaces.
const queryRow = (label: string) =>
  screen
    .queryAllByRole('treeitem')
    .find((item) => item.querySelector('.about-tools-label')?.textContent === label);

const row = (label: string) => {
  const item = queryRow(label);
  if (!item) throw new Error(`No row labelled ${label}`);
  return item;
};

const filter = () => screen.getByPlaceholderText('Filter tools, plugins, and skills');

describe('AboutToolsPanel', () => {
  it('summarises usable tools, extensions and skills', () => {
    const { container } = render(<AboutToolsPanel inventory={sampleToolInventory} />);
    expect(container.querySelector('.about-tools-total')).toHaveTextContent(
      '24 tools · 8 extensions · 15 skills'
    );
    expect(screen.getByRole('tree')).toBeInTheDocument();
  });

  it('shows names without descriptions until a row is opened', async () => {
    const user = userEvent.setup();
    render(<AboutToolsPanel inventory={sampleToolInventory} />);
    expect(screen.queryByText(/Run a bash command/)).not.toBeInTheDocument();

    await user.click(row('bash'));
    expect(screen.getByText(/Run a bash command/)).toBeVisible();
    await user.click(row('bash'));
    expect(screen.queryByText(/Run a bash command/)).not.toBeInTheDocument();
  });

  it('shows a container path while it is unfolded and hides it when folded', async () => {
    const user = userEvent.setup();
    render(<AboutToolsPanel inventory={sampleToolInventory} />);
    const plugin = row('jingkaihe@skills');

    await user.click(plugin);
    expect(plugin).toHaveAttribute('aria-expanded', 'true');
    expect(within(plugin).getByText('~/.kodelet/plugins/jingkaihe@skills')).toBeVisible();
    expect(queryRow('code-search')).toBeDefined();

    await user.click(plugin);
    expect(plugin).toHaveAttribute('aria-expanded', 'false');
    expect(within(plugin).queryByText('~/.kodelet/plugins/jingkaihe@skills')).toBeNull();
    expect(queryRow('code-search')).toBeUndefined();
  });

  it('filters by name or description and explains description matches', async () => {
    const user = userEvent.setup();
    render(<AboutToolsPanel inventory={sampleToolInventory} />);

    await user.type(filter(), 'search');
    expect(queryRow('web_search')).toBeDefined();
    expect(within(row('grep_tool')).getByText(/file contents/)).toBeInTheDocument();
    expect(within(row('code_search')).queryByText(/Find code/)).toBeNull();
    expect(queryRow('bash')).toBeUndefined();

    await user.clear(filter());
    await user.type(filter(), 'zzz');
    expect(screen.getByText('Nothing matches “zzz”.')).toBeInTheDocument();
  });

  it('does not misplace highlights when lowercasing changes the text length', async () => {
    const user = userEvent.setup();
    render(
      <AboutToolsPanel
        inventory={{
          builtinTools: [{ name: 'İzmir_tool', description: 'Plan a trip.', enabled: true }],
          plugins: [],
          extensions: [],
          skills: [],
        }}
      />
    );
    await user.type(filter(), 'tool');
    const label = row('İzmir_tool').querySelector('.about-tools-label');
    expect(label).toHaveTextContent('İzmir_tool');
    expect(label?.querySelector('mark')).toBeNull();
  });

  it('keeps the selected row when the filter is cleared', async () => {
    const user = userEvent.setup();
    render(<AboutToolsPanel inventory={sampleToolInventory} />);
    await user.type(filter(), 'search');
    await user.click(row('code_search'));
    expect(row('code_search')).toHaveAttribute('aria-selected', 'true');

    await user.clear(filter());
    expect(row('code_search')).toHaveAttribute('aria-selected', 'true');
    expect(row('code-search')).toHaveAttribute('aria-expanded', 'true');
  });

  it('supports tree keyboard navigation', async () => {
    const user = userEvent.setup();
    render(<AboutToolsPanel inventory={sampleToolInventory} />);
    act(() => row('Built-in tools').focus());

    await user.keyboard('{ArrowLeft}');
    expect(row('Built-in tools')).toHaveAttribute('aria-expanded', 'false');
    await user.keyboard('{ArrowDown}');
    expect(row('Plugins')).toHaveFocus();
    await user.keyboard('{ArrowDown}{ArrowRight}');
    expect(row('jingkaihe@kodelet')).toHaveAttribute('aria-expanded', 'true');
    await user.keyboard('{ArrowRight}');
    expect(row('kodelet')).toHaveFocus();
    await user.keyboard('{ArrowLeft}');
    expect(row('jingkaihe@kodelet')).toHaveFocus();
  });
});
