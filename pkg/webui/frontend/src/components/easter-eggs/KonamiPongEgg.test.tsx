import { fireEvent, render, screen } from '@testing-library/react';
import { describe, expect, it, vi } from 'vitest';
import KonamiPongEgg from './KonamiPongEgg';

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

describe('KonamiPongEgg', () => {
  it('opens the pong canvas popup after the Konami sequence', () => {
    installCanvasMock();
    render(<KonamiPongEgg />);

    enterKonamiSequence();

    expect(screen.getByTestId('konami-egg-modal')).toBeInTheDocument();
    expect(screen.getByTestId('konami-egg-canvas')).toBeInTheDocument();
    expect(screen.queryByText('Konami channel unlocked')).not.toBeInTheDocument();
  });

  it('closes the popup with Escape', () => {
    installCanvasMock();
    render(<KonamiPongEgg />);

    enterKonamiSequence();
    fireEvent.keyDown(window, { key: 'Escape' });

    expect(screen.queryByTestId('konami-egg-modal')).not.toBeInTheDocument();
  });

  it('opens from the focused composer without typing the final letters', () => {
    installCanvasMock();
    render(
      <>
        <textarea aria-label="Prompt" />
        <KonamiPongEgg />
      </>,
    );

    const input = screen.getByLabelText('Prompt');
    enterKonamiSequence(input);

    expect(screen.getByTestId('konami-egg-modal')).toBeInTheDocument();
  });
});
