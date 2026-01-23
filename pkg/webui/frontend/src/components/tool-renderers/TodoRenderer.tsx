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

  const getStatusBadgeClass = (status: string): string => {
    const classes = {
      'completed': 'bg-kodelet-green/10 text-kodelet-green border-kodelet-green/20',
      'in_progress': 'bg-kodelet-blue/10 text-kodelet-blue border-kodelet-blue/20',
      'pending': 'bg-kodelet-mid-gray/10 text-kodelet-mid-gray border-kodelet-mid-gray/20',
      'canceled': 'bg-kodelet-orange/10 text-kodelet-orange border-kodelet-orange/20'
    };
    return classes[status as keyof typeof classes] || 'bg-kodelet-mid-gray/10 text-kodelet-mid-gray border-kodelet-mid-gray/20';
  };

  const getPriorityClass = (priority: string): string => {
    const classes = {
      'high': 'bg-kodelet-orange/10 text-kodelet-orange border-kodelet-orange/20',
      'medium': 'bg-kodelet-blue/10 text-kodelet-blue border-kodelet-blue/20',
      'low': 'bg-kodelet-green/10 text-kodelet-green border-kodelet-green/20'
    };
    return classes[priority as keyof typeof classes] || 'bg-kodelet-blue/10 text-kodelet-blue border-kodelet-blue/20';
  };

  const renderTodoList = (todos: TodoItem[]) => {
    const todoContent = todos.map((todo, index) => {
      const statusBadgeClass = getStatusBadgeClass(todo.status);
      const priorityClass = getPriorityClass(todo.priority);
      const isCompleted = todo.status === 'completed';

      return (
        <div key={index} className="flex items-start gap-2 p-2 hover:bg-kodelet-light-gray/20 rounded" role="listitem">
          <div className="flex-1">
            <div className={`text-sm font-body ${isCompleted ? 'line-through text-kodelet-mid-gray' : 'text-kodelet-dark'}`}>
              {todo.content}
            </div>
            <div className="flex items-center gap-2 mt-1">
              <div 
                className={`px-1.5 py-0.5 rounded text-xs font-heading font-medium border ${priorityClass}`}
                aria-label={`Priority: ${todo.priority}`}
              >
                {todo.priority}
              </div>
              <div 
                className={`px-1.5 py-0.5 rounded text-xs font-heading font-medium border ${statusBadgeClass}`}
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
        badge={{ text: `${todos.length} items`, className: 'bg-kodelet-blue/10 text-kodelet-blue border border-kodelet-blue/20' }}
      >
        <div role="list">{todoContent}</div>
      </Collapsible>
    );
  };

  return (
    <ToolCard
      title="Todo List"
      badge={{ text: action, className: 'bg-kodelet-blue/10 text-kodelet-blue border border-kodelet-blue/20' }}
    >
      {todos.length > 0 ? (
        renderTodoList(todos)
      ) : (
        <div className="text-sm font-body text-kodelet-mid-gray">No todos available</div>
      )}
    </ToolCard>
  );
};

export default TodoRenderer;