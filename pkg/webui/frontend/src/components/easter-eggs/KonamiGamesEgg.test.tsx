import { fireEvent, render, screen } from '@testing-library/react';
import { describe, expect, it, vi } from 'vitest';
import KonamiGamesEgg from './KonamiGamesEgg';

const konamiKeys = ['ArrowUp', 'ArrowUp', 'ArrowDown', 'ArrowDown', 'ArrowLeft', 'ArrowRight', 'ArrowLeft', 'ArrowRight', 'b', 'a'];

const enterKonamiSequence = (target: Window | HTMLElement = window) => {
  konamiKeys.forEach((key) => {
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

describe('KonamiGamesEgg', () => {
  it('opens a game picker after the Konami sequence', () => {
    installCanvasMock();
    render(<KonamiGamesEgg />);

    enterKonamiSequence();

    expect(screen.getByTestId('konami-egg-modal')).toBeInTheDocument();
    expect(screen.getByTestId('konami-game-picker')).toBeInTheDocument();
    expect(screen.getByText('Pong')).toBeInTheDocument();
    expect(screen.getByText('Tetris')).toBeInTheDocument();
    expect(screen.getByText('Flappy Bird')).toBeInTheDocument();
  });

  it('starts a selected game in the canvas', () => {
    installCanvasMock();
    render(<KonamiGamesEgg />);

    enterKonamiSequence();
    fireEvent.click(screen.getByText('Tetris'));

    expect(screen.queryByTestId('konami-game-picker')).not.toBeInTheDocument();
    expect(screen.getByTestId('konami-egg-canvas')).toBeInTheDocument();
  });

  it('closes the popup with Escape', () => {
    installCanvasMock();
    render(<KonamiGamesEgg />);

    enterKonamiSequence();
    fireEvent.keyDown(window, { key: 'Escape' });

    expect(screen.queryByTestId('konami-egg-modal')).not.toBeInTheDocument();
  });

  it('opens from the focused composer without typing the final letters', () => {
    installCanvasMock();
    render(
      <>
        <textarea aria-label="Prompt" />
        <KonamiGamesEgg />
      </>,
    );

    const input = screen.getByLabelText('Prompt');
    enterKonamiSequence(input);

    expect(screen.getByTestId('konami-game-picker')).toBeInTheDocument();
  });
});
