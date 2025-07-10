import React from 'react';
import { render, RenderOptions } from '@testing-library/react';
import { BrowserRouter } from 'react-router-dom';

// Custom render function that includes providers
export const renderWithRouter = (
  ui: React.ReactElement,
  options?: RenderOptions
) => {
  const AllTheProviders = ({ children }: { children: React.ReactNode }) => {
    return <BrowserRouter future={{
      v7_startTransition: true,
      v7_relativeSplatPath: true
    }}>{children}</BrowserRouter>;
  };

  return render(ui, { wrapper: AllTheProviders, ...options });
};

// Re-export everything from testing library
/* eslint-disable react-refresh/only-export-components */
export * from '@testing-library/react';
export { renderWithRouter as render };