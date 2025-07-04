// Complete Tool Result Renderers - Part 3
// Stub implementations for the remaining tools

// Todo Management Renderer
class TodoRenderer extends BaseRenderer {
    renderSuccess(toolResult) {
        const meta = toolResult.metadata;
        if (!meta) return this.renderFallback(toolResult);

        const action = meta.action || 'updated';
        const todos = meta.todos || meta.todoList || [];
        
        return `
            <div class="card bg-base-200 border">
                <div class="card-body">
                    <div class="flex items-center justify-between mb-3">
                        <div class="flex items-center gap-2">
                            <h4 class="font-semibold">📋 Todo List</h4>
                            <div class="badge badge-info badge-sm">${action}</div>
                        </div>
                    </div>
                    
                    ${todos.length > 0 ? this.renderTodoList(todos) : 
                        '<div class="text-sm text-base-content/60">No todos available</div>'}
                </div>
            </div>
        `;
    }

    renderTodoList(todos) {
        const todoContent = todos.map(todo => {
            const statusIcon = this.getTodoStatusIcon(todo.status);
            const priorityClass = this.getPriorityClass(todo.priority);
            
            return `
                <div class="flex items-start gap-3 p-2 hover:bg-base-100 rounded">
                    <span class="text-lg">${statusIcon}</span>
                    <div class="flex-1">
                        <div class="text-sm ${todo.status === 'completed' ? 'line-through text-base-content/60' : ''}">${this.escapeHtml(todo.content)}</div>
                        <div class="flex items-center gap-2 mt-1">
                            <div class="badge badge-xs ${priorityClass}">${todo.priority}</div>
                            <div class="badge badge-xs badge-outline">${todo.status}</div>
                        </div>
                    </div>
                </div>
            `;
        }).join('');

        return this.createCollapsible('Todo Items', todoContent, false, 
            { text: `${todos.length} items`, class: 'badge-info' });
    }

    getTodoStatusIcon(status) {
        const icons = {
            'completed': '✅',
            'in_progress': '⏳',
            'pending': '📋',
            'canceled': '❌'
        };
        return icons[status] || '📋';
    }

    getPriorityClass(priority) {
        const classes = {
            'high': 'badge-error',
            'medium': 'badge-warning',
            'low': 'badge-info'
        };
        return classes[priority] || 'badge-info';
    }
}

// Sub-agent Renderer
class SubagentRenderer extends BaseRenderer {
    renderSuccess(toolResult) {
        const meta = toolResult.metadata;
        if (!meta) return this.renderFallback(toolResult);

        const modelStrength = meta.modelStrength || meta.model_strength || 'unknown';
        
        return `
            <div class="card bg-base-200 border">
                <div class="card-body">
                    <div class="flex items-center justify-between mb-3">
                        <div class="flex items-center gap-2">
                            <h4 class="font-semibold">🤖 Sub-agent</h4>
                            <div class="badge badge-info badge-sm">${modelStrength} model</div>
                        </div>
                    </div>
                    
                    <div class="space-y-3">
                        <div>
                            <div class="text-xs text-base-content/60 mb-1"><strong>Question:</strong></div>
                            <div class="bg-blue-50 p-3 rounded text-sm">${this.escapeHtml(meta.question)}</div>
                        </div>
                        
                        ${meta.response ? `
                            <div>
                                <div class="text-xs text-base-content/60 mb-1"><strong>Response:</strong></div>
                                <div class="bg-green-50 p-3 rounded text-sm">${this.formatMarkdown(meta.response)}</div>
                            </div>
                        ` : ''}
                    </div>
                </div>
            </div>
        `;
    }

    formatMarkdown(text) {
        if (!text) return '';
        // Basic markdown formatting
        return this.escapeHtml(text)
            .replace(/\*\*(.*?)\*\*/g, '<strong>$1</strong>')
            .replace(/\*(.*?)\*/g, '<em>$1</em>')
            .replace(/`(.*?)`/g, '<code class="bg-gray-200 px-1 rounded">$1</code>')
            .replace(/\n/g, '<br>');
    }
}

// Batch Operation Renderer
class BatchRenderer extends BaseRenderer {
    renderSuccess(toolResult) {
        const meta = toolResult.metadata;
        if (!meta) return this.renderFallback(toolResult);

        const description = meta.description || 'Batch operation';
        const subResults = meta.subResults || meta.results || [];
        const successCount = meta.successCount || subResults.filter(r => r.success).length;
        const failureCount = meta.failureCount || subResults.filter(r => !r.success).length;
        
        return `
            <div class="card bg-base-200 border">
                <div class="card-body">
                    <div class="flex items-center justify-between mb-3">
                        <div class="flex items-center gap-2">
                            <h4 class="font-semibold">📦 Batch Operation</h4>
                            <div class="badge badge-info badge-sm">${description}</div>
                        </div>
                    </div>
                    
                    <div class="text-xs text-base-content/60 mb-3">
                        <div class="flex items-center gap-4">
                            <span><strong>Total:</strong> ${subResults.length} operations</span>
                            <span class="text-green-600"><strong>Success:</strong> ${successCount}</span>
                            ${failureCount > 0 ? `<span class="text-red-600"><strong>Failed:</strong> ${failureCount}</span>` : ''}
                        </div>
                    </div>
                    
                    ${subResults.length > 0 ? this.createCollapsible(
                        'Sub-operations',
                        this.renderSubResults(subResults),
                        true,
                        { text: `${subResults.length} operations`, class: 'badge-info' }
                    ) : ''}
                </div>
            </div>
        `;
    }

