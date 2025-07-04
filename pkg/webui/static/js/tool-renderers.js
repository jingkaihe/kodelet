// Advanced Tool Result Renderers for Web UI
// These renderers use the structured metadata to create rich web experiences

// Tool Result Renderer Registry
class ToolRendererRegistry {
    constructor() {
        this.renderers = new Map();
        this.registerDefaultRenderers();
    }

    registerDefaultRenderers() {
        this.register('file_read', new FileReadRenderer());
        this.register('file_write', new FileWriteRenderer());
        this.register('file_edit', new FileEditRenderer());
        this.register('file_multi_edit', new FileMultiEditRenderer());
        this.register('bash', new BashRenderer());
        this.register('grep_tool', new GrepRenderer());
        this.register('glob_tool', new GlobRenderer());
        this.register('web_fetch', new WebFetchRenderer());
        this.register('image_recognition', new ImageRecognitionRenderer());
        this.register('thinking', new ThinkingRenderer());
        this.register('todo_read', new TodoRenderer());
        this.register('todo_write', new TodoRenderer());
        this.register('subagent', new SubagentRenderer());
        this.register('batch', new BatchRenderer());
        this.register('browser_navigate', new BrowserNavigateRenderer());
        this.register('browser_screenshot', new BrowserScreenshotRenderer());
        this.register('view_background_processes', new BackgroundProcessesRenderer());
        this.register('mcp_definition', new MCPDefinitionRenderer());
        this.register('mcp_references', new MCPReferencesRenderer());
        this.register('mcp_hover', new MCPHoverRenderer());
        this.register('mcp_diagnostics', new MCPDiagnosticsRenderer());
    }

    register(toolName, renderer) {
        this.renderers.set(toolName, renderer);
    }

    render(toolResult) {
        const renderer = this.renderers.get(toolResult.toolName);
        if (!renderer) {
            return this.renderFallback(toolResult);
        }
        return renderer.render(toolResult);
    }

    renderFallback(toolResult) {
        if (!toolResult.success) {
            return `
                <div class="alert alert-error">
                    <div class="flex items-center gap-2">
                        <svg xmlns="http://www.w3.org/2000/svg" class="h-5 w-5" fill="none" viewBox="0 0 24 24" stroke="currentColor">
                            <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M12 8v4m0 4h.01M21 12a9 9 0 11-18 0 9 9 0 0118 0z" />
                        </svg>
                        <strong>Error (${toolResult.toolName}):</strong>
                    </div>
                    <div class="mt-2 text-sm">${this.escapeHtml(toolResult.error)}</div>
                </div>
            `;
        }
        
        return `
            <div class="card bg-base-200 border">
                <div class="card-body">
                    <div class="flex items-center justify-between">
                        <h4 class="card-title text-sm">🔧 ${toolResult.toolName}</h4>
                        <div class="badge badge-info badge-sm">Unknown Tool</div>
                    </div>
                    <div class="collapse collapse-arrow bg-base-100 mt-2">
                        <input type="checkbox">
                        <div class="collapse-title text-xs font-medium">Raw Metadata</div>
                        <div class="collapse-content">
                            <pre class="text-xs overflow-x-auto"><code>${JSON.stringify(toolResult.metadata, null, 2)}</code></pre>
                        </div>
                    </div>
                </div>
            </div>
        `;
    }

    escapeHtml(text) {
        if (!text) return '';
        const div = document.createElement('div');
        div.textContent = text;
        return div.innerHTML;
    }
}

// Base Renderer Class with common utilities
class BaseRenderer {
    render(toolResult) {
        if (!toolResult.success) {
            return this.renderError(toolResult);
        }
        return this.renderSuccess(toolResult);
    }

    renderError(toolResult) {
        return `
            <div class="alert alert-error">
                <div class="flex items-center gap-2">
                    <svg xmlns="http://www.w3.org/2000/svg" class="h-5 w-5" fill="none" viewBox="0 0 24 24" stroke="currentColor">
                        <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M12 8v4m0 4h.01M21 12a9 9 0 11-18 0 9 9 0 0118 0z" />
                    </svg>
                    <strong>Error (${toolResult.toolName}):</strong>
                </div>
                <div class="mt-2 text-sm">${this.escapeHtml(toolResult.error)}</div>
            </div>
        `;
    }

    renderSuccess(toolResult) {
        return `<div class="text-sm text-base-content/70">Tool executed successfully</div>`;
    }

    escapeHtml(text) {
        if (!text) return '';
        const div = document.createElement('div');
        div.textContent = text;
        return div.innerHTML;
    }

