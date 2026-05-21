import { fireEvent, render, screen } from '@testing-library/react';
import { describe, expect, it, vi } from 'vitest';
import ArcadeGamesModal from './ArcadeGamesModal';

describe('ArcadeGamesModal', () => {
  it('renders the game picker and selects games externally', () => {
    const onSelectGame = vi.fn();

    render(
      <ArcadeGamesModal
        selectedGame={null}
        onBackToGames={vi.fn()}
        onClose={vi.fn()}
        onSelectGame={onSelectGame}
      />
    );

    fireEvent.click(screen.getByRole('button', { name: /Pong/i }));

    expect(screen.getByText('Hidden arcade')).toBeInTheDocument();
    expect(screen.getByTestId('arcade-game-picker')).toBeInTheDocument();
    expect(onSelectGame).toHaveBeenCalledWith('pong');
  });

  it('renders a selected game canvas and navigation actions', () => {
    const onBackToGames = vi.fn();
    const onClose = vi.fn();

    render(
      <ArcadeGamesModal
        selectedGame="tetris"
        onBackToGames={onBackToGames}
        onClose={onClose}
        onSelectGame={vi.fn()}
      />
    );

    fireEvent.click(screen.getByRole('button', { name: 'Games' }));
    fireEvent.click(screen.getByRole('button', { name: 'Close' }));

    expect(screen.getByText('Tetris')).toBeInTheDocument();
    expect(screen.getByLabelText('Tetris play area')).toBeInTheDocument();
    expect(onBackToGames).toHaveBeenCalledTimes(1);
    expect(onClose).toHaveBeenCalledTimes(1);
  });
});
