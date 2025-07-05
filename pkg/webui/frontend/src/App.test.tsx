import { describe, it, expect, vi } from 'vitest';
import { render, screen } from '@testing-library/react';
import { MemoryRouter } from 'react-router-dom';
import App from './App';

// Mock the page components
vi.mock('./pages/ConversationListPage', () => ({
  default: () => <div data-testid="conversation-list-page">Conversation List Page</div>,
}));

vi.mock('./pages/ConversationViewPage', () => ({
  default: () => <div data-testid="conversation-view-page">Conversation View Page</div>,
}));

describe('App', () => {
  it('renders ConversationListPage at root path', () => {
    render(<App />);
    
    expect(screen.getByTestId('conversation-list-page')).toBeInTheDocument();
  });

  it('renders ConversationViewPage at conversation path', () => {
    // We need to wrap App in MemoryRouter with initialEntries to test specific routes
    window.history.pushState({}, 'Test page', '/c/123');
    
    render(<App />);
    
    expect(screen.getByTestId('conversation-view-page')).toBeInTheDocument();
  });

  it('applies correct base styling', () => {
    const { container } = render(<App />);
    
    const wrapper = container.querySelector('.min-h-screen');
    expect(wrapper).toBeInTheDocument();
    expect(wrapper).toHaveClass('bg-base-100');
  });
});

describe('App routing', () => {
  it('navigates to conversation list at /', () => {
    render(
      <MemoryRouter initialEntries={['/']} future={{
        v7_startTransition: true,
        v7_relativeSplatPath: true
      }}>
        <Routes>
          <Route path="/" element={<div data-testid="list">List</div>} />
          <Route path="/c/:id" element={<div data-testid="view">View</div>} />
        </Routes>
      </MemoryRouter>
    );
    
    expect(screen.getByTestId('list')).toBeInTheDocument();
    expect(screen.queryByTestId('view')).not.toBeInTheDocument();
  });

  it('navigates to conversation view at /c/:id', () => {
    render(
      <MemoryRouter initialEntries={['/c/test-id-123']} future={{
        v7_startTransition: true,
        v7_relativeSplatPath: true
      }}>
        <Routes>
          <Route path="/" element={<div data-testid="list">List</div>} />
          <Route path="/c/:id" element={<div data-testid="view">View</div>} />
        </Routes>
      </MemoryRouter>
    );
    
    expect(screen.getByTestId('view')).toBeInTheDocument();
    expect(screen.queryByTestId('list')).not.toBeInTheDocument();
  });
});

// Import Routes and Route for the routing test
import { Routes, Route } from 'react-router-dom';