import React from 'react';
import { ToolResult, TodoMetadata, TodoItem } from '../../types';
import { StatusBadge } from './shared';

interface TodoRendererProps {
  toolResult: ToolResult;
}

const TodoRenderer: React.FC<TodoRendererProps> = ({ toolResult }) => {
  const meta = toolResult.metadata as TodoMetadata;
  if (!meta) return null;

  const action = meta.action || 'updated';
  const todos = meta.todos || meta.todoList || [];

  const getStatusVariant = (status: string): 'success' | 'info' | 'neutral' | 'warning' => {
    const variants = {
      'completed': 'success' as const,
      'in_progress': 'info' as const,
      'pending': 'neutral' as const,
      'canceled': 'warning' as const,
    };
    return variants[status as keyof typeof variants] || 'neutral';
  };

  return (
    <div className="space-y-2">
      <div className="flex items-center gap-2 text-xs">
        <StatusBadge text={`${todos.length} items`} variant="info" />
        <span className="text-kodelet-mid-gray">{action}</span>
      </div>

      {todos.length > 0 ? (
        <div className="space-y-1 text-xs">
          {todos.map((todo: TodoItem, index: number) => (
            <div key={index} className="flex items-center gap-2 py-1">
              <StatusBadge text={todo.status} variant={getStatusVariant(todo.status)} />
              <span className={todo.status === 'completed' ? 'line-through text-kodelet-mid-gray' : 'text-kodelet-dark'}>
                {todo.content}
              </span>
            </div>
          ))}
        </div>
      ) : (
        <div className="text-xs text-kodelet-mid-gray">No todos</div>
      )}
    </div>
  );
};

export default TodoRenderer;