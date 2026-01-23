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
    <div className="text-center py-12 animate-fade-in">
      <div className={`loading loading-spinner ${sizeClass} text-kodelet-orange`} role="status"></div>
      <p className="mt-6 font-body text-kodelet-mid-gray italic">{message}</p>
    </div>
  );
};

export default LoadingSpinner;