    renderSubResults(subResults) {
        return subResults.map((result, index) => {
            const statusIcon = result.success ? '✅' : '❌';
            const statusClass = result.success ? 'text-green-600' : 'text-red-600';
            
            return `
                <div class="border rounded p-3 mb-2">
                    <div class="flex items-center gap-2 mb-2">
                        <span class="${statusClass}">${statusIcon}</span>
                        <span class="font-medium text-sm">Operation ${index + 1}</span>
                        ${result.toolName ? `<div class="badge badge-xs badge-outline">${result.toolName}</div>` : ''}
                    </div>
                    ${result.error ? `<div class="text-xs text-red-600">${this.escapeHtml(result.error)}</div>` : ''}
                </div>
            `;
        }).join('');
    }
}

// Image Recognition Renderer
class ImageRecognitionRenderer extends BaseRenderer {
    renderSuccess(toolResult) {
        const meta = toolResult.metadata;
        if (!meta) return this.renderFallback(toolResult);

        const imagePath = meta.imagePath || meta.image_path || meta.path;
        const analysis = meta.analysis || meta.result;
        
        return `
            <div class="card bg-base-200 border">
                <div class="card-body">
                    <div class="flex items-center justify-between mb-3">
                        <div class="flex items-center gap-2">
                            <h4 class="font-semibold">👁️ Image Recognition</h4>
                            <div class="badge badge-success badge-sm">Analyzed</div>
                        </div>
                    </div>
                    
                    <div class="text-xs text-base-content/60 mb-3 font-mono">
                        <div><strong>Image:</strong> ${this.escapeHtml(imagePath)}</div>
                        ${meta.prompt ? `<div><strong>Prompt:</strong> ${this.escapeHtml(meta.prompt)}</div>` : ''}
                    </div>
                    
                    ${analysis ? `
                        <div class="bg-base-100 p-3 rounded">
                            <div class="text-sm">${this.escapeHtml(analysis)}</div>
                        </div>
                    ` : ''}
                </div>
            </div>
        `;
    }
}

// Browser Navigation Renderer
class BrowserNavigateRenderer extends BaseRenderer {
    renderSuccess(toolResult) {
        const meta = toolResult.metadata;
        if (!meta) return this.renderFallback(toolResult);

        const url = meta.url;
        const title = meta.title || meta.pageTitle;
        
        return `
            <div class="card bg-base-200 border">
                <div class="card-body">
                    <div class="flex items-center justify-between mb-3">
                        <div class="flex items-center gap-2">
                            <h4 class="font-semibold">🌐 Browser Navigation</h4>
                            <div class="badge badge-success badge-sm">Success</div>
                        </div>
                        <a href="${url}" target="_blank" rel="noopener" class="btn btn-ghost btn-xs">
                            <svg xmlns="http://www.w3.org/2000/svg" class="h-4 w-4" fill="none" viewBox="0 0 24 24" stroke="currentColor">
                                <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M10 6H6a2 2 0 00-2 2v10a2 2 0 002 2h10a2 2 0 002-2v-4M14 4h6m0 0v6m0-6L10 14" />
                            </svg>
                        </a>
                    </div>
                    
                    <div class="text-xs text-base-content/60 font-mono">
                        <div class="space-y-1">
                            <div><strong>URL:</strong> ${this.escapeHtml(url)}</div>
                            ${title ? `<div><strong>Title:</strong> ${this.escapeHtml(title)}</div>` : ''}
                        </div>
                    </div>
                </div>
            </div>
        `;
    }
}

// Browser Screenshot Renderer
class BrowserScreenshotRenderer extends BaseRenderer {
    renderSuccess(toolResult) {
        const meta = toolResult.metadata;
        if (!meta) return this.renderFallback(toolResult);

        const filePath = meta.filePath || meta.file_path || meta.path;
        const dimensions = meta.dimensions || meta.size;
        
        return `
            <div class="card bg-base-200 border">
                <div class="card-body">
                    <div class="flex items-center justify-between mb-3">
                        <div class="flex items-center gap-2">
                            <h4 class="font-semibold">📸 Browser Screenshot</h4>
                            <div class="badge badge-success badge-sm">Captured</div>
                        </div>
                    </div>
                    
                    <div class="text-xs text-base-content/60 mb-3 font-mono">
                        <div class="space-y-1">
                            <div><strong>File:</strong> ${this.escapeHtml(filePath)}</div>
                            ${dimensions ? `<div><strong>Dimensions:</strong> ${dimensions}</div>` : ''}
                        </div>
                    </div>
                    
                    ${filePath && this.isImageFile(filePath) ? `
                        <div class="mt-3">
                            <img src="file://${filePath}" alt="Screenshot" class="max-w-full h-auto rounded border" 
                                 onerror="this.style.display='none'">
                        </div>
                    ` : ''}
                </div>
            </div>
        `;
    }

