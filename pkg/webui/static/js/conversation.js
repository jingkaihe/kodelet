// Conversation View Alpine.js Component and Tool Result Renderers

// Initialize the updated registry - now using the advanced renderers
let toolRendererRegistry = null;

// Initialize immediately if available, otherwise wait for DOMContentLoaded
if (typeof window.toolRendererRegistry !== 'undefined') {
    toolRendererRegistry = window.toolRendererRegistry;
} else {
    // Wait for all renderer files to load
    document.addEventListener('DOMContentLoaded', function() {
        // Check if the registry is available from tool-renderers.js
        if (typeof window.toolRendererRegistry !== 'undefined') {
            toolRendererRegistry = window.toolRendererRegistry;
        } else if (typeof window.ToolRendererRegistry !== 'undefined') {
            // Fallback: create a new instance if class is available
            toolRendererRegistry = new window.ToolRendererRegistry();
        }
    });
}

// Conversation View Alpine.js Component
function conversationViewApp(conversationId) {
    return {
        // State
        conversationId,
        conversation: {
            id: '',
            messages: [],
            toolResults: {},
            usage: {},
            createdAt: null,
            updatedAt: null,
            messageCount: 0,
            summary: '',
            modelType: ''
        },
        loading: false,
        error: null,
        expandedToolCalls: [],
        
        // Initialization
        init() {
            this.loadConversation();
        },
        
        // Load conversation
        async loadConversation() {
            this.loading = true;
            this.error = null;
            
            try {
                const response = await apiCall(`/api/conversations/${this.conversationId}`);
                
                // Ensure all messages have proper structure
                if (response.messages) {
                    response.messages = response.messages.map(message => ({
                        role: message.role || 'user',
                        content: message.content || '',
                        toolCalls: message.toolCalls || message.tool_calls || []
                    }));
                }
                
                // Ensure toolResults is always an object
                if (!response.toolResults) {
                    response.toolResults = {};
                }
                
                // Update the conversation object with the response
                this.conversation = {
                    ...this.conversation,
                    ...response
                };
            } catch (err) {
                this.error = err.message;
                console.error('Failed to load conversation:', err);
            } finally {
                this.loading = false;
            }
        },
        
        // Render message content
        renderMessageContent(content) {
            if (typeof content === 'string') {
                return renderMarkdown(content);
            }
            
            // Handle array of content blocks (for multimodal messages)
            if (Array.isArray(content)) {
                return content.map(block => {
                    if (block.type === 'text') {
                        return renderMarkdown(block.text);
                    } else if (block.type === 'image') {
                        return `<img src="${block.source?.data || block.image_url?.url}" alt="Image" class="max-w-full h-auto rounded">`;
                    }
                    return '';
                }).join('');
            }
            
            return renderMarkdown(String(content));
        },
        
        // Render tool result using the advanced renderer registry
        renderToolResult(toolResult) {
            try {
                if (!toolResult) {
                    console.warn('No tool result provided to renderToolResult');
                    return '<div class="text-sm text-base-content/60">No tool result available</div>';
                }
                
                if (!toolRendererRegistry) {
                    // Fallback if registry not loaded yet
                    console.warn('Tool renderer registry not loaded, using fallback');
                    return this.renderToolResultFallback(toolResult);
                }
                
                return toolRendererRegistry.render(toolResult);
            } catch (error) {
                console.error('Error rendering tool result:', error, toolResult);
                return this.renderToolResultFallback(toolResult);
            }
        },
        
        // Fallback renderer for when the advanced registry isn't available
        renderToolResultFallback(toolResult) {
            const successIcon = toolResult.success ? '✅' : '❌';
            const statusClass = toolResult.success ? 'badge-success' : 'badge-error';
            const timestamp = toolResult.timestamp ? new Date(toolResult.timestamp).toLocaleString() : '';
            
            return `
                <div class="card bg-base-200 border">
                    <div class="card-body">
                        <div class="flex items-center justify-between mb-3">
                            <div class="flex items-center gap-2">
                                <span>${successIcon}</span>
                                <h4 class="font-semibold">${toolResult.toolName}</h4>
                                <div class="badge badge-sm ${statusClass}">
                                    ${toolResult.success ? 'Success' : 'Error'}
                                </div>
                            </div>
                            ${timestamp ? `<div class="text-xs text-base-content/60">${timestamp}</div>` : ''}
                        </div>
                        
                        ${!toolResult.success && toolResult.error ? `
                            <div class="alert alert-error alert-sm">
                                <span>${this.escapeHtml(toolResult.error)}</span>
                            </div>
                        ` : ''}
                        
                        ${toolResult.metadata ? `
                            <div class="collapse collapse-arrow bg-base-100 mt-2">
                                <input type="checkbox">
                                <div class="collapse-title text-sm font-medium">Raw Metadata</div>
                                <div class="collapse-content">
                                    <pre class="text-xs overflow-x-auto"><code>${JSON.stringify(toolResult.metadata, null, 2)}</code></pre>
                                </div>
                            </div>
                        ` : ''}
                    </div>
                </div>
            `;
        },
        
        // HTML escape utility
        escapeHtml(text) {
            if (!text) return '';
            const div = document.createElement('div');
            div.textContent = text;
            return div.innerHTML;
        },
        
        // Toggle tool call expansion
        toggleToolCall(toolCallId) {
            const index = this.expandedToolCalls.indexOf(toolCallId);
            if (index > -1) {
                this.expandedToolCalls.splice(index, 1);
            } else {
                this.expandedToolCalls.push(toolCallId);
            }
        },
        
        // Copy message content
        copyMessage(content) {
            const text = typeof content === 'string' ? content : JSON.stringify(content, null, 2);
            copyToClipboard(text);
        },
        
        // Export conversation
        exportConversation() {
            if (!this.conversation.id) return;
            
            const exportData = {
                id: this.conversation.id,
                createdAt: this.conversation.createdAt,
                messages: this.conversation.messages,
                usage: this.conversation.usage,
                toolResults: this.conversation.toolResults
            };
            
            const blob = new Blob([JSON.stringify(exportData, null, 2)], { type: 'application/json' });
            const url = URL.createObjectURL(blob);
            const a = document.createElement('a');
            a.href = url;
            a.download = `conversation-${this.conversation.id.substring(0, 8)}.json`;
            a.click();
            URL.revokeObjectURL(url);
        },
        
        // Delete conversation
        async deleteConversation() {
            if (!this.conversation.id) return;
            
            if (!confirm('Are you sure you want to delete this conversation?')) {
                return;
            }
            
            try {
                await apiCall(`/api/conversations/${this.conversation.id}`, {
                    method: 'DELETE'
                });
                
                showToast('Conversation deleted successfully', 'success');
                // Redirect to conversation list
                window.location.href = '/';
            } catch (err) {
                showToast(`Failed to delete conversation: ${err.message}`, 'error');
            }
        },
        
        // Utility functions
        formatDate,
        formatCost
    };
}

// Make the conversation view app available globally
window.conversationViewApp = conversationViewApp;