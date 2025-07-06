import React from 'react';

interface LoadingSpinnerProps {
  message?: string;
  size?: 'sm' | 'md' | 'lg';
}

const LoadingSpinner: React.FC<LoadingSpinnerProps> = ({ 
  message = 'Loading...', 
  size = 'lg' 
}) => {
  const sizeClass = {
    sm: 'loading-sm',
    md: 'loading-md',
    lg: 'loading-lg'
  }[size];

  return (
    <div className="text-center py-8">
      <div className={`loading loading-spinner ${sizeClass}`} role="status"></div>
      <p className="mt-4 text-base-content/70">{message}</p>
    </div>
  );
};

export default LoadingSpinner;