    isImageFile(path) {
        const imageExts = ['.png', '.jpg', '.jpeg', '.gif', '.bmp', '.webp'];
        return imageExts.some(ext => path.toLowerCase().endsWith(ext));
    }
}

// Background Processes Renderer
class BackgroundProcessesRenderer extends BaseRenderer {
    renderSuccess(toolResult) {
        const meta = toolResult.metadata;
        if (!meta) return this.renderFallback(toolResult);

        const processes = meta.processes || [];
        const processCount = meta.processCount || processes.length;
        
        return `
            <div class="card bg-base-200 border">
                <div class="card-body">
                    <div class="flex items-center justify-between mb-3">
                        <div class="flex items-center gap-2">
                            <h4 class="font-semibold">⚙️ Background Processes</h4>
                            <div class="badge badge-info badge-sm">${processCount} processes</div>
                        </div>
                    </div>
                    
                    ${processes.length > 0 ? this.renderProcessList(processes) : 
                        '<div class="text-sm text-base-content/60">No background processes</div>'}
                </div>
            </div>
        `;
    }

    renderProcessList(processes) {
        const processContent = processes.map(process => {
            const statusIcon = process.status === 'running' ? '🟢' : '🔴';
            const statusClass = process.status === 'running' ? 'text-green-600' : 'text-red-600';
            
            return `
                <div class="flex items-center justify-between p-2 hover:bg-base-100 rounded">
                    <div class="flex items-center gap-3">
                        <span>${statusIcon}</span>
                        <div>
                            <div class="text-sm font-mono">${this.escapeHtml(process.command || 'Unknown')}</div>
                            <div class="text-xs text-base-content/60">PID: ${process.pid || 'Unknown'}</div>
                        </div>
                    </div>
                    <div class="text-xs ${statusClass}">${process.status || 'Unknown'}</div>
                </div>
            `;
        }).join('');

        return this.createCollapsible('Processes', processContent, false,
            { text: `${processes.length} processes`, class: 'badge-info' });
    }
}

// MCP Tool Renderers
class MCPDefinitionRenderer extends BaseRenderer {
    renderSuccess(toolResult) {
        const meta = toolResult.metadata;
        if (!meta) return this.renderFallback(toolResult);

        return `
            <div class="card bg-base-200 border">
                <div class="card-body">
                    <div class="flex items-center gap-2 mb-3">
                        <h4 class="font-semibold">🔍 Code Definition</h4>
                        <div class="badge badge-info badge-sm">MCP</div>
                    </div>
                    <div class="text-sm text-base-content/60">Code definition retrieved</div>
                </div>
            </div>
        `;
    }
}

class MCPReferencesRenderer extends BaseRenderer {
    renderSuccess(toolResult) {
        const meta = toolResult.metadata;
        if (!meta) return this.renderFallback(toolResult);

        return `
            <div class="card bg-base-200 border">
                <div class="card-body">
                    <div class="flex items-center gap-2 mb-3">
                        <h4 class="font-semibold">📚 Code References</h4>
                        <div class="badge badge-info badge-sm">MCP</div>
                    </div>
                    <div class="text-sm text-base-content/60">Code references found</div>
                </div>
            </div>
        `;
    }
}

class MCPHoverRenderer extends BaseRenderer {
    renderSuccess(toolResult) {
        const meta = toolResult.metadata;
        if (!meta) return this.renderFallback(toolResult);

        return `
            <div class="card bg-base-200 border">
                <div class="card-body">
                    <div class="flex items-center gap-2 mb-3">
                        <h4 class="font-semibold">💡 Code Hover Info</h4>
                        <div class="badge badge-info badge-sm">MCP</div>
                    </div>
                    <div class="text-sm text-base-content/60">Hover information retrieved</div>
                </div>
            </div>
        `;
    }
}

class MCPDiagnosticsRenderer extends BaseRenderer {
    renderSuccess(toolResult) {
        const meta = toolResult.metadata;
        if (!meta) return this.renderFallback(toolResult);

        return `
            <div class="card bg-base-200 border">
                <div class="card-body">
                    <div class="flex items-center gap-2 mb-3">
                        <h4 class="font-semibold">🔍 Code Diagnostics</h4>
                        <div class="badge badge-info badge-sm">MCP</div>
                    </div>
                    <div class="text-sm text-base-content/60">Diagnostics retrieved</div>
                </div>
            </div>
        `;
    }
}