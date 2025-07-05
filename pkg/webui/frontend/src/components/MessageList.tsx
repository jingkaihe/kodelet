import React, { useState } from 'react';
import { Message, ToolResult, ContentBlock } from '../types';
import { copyToClipboard } from '../utils';
import ToolRenderer from './ToolRenderer';
import { marked } from 'marked';

interface MessageListProps {
  messages: Message[];
  toolResults: Record<string, ToolResult>;
}

const MessageList: React.FC<MessageListProps> = ({ messages, toolResults }) => {
  const [expandedToolCalls, setExpandedToolCalls] = useState<string[]>([]);

  const toggleToolCall = (toolCallId: string) => {
    setExpandedToolCalls(prev => {
      const index = prev.indexOf(toolCallId);
      if (index > -1) {
        return prev.filter(id => id !== toolCallId);
      } else {
        return [...prev, toolCallId];
      }
    });
  };

  const renderMessageContent = (content: string | ContentBlock[]): string => {
    if (typeof content === 'string') {
      return marked.parse(content);
    }

    // Handle array of content blocks (for multimodal messages)
    if (Array.isArray(content)) {
      return content.map(block => {
        if (block.type === 'text') {
          return marked.parse(block.text || '');
        } else if (block.type === 'image') {
          const imageUrl = block.source?.data || block.image_url?.url;
          return `<img src="${imageUrl}" alt="Image" class="max-w-full h-auto rounded">`;
        }
        return '';
      }).join('');
    }

    return marked.parse(String(content));
  };

  const handleCopyMessage = (content: string | ContentBlock[]) => {
    const text = typeof content === 'string' ? content : JSON.stringify(content, null, 2);
    copyToClipboard(text);
  };

  return (
    <div className="space-y-6">
      {messages.map((message, index) => {
        const isUser = message.role === 'user';
        const toolCalls = message.toolCalls || message.tool_calls || [];

        return (
          <div
            key={index}
            className={`card shadow-lg ${
              isUser ? 'bg-primary/10 ml-4' : 'bg-base-100 mr-4'
            }`}
          >
            <div className="card-body">
              {/* Message Header */}
              <div className="flex items-center justify-between mb-3">
                <div className="flex items-center gap-2">
                  <div className="avatar placeholder">
                    <div className="bg-neutral text-neutral-content rounded-full w-8 h-8">
                      <span className="text-xs">{isUser ? 'U' : 'A'}</span>
                    </div>
                  </div>
                  <div>
                    <h3 className="font-semibold capitalize">
                      {isUser ? 'You' : 'Assistant'}
                    </h3>
                    <div className="text-xs text-base-content/60">
                      Message {index + 1}
                    </div>
                  </div>
                </div>

                <div className="flex gap-2">
                  <button
                    className="btn btn-ghost btn-xs"
                    onClick={() => handleCopyMessage(message.content)}
                    title="Copy message"
                    aria-label="Copy message"
                  >
                    <svg
                      xmlns="http://www.w3.org/2000/svg"
                      className="h-4 w-4"
                      fill="none"
                      viewBox="0 0 24 24"
                      stroke="currentColor"
                      aria-hidden="true"
                    >
                      <path
                        strokeLinecap="round"
                        strokeLinejoin="round"
                        strokeWidth="2"
                        d="M8 16H6a2 2 0 01-2-2V6a2 2 0 012-2h8a2 2 0 012 2v2m-6 12h8a2 2 0 002-2v-8a2 2 0 00-2-2h-8a2 2 0 00-2 2v8a2 2 0 002 2z"
                      />
                    </svg>
                  </button>
                </div>
              </div>

              {/* Message Content */}
              <div
                className="prose prose-sm max-w-none"
                dangerouslySetInnerHTML={{
                  __html: renderMessageContent(message.content)
                }}
              />

              {/* Tool Calls */}
              {toolCalls.length > 0 && (
                <div className="mt-4">
                  <h4 className="font-semibold mb-2">Tool Calls:</h4>
                  <div className="space-y-2">
                    {toolCalls.map((toolCall, toolIndex) => (
                      <div key={toolCall.id || toolIndex} className="bg-base-200 p-3 rounded-lg">
                        <div className="flex items-center justify-between mb-2">
                          <div className="flex items-center gap-2">
                            <div className="badge badge-outline">
                              {toolCall.function?.name || 'Unknown'}
                            </div>
                            <div className="text-xs text-base-content/60">
                              {toolCall.id}
                            </div>
                          </div>
                          <button
                            className={`btn btn-ghost btn-xs ${
                              expandedToolCalls.includes(toolCall.id) ? 'btn-active' : ''
                            }`}
                            onClick={() => toggleToolCall(toolCall.id)}
                            aria-label="Toggle tool call details"
                          >
                            <svg
                              xmlns="http://www.w3.org/2000/svg"
                              className="h-4 w-4"
                              fill="none"
                              viewBox="0 0 24 24"
                              stroke="currentColor"
                              aria-hidden="true"
                            >
                              <path
                                strokeLinecap="round"
                                strokeLinejoin="round"
                                strokeWidth="2"
                                d="M19 9l-7 7-7-7"
                              />
                            </svg>
                          </button>
                        </div>

                        {expandedToolCalls.includes(toolCall.id) && (
                          <div className="mt-2">
                            <div className="text-sm">
                              <strong>Arguments:</strong>
                              <pre className="bg-base-100 p-2 rounded text-xs mt-1 overflow-x-auto">
                                <code>
                                  {JSON.stringify(
                                    JSON.parse(toolCall.function?.arguments || '{}'),
                                    null,
                                    2
                                  )}
                                </code>
                              </pre>
                            </div>

                            {/* Tool Result */}
                            {toolResults[toolCall.id] && (
                              <div className="mt-3">
                                <div className="divider divider-start">Result</div>
                                <ToolRenderer toolResult={toolResults[toolCall.id]} />
                              </div>
                            )}
                          </div>
                        )}
                      </div>
                    ))}
                  </div>
                </div>
              )}
            </div>
          </div>
        );
      })}
    </div>
  );
};

export default MessageList;