    formatTimestamp(timestamp) {
        if (!timestamp) return '';
        return new Date(timestamp).toLocaleString();
    }

    createCollapsible(title, content, collapsed = false, badge = null) {
        const collapseId = `collapse-${Math.random().toString(36).substr(2, 9)}`;
        const badgeHtml = badge ? `<div class="badge badge-sm ${badge.class || 'badge-info'}">${badge.text}</div>` : '';
        
        return `
            <div class="collapse collapse-arrow bg-base-100 mt-2">
                <input type="checkbox" id="${collapseId}" ${collapsed ? '' : 'checked'}>
                <div class="collapse-title text-sm font-medium flex items-center justify-between">
                    <span>${title}</span>
                    ${badgeHtml}
                </div>
                <div class="collapse-content">
                    ${content}
                </div>
            </div>
        `;
    }

    createCodeBlock(code, language = '', showLineNumbers = true, maxHeight = null) {
        const lines = code.split('\n');
        const heightStyle = maxHeight ? `max-height: ${maxHeight}px; overflow-y: auto;` : '';
        
        let codeHtml;
        if (showLineNumbers) {
            codeHtml = lines.map((line, index) => {
                const lineNumber = (index + 1).toString().padStart(3, ' ');
                return `<span class="line-number">${lineNumber}</span><span class="line-content">${this.escapeHtml(line)}</span>`;
            }).join('\n');
        } else {
            codeHtml = this.escapeHtml(code);
        }

        return `
            <div class="mockup-code bg-base-300 text-sm" style="${heightStyle}">
                <pre><code class="language-${language}">${codeHtml}</code></pre>
            </div>
        `;
    }

    createCopyButton(content, className = 'btn-xs') {
        return `
            <button class="btn btn-ghost ${className}" onclick="copyToClipboard('${this.escapeHtml(content).replace(/'/g, '\\\'')}')" title="Copy to clipboard">
                <svg xmlns="http://www.w3.org/2000/svg" class="h-4 w-4" fill="none" viewBox="0 0 24 24" stroke="currentColor">
                    <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M8 16H6a2 2 0 01-2-2V6a2 2 0 012-2h8a2 2 0 012 2v2m-6 12h8a2 2 0 002-2v-8a2 2 0 00-2-2h-8a2 2 0 00-2 2v8a2 2 0 002 2z" />
                </svg>
            </button>
        `;
    }

    formatFileSize(bytes) {
        if (!bytes) return '';
        const sizes = ['B', 'KB', 'MB', 'GB'];
        let size = bytes;
        let unit = 0;
        while (size >= 1024 && unit < sizes.length - 1) {
            size /= 1024;
            unit++;
        }
        return `${size.toFixed(1)} ${sizes[unit]}`;
    }

    detectLanguageFromPath(filePath) {
        if (!filePath) return '';
        const ext = filePath.split('.').pop().toLowerCase();
        const langMap = {
            'js': 'javascript', 'ts': 'typescript', 'py': 'python', 'go': 'go',
            'java': 'java', 'cpp': 'cpp', 'c': 'c', 'cs': 'csharp',
            'php': 'php', 'rb': 'ruby', 'rs': 'rust', 'sh': 'bash',
            'html': 'html', 'css': 'css', 'json': 'json', 'xml': 'xml',
            'yaml': 'yaml', 'yml': 'yaml', 'md': 'markdown', 'sql': 'sql'
        };
        return langMap[ext] || ext;
    }
}

// File Read Renderer
class FileReadRenderer extends BaseRenderer {
    renderSuccess(toolResult) {
        const meta = toolResult.metadata;
        if (!meta) return this.renderFallback(toolResult);

        const language = meta.language || this.detectLanguageFromPath(meta.filePath);
        const fileContent = (meta.lines || []).join('\n');
        const startLine = meta.offset || 1;
        
        // Create line-numbered code
        const lines = meta.lines || [];
        const codeWithLineNumbers = lines.map((line, index) => {
            const lineNumber = (startLine + index).toString().padStart(4, ' ');
            return `<span class="line-number text-base-content/50">${lineNumber}</span><span class="line-content">${this.escapeHtml(line)}</span>`;
        }).join('\n');

        const truncatedBadge = meta.truncated ? 
            '<div class="badge badge-warning badge-sm">Truncated</div>' : '';

        const copyButton = this.createCopyButton(fileContent);

        return `
            <div class="card bg-base-200 border">
                <div class="card-body">
                    <div class="flex items-center justify-between mb-3">
                        <div class="flex items-center gap-2">
                            <h4 class="font-semibold flex items-center gap-2">
                                📄 File Read
                                ${truncatedBadge}
                            </h4>
                        </div>
                        ${copyButton}
                    </div>
                    
                    <div class="text-xs text-base-content/60 mb-3 font-mono">
                        <div class="flex items-center gap-4">
                            <span><strong>Path:</strong> ${this.escapeHtml(meta.filePath)}</span>
                            ${startLine > 1 ? `<span><strong>Starting at line:</strong> ${startLine}</span>` : ''}
                            ${lines.length > 0 ? `<span><strong>Lines:</strong> ${lines.length}</span>` : ''}
                            ${language ? `<span><strong>Language:</strong> ${language}</span>` : ''}
                        </div>
                    </div>
                    
                    <div class="mockup-code bg-base-300 text-sm max-h-96 overflow-y-auto">
                        <pre><code class="language-${language}">${codeWithLineNumbers}</code></pre>
                    </div>
                </div>
            </div>
        `;
    }
}

