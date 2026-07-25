import React from 'react';
import { render, RenderOptions } from '@testing-library/react';
import { BrowserRouter } from 'react-router';

// Custom render function that includes providers
export const renderWithRouter = (
  ui: React.ReactElement,
  options?: RenderOptions
) => {
  const AllTheProviders = ({ children }: { children: React.ReactNode }) => {
    return <BrowserRouter>{children}</BrowserRouter>;
  };

  return render(ui, { wrapper: AllTheProviders, ...options });
};

// Re-export everything from testing library
/* eslint-disable react-refresh/only-export-components */
export * from '@testing-library/react';
export { renderWithRouter as render };