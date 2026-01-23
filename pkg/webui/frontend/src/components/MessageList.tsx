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
  // Initialize thinking blocks and tool calls to be expanded by default
  const [expandedToolCalls, setExpandedToolCalls] = useState<string[]>(() => {
    const allToolCallIds: string[] = [];
    messages.forEach(message => {
      const toolCalls = message.toolCalls || message.tool_calls || [];
      toolCalls.forEach(toolCall => {
        if (toolCall.id) {
          allToolCallIds.push(toolCall.id);
        }
      });
    });
    return allToolCallIds;
  });

  const [expandedThinking, setExpandedThinking] = useState<string[]>(() => {
    const allMessageIndices: string[] = [];
    messages.forEach((message, index) => {
      if (message.thinkingText) {
        allMessageIndices.push(index.toString());
      }
    });
    return allMessageIndices;
  });

  // New state for arguments and results within tool calls
  const [expandedArguments, setExpandedArguments] = useState<string[]>([]);
  const [expandedResults, setExpandedResults] = useState<string[]>(() => {
    const allToolCallIds: string[] = [];
    messages.forEach(message => {
      const toolCalls = message.toolCalls || message.tool_calls || [];
      toolCalls.forEach(toolCall => {
        if (toolCall.id && toolResults[toolCall.id]) {
          allToolCallIds.push(toolCall.id);
        }
      });
    });
    return allToolCallIds;
  });

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

  const toggleThinking = (messageIndex: string) => {
    setExpandedThinking(prev => {
      const index = prev.indexOf(messageIndex);
      if (index > -1) {
        return prev.filter(id => id !== messageIndex);
      } else {
        return [...prev, messageIndex];
      }
    });
  };

  const toggleArguments = (toolCallId: string) => {
    setExpandedArguments(prev => {
      const index = prev.indexOf(toolCallId);
      if (index > -1) {
        return prev.filter(id => id !== toolCallId);
      } else {
        return [...prev, toolCallId];
      }
    });
  };

  const toggleResults = (toolCallId: string) => {
    setExpandedResults(prev => {
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
            className={`card shadow-sm ${
              isUser ? 'message-user' : 'message-assistant'
            }`}
          >
            <div className="card-body p-4">
              {/* Message Header */}
              <div className="flex items-center justify-between mb-2">
                <div className="flex items-center gap-2">
                  <div className="avatar placeholder">
                    <div className={`rounded-full w-8 h-8 ${
                      isUser ? 'bg-kodelet-blue text-white' : 'bg-kodelet-dark text-white'
                    }`}>
                      <span className="text-xs font-heading font-semibold">{isUser ? 'U' : 'A'}</span>
                    </div>
                  </div>
                  <div>
                    <h3 className="font-heading font-semibold capitalize text-kodelet-dark">
                      {isUser ? 'You' : 'Assistant'}
                    </h3>
                    <div className="text-xs text-kodelet-mid-gray font-body">
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

              {/* Thinking Block */}
              {message.thinkingText && (
                <div className="mb-3">
                  <div className="flex items-center justify-between mb-2">
                    <div className="flex items-center gap-2">
                      <div className="px-2 py-0.5 rounded text-xs font-heading font-medium bg-kodelet-blue/10 text-kodelet-blue border border-kodelet-blue/20">
                        💭 Thinking
                      </div>
                    </div>
                    <button
                      className={`btn btn-ghost btn-xs ${
                        expandedThinking.includes(index.toString()) ? 'btn-active' : ''
                      }`}
                      onClick={() => toggleThinking(index.toString())}
                      aria-label="Toggle thinking block"
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
                          d={expandedThinking.includes(index.toString()) ? "M19 9l-7 7-7-7" : "M9 5l7 7-7 7"}
                        />
                      </svg>
                    </button>
                  </div>
                  {expandedThinking.includes(index.toString()) && (
                    <div className="bg-kodelet-light-gray/30 border border-kodelet-mid-gray/20 p-3 rounded">
                      <pre className="whitespace-pre-wrap text-sm text-kodelet-dark bg-transparent font-mono">{message.thinkingText.trim()}</pre>
                    </div>
                  )}
                </div>
              )}

              {/* Message Content */}
              <div
                className="prose prose-sm max-w-none"
                dangerouslySetInnerHTML={{
                  __html: renderMessageContent(message.content)
                }}
              />

              {/* Tool Calls */}
              {toolCalls.length > 0 && (
                <div className="mt-3">
                  <h4 className="font-heading font-semibold mb-2 text-sm text-kodelet-dark">Tool Calls:</h4>
                  <div className="space-y-2">
                    {toolCalls.map((toolCall, toolIndex) => (
                      <div key={toolCall.id || toolIndex} className="bg-kodelet-light-gray/20 border border-kodelet-mid-gray/20 p-3 rounded">
                        <div className="flex items-center justify-between mb-2">
                          <div className="flex items-center gap-2">
                            <div className="px-2 py-0.5 rounded text-xs font-heading font-medium bg-kodelet-green/10 text-kodelet-green border border-kodelet-green/20">
                              {toolCall.function?.name || 'Unknown'}
                            </div>
                            <div className="text-xs text-kodelet-mid-gray font-mono">
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
                                d={expandedToolCalls.includes(toolCall.id) ? "M19 9l-7 7-7-7" : "M9 5l7 7-7 7"}
                              />
                            </svg>
                          </button>
                        </div>

                        {expandedToolCalls.includes(toolCall.id) && (
                          <div className="mt-2">
                            {/* Arguments Section */}
                            <div className="mb-2">
                              <div className="flex items-center justify-between mb-1.5">
                                <div className="flex items-center gap-2">
                                  <div className="text-xs font-heading font-medium text-kodelet-mid-gray">
                                    Arguments
                                  </div>
                                </div>
                                <button
                                  className={`btn btn-ghost btn-xs ${
                                    expandedArguments.includes(toolCall.id) ? 'btn-active' : ''
                                  }`}
                                  onClick={() => toggleArguments(toolCall.id)}
                                  aria-label="Toggle arguments"
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
                                      d={expandedArguments.includes(toolCall.id) ? "M19 9l-7 7-7-7" : "M9 5l7 7-7 7"}
                                    />
                                  </svg>
                                </button>
                              </div>
                              {expandedArguments.includes(toolCall.id) && (
                                <pre className="bg-kodelet-light p-2 rounded text-xs overflow-x-auto border border-kodelet-mid-gray/20">
                                  <code className="text-kodelet-dark">
                                    {JSON.stringify(
                                      JSON.parse(toolCall.function?.arguments || '{}'),
                                      null,
                                      2
                                    )}
                                  </code>
                                </pre>
                              )}
                            </div>

                            {/* Tool Result Section */}
                            {toolResults[toolCall.id] && (
                              <div className="mt-2">
                                <div className="flex items-center justify-between mb-1.5">
                                  <div className="flex items-center gap-2">
                                    <div className="text-xs font-heading font-medium text-kodelet-mid-gray">
                                      Result
                                    </div>
                                  </div>
                                  <button
                                    className={`btn btn-ghost btn-xs ${
                                      expandedResults.includes(toolCall.id) ? 'btn-active' : ''
                                    }`}
                                    onClick={() => toggleResults(toolCall.id)}
                                    aria-label="Toggle results"
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
                                        d={expandedResults.includes(toolCall.id) ? "M19 9l-7 7-7-7" : "M9 5l7 7-7 7"}
                                      />
                                    </svg>
                                  </button>
                                </div>
                                {expandedResults.includes(toolCall.id) && (
                                  <ToolRenderer toolResult={toolResults[toolCall.id]} />
                                )}
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