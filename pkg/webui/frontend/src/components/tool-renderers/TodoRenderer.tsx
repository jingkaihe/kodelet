import React from 'react';
import { ToolResult, TodoMetadata, TodoItem } from '../../types';
import { cn } from '../../utils';
import { ReferenceToolHeader, ReferenceToolNote } from './reference';

interface TodoRendererProps {
  toolResult: ToolResult;
}

const TodoRenderer: React.FC<TodoRendererProps> = ({ toolResult }) => {
  const meta = toolResult.metadata as TodoMetadata;
  if (!meta) return null;

  const action = meta.action || 'updated';
  const todos = meta.todos || meta.todoList || [];

  return (
    <div className="space-y-2">
      <ReferenceToolHeader
        badges={[
          { text: `${todos.length} items`, variant: 'info' },
          { text: action, variant: 'neutral' },
        ]}
        title="Todo List"
      />

      {todos.length > 0 ? (
        <div className="space-y-1 text-xs">
          {todos.map((todo: TodoItem, index: number) => (
            <div
              key={index}
              className="flex items-center gap-2 rounded-lg border border-black/8 bg-white/70 px-3 py-2"
            >
              <span className="tool-badge tool-badge-neutral">{todo.status}</span>
              <span className={cn(
                todo.status === 'completed' ? 'line-through text-kodelet-mid-gray' : 'text-kodelet-dark'
              )}>
                {todo.content}
              </span>
            </div>
          ))}
        </div>
      ) : (
        <ReferenceToolNote text="No todos" />
      )}
    </div>
  );
};

export default TodoRenderer;
