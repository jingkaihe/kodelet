import React from 'react';

interface ErrorAlertProps {
  message: string;
  onRetry?: () => void;
}

const ErrorAlert: React.FC<ErrorAlertProps> = ({ message, onRetry }) => {
  return (
    <div className="alert alert-error mb-6" role="alert">
      <svg 
        xmlns="http://www.w3.org/2000/svg" 
        className="stroke-current shrink-0 h-6 w-6" 
        fill="none" 
        viewBox="0 0 24 24"
        aria-hidden="true"
      >
        <path 
          strokeLinecap="round" 
          strokeLinejoin="round" 
          strokeWidth="2" 
          d="M10 14l2-2m0 0l2-2m-2 2l-2-2m2 2l2 2m7-2a9 9 0 11-18 0 9 9 0 0118 0z" 
        />
      </svg>
      <div className="flex-1">
        <span>{message}</span>
      </div>
      {onRetry && (
        <button 
          className="btn btn-sm btn-outline" 
          onClick={onRetry}
          aria-label="Retry operation"
        >
          Retry
        </button>
      )}
    </div>
  );
};

export default ErrorAlert;