// File Write Renderer
class FileWriteRenderer extends BaseRenderer {
    renderSuccess(toolResult) {
        const meta = toolResult.metadata;
        if (!meta) return this.renderFallback(toolResult);

        const language = meta.language || this.detectLanguageFromPath(meta.filePath);
        const sizeText = meta.size ? this.formatFileSize(meta.size) : '';
        const copyButton = meta.content ? this.createCopyButton(meta.content) : '';

        return `
            <div class="card bg-base-200 border">
                <div class="card-body">
                    <div class="flex items-center justify-between mb-3">
                        <div class="flex items-center gap-2">
                            <h4 class="font-semibold">📝 File Written</h4>
                            <div class="badge badge-success badge-sm">Success</div>
                        </div>
                        ${copyButton}
                    </div>
                    
                    <div class="text-xs text-base-content/60 mb-3 font-mono">
                        <div class="flex items-center gap-4">
                            <span><strong>Path:</strong> ${this.escapeHtml(meta.filePath)}</span>
                            ${sizeText ? `<span><strong>Size:</strong> ${sizeText}</span>` : ''}
                            ${language ? `<span><strong>Language:</strong> ${language}</span>` : ''}
                        </div>
                    </div>
                    
                    ${meta.content ? this.createCollapsible(
                        'View Content', 
                        this.createCodeBlock(meta.content, language, true, 300),
                        true
                    ) : ''}
                </div>
            </div>
        `;
    }
}

// File Edit Renderer  
class FileEditRenderer extends BaseRenderer {
    renderSuccess(toolResult) {
        const meta = toolResult.metadata;
        if (!meta) return this.renderFallback(toolResult);

        const language = meta.language || this.detectLanguageFromPath(meta.filePath);
        const edits = meta.edits || [];
        
        return `
            <div class="card bg-base-200 border">
                <div class="card-body">
                    <div class="flex items-center justify-between mb-3">
                        <div class="flex items-center gap-2">
                            <h4 class="font-semibold">✏️ File Edit</h4>
                            <div class="badge badge-info badge-sm">${edits.length} edit${edits.length !== 1 ? 's' : ''}</div>
                        </div>
                    </div>
                    
                    <div class="text-xs text-base-content/60 mb-3 font-mono">
                        <span><strong>Path:</strong> ${this.escapeHtml(meta.filePath)}</span>
                    </div>
                    
                    ${edits.length > 0 ? this.createCollapsible(
                        'View Changes',
                        this.renderEdits(edits, language),
                        false,
                        { text: `${edits.length} changes`, class: 'badge-info' }
                    ) : ''}
                </div>
            </div>
        `;
    }

    renderEdits(edits, language) {
        return edits.map((edit, index) => {
            const oldText = edit.oldText || '';
            const newText = edit.newText || '';
            
            return `
                <div class="mb-4">
                    <h5 class="text-sm font-medium mb-2">Edit ${index + 1}: Lines ${edit.startLine}-${edit.endLine}</h5>
                    <div class="grid grid-cols-1 md:grid-cols-2 gap-4">
                        <div>
                            <div class="text-xs text-red-600 mb-1">- Removed</div>
                            <div class="mockup-code bg-red-50 border-red-200">
                                <pre><code class="language-${language}">${this.escapeHtml(oldText)}</code></pre>
                            </div>
                        </div>
                        <div>
                            <div class="text-xs text-green-600 mb-1">+ Added</div>
                            <div class="mockup-code bg-green-50 border-green-200">
                                <pre><code class="language-${language}">${this.escapeHtml(newText)}</code></pre>
                            </div>
                        </div>
                    </div>
                </div>
            `;
        }).join('');
    }
}

// Continue with more renderers in next file...