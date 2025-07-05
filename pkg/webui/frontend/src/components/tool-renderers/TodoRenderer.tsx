import React from 'react';
import { ToolResult, TodoMetadata, TodoItem } from '../../types';
import { ToolCard, Collapsible } from './shared';

interface TodoRendererProps {
  toolResult: ToolResult;
}

const TodoRenderer: React.FC<TodoRendererProps> = ({ toolResult }) => {
  const meta = toolResult.metadata as TodoMetadata;
  if (!meta) return null;

  const action = meta.action || 'updated';
  const todos = meta.todos || meta.todoList || [];

  const getTodoStatusIcon = (status: string): string => {
    const icons = {
      'completed': '✅',
      'in_progress': '⏳',
      'pending': '📋',
      'canceled': '❌'
    };
    return icons[status as keyof typeof icons] || '📋';
  };

  const getPriorityClass = (priority: string): string => {
    const classes = {
      'high': 'badge-error',
      'medium': 'badge-warning',
      'low': 'badge-info'
    };
    return classes[priority as keyof typeof classes] || 'badge-info';
  };

  const renderTodoList = (todos: TodoItem[]) => {
    const todoContent = todos.map((todo, index) => {
      const statusIcon = getTodoStatusIcon(todo.status);
      const priorityClass = getPriorityClass(todo.priority);
      const isCompleted = todo.status === 'completed';

      return (
        <div key={index} className="flex items-start gap-3 p-2 hover:bg-base-100 rounded" role="listitem">
          <span className="text-lg" aria-label={todo.status}>
            {statusIcon}
          </span>
          <div className="flex-1">
            <div className={`text-sm ${isCompleted ? 'line-through text-base-content/60' : ''}`}>
              {todo.content}
            </div>
            <div className="flex items-center gap-2 mt-1">
              <div 
                className={`badge badge-xs ${priorityClass}`} 
                aria-label={`Priority: ${todo.priority}`}
              >
                {todo.priority}
              </div>
              <div 
                className="badge badge-xs badge-outline" 
                aria-label={`Status: ${todo.status}`}
              >
                {todo.status}
              </div>
            </div>
          </div>
        </div>
      );
    });

    return (
      <Collapsible
        title="Todo Items"
        collapsed={false}
        badge={{ text: `${todos.length} items`, className: 'badge-info' }}
      >
        <div role="list">{todoContent}</div>
      </Collapsible>
    );
  };

  return (
    <ToolCard
      title="📋 Todo List"
      badge={{ text: action, className: 'badge-info' }}
    >
      {todos.length > 0 ? (
        renderTodoList(todos)
      ) : (
        <div className="text-sm text-base-content/60">No todos available</div>
      )}
    </ToolCard>
  );
};

export default TodoRenderer;