import { fireEvent, render, screen, waitFor } from '@testing-library/react';
import { describe, expect, it, vi } from 'vitest';
import ArcadeGames from './ArcadeGames';

const unlockKeys = ['ArrowUp', 'ArrowUp', 'ArrowDown', 'ArrowDown', 'ArrowLeft', 'ArrowRight', 'ArrowLeft', 'ArrowRight', 'b', 'a'];

const enterUnlockSequence = (target: Window | HTMLElement = window) => {
  unlockKeys.forEach((key) => {
    fireEvent.keyDown(target, { key });
  });
};

const installCanvasMock = () => {
  const context = {
    arc: vi.fn(),
    beginPath: vi.fn(),
    clearRect: vi.fn(),
    createLinearGradient: vi.fn(() => ({ addColorStop: vi.fn() })),
    fill: vi.fn(),
    fillRect: vi.fn(),
    fillText: vi.fn(),
    lineTo: vi.fn(),
    measureText: vi.fn(() => ({ width: 10 })),
    moveTo: vi.fn(),
    restore: vi.fn(),
    save: vi.fn(),
    setLineDash: vi.fn(),
    setTransform: vi.fn(),
    stroke: vi.fn(),
    strokeRect: vi.fn(),
    fillStyle: '',
    font: '',
    globalAlpha: 1,
    lineWidth: 1,
    shadowBlur: 0,
    shadowColor: '',
    strokeStyle: '',
    textAlign: '',
    textBaseline: '',
  };

  vi.spyOn(HTMLCanvasElement.prototype, 'getContext').mockReturnValue(context as unknown as CanvasRenderingContext2D);
};

describe('ArcadeGames', () => {
  it('opens a game picker after the unlock sequence', () => {
    installCanvasMock();
    render(<ArcadeGames />);

    enterUnlockSequence();

    expect(screen.getByTestId('arcade-games-modal')).toBeInTheDocument();
    expect(screen.getByTestId('arcade-game-picker')).toBeInTheDocument();
    expect(screen.getByText('Pong')).toBeInTheDocument();
    expect(screen.getByText('Tetris')).toBeInTheDocument();
    expect(screen.getByText('Flappy Bird')).toBeInTheDocument();
  });

  it('starts a selected game in the canvas', async () => {
    installCanvasMock();
    render(<ArcadeGames />);

    enterUnlockSequence();
    fireEvent.click(screen.getByText('Tetris'));

    expect(screen.queryByTestId('arcade-game-picker')).not.toBeInTheDocument();
    const canvas = screen.getByTestId('arcade-games-canvas');
    expect(canvas).toBeInTheDocument();
    await waitFor(() => {
      expect(canvas).toHaveFocus();
    });
  });

  it('moves focus into the popup and restores it after close', async () => {
    installCanvasMock();
    render(
      <>
        <textarea aria-label="Prompt" />
        <ArcadeGames />
      </>,
    );

    const input = screen.getByLabelText('Prompt');
    input.focus();

    enterUnlockSequence(input);

    await waitFor(() => {
      expect(screen.getByRole('button', { name: /Pong/ })).toHaveFocus();
    });

    fireEvent.keyDown(window, { key: 'Escape' });

    expect(input).toHaveFocus();
  });

  it('keeps Tab focus inside the popup', async () => {
    installCanvasMock();
    render(<ArcadeGames />);

    enterUnlockSequence();

    await screen.findByRole('button', { name: /Pong/ });
    const closeButton = screen.getByText('Close');
    const lastGame = screen.getByRole('button', { name: /Flappy Bird/ });

    await waitFor(() => {
      expect(screen.getByRole('button', { name: /Pong/ })).toHaveFocus();
    });

    closeButton.focus();
    fireEvent.keyDown(closeButton, { key: 'Tab', code: 'Tab', shiftKey: true });

    expect(lastGame).toHaveFocus();

    fireEvent.keyDown(lastGame, { key: 'Tab', code: 'Tab' });

    expect(closeButton).toHaveFocus();
  });

  it('lets focused popup buttons handle Space normally while a game is running', () => {
    installCanvasMock();
    render(<ArcadeGames />);

    enterUnlockSequence();
    fireEvent.click(screen.getByText('Flappy Bird'));

    const closeButton = screen.getByText('Close');
    closeButton.focus();
    fireEvent.keyDown(closeButton, { key: ' ' });
    fireEvent.click(closeButton);

    expect(screen.queryByTestId('arcade-games-modal')).not.toBeInTheDocument();
  });

  it('closes the popup with Escape', () => {
    installCanvasMock();
    render(<ArcadeGames />);

    enterUnlockSequence();
    fireEvent.keyDown(window, { key: 'Escape' });

    expect(screen.queryByTestId('arcade-games-modal')).not.toBeInTheDocument();
  });

  it('opens from the focused composer without typing the final letters', () => {
    installCanvasMock();
    render(
      <>
        <textarea aria-label="Prompt" />
        <ArcadeGames />
      </>,
    );

    const input = screen.getByLabelText('Prompt');
    enterUnlockSequence(input);

    expect(screen.getByTestId('arcade-game-picker')).toBeInTheDocument();
  });
});
