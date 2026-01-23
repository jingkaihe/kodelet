import React from 'react';

interface ErrorAlertProps {
  message: string;
  onRetry?: () => void;
}

const ErrorAlert: React.FC<ErrorAlertProps> = ({ message, onRetry }) => {
  return (
    <div className="bg-kodelet-light border-l-4 border-kodelet-orange p-6 mb-6 rounded-lg shadow-md animate-slide-up" role="alert">
      <div className="flex items-start gap-4">
        <svg 
          xmlns="http://www.w3.org/2000/svg" 
          className="shrink-0 h-6 w-6 text-kodelet-orange mt-0.5" 
          fill="none" 
          viewBox="0 0 24 24"
          strokeWidth="2"
          stroke="currentColor"
          aria-hidden="true"
        >
          <path 
            strokeLinecap="round" 
            strokeLinejoin="round" 
            d="M10 14l2-2m0 0l2-2m-2 2l-2-2m2 2l2 2m7-2a9 9 0 11-18 0 9 9 0 0118 0z" 
          />
        </svg>
        <div className="flex-1">
          <p className="font-body text-kodelet-dark">{message}</p>
        </div>
        {onRetry && (
          <button 
            className="btn btn-sm bg-kodelet-orange text-white hover:bg-kodelet-dark border-none font-heading" 
            onClick={onRetry}
            aria-label="Retry operation"
          >
            Retry
          </button>
        )}
      </div>
    </div>
  );
};

export default ErrorAlert;