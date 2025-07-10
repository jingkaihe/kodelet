import React from 'react';

interface EmptyStateProps {
  icon: string;
  title: string;
  description: string;
  action?: React.ReactNode;
}

const EmptyState: React.FC<EmptyStateProps> = ({ 
  icon, 
  title, 
  description, 
  action 
}) => {
  return (
    <div className="text-center py-12">
      <div className="text-6xl mb-4" role="img" aria-label={title}>
        {icon}
      </div>
      <h2 className="mt-4 text-xl font-semibold text-base-content/70">{title}</h2>
      <p className="mt-2 text-base-content/50">{description}</p>
      {action && (
        <div className="mt-6">
          {action}
        </div>
      )}
    </div>
  );
};

export default EmptyState;