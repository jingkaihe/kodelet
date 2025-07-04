// Improved Tool Result Renderers for Web UI
// Consolidated with better patterns and security

// Global utility functions
function copyToClipboard(text) {
    if (navigator.clipboard && navigator.clipboard.writeText) {
        navigator.clipboard.writeText(text).then(() => {
            // Show success toast or feedback
            showToast('Copied to clipboard!', 'success');
        }).catch(err => {
            console.error('Failed to copy:', err);
            // Fallback to older method
            fallbackCopyToClipboard(text);
        });
    } else {
        // Fallback for older browsers
        fallbackCopyToClipboard(text);
    }
}

function fallbackCopyToClipboard(text) {
    const textArea = document.createElement('textarea');
    textArea.value = text;
    textArea.style.position = 'fixed';
    textArea.style.opacity = '0';
    document.body.appendChild(textArea);
    textArea.select();
    try {
        document.execCommand('copy');
        showToast('Copied to clipboard!', 'success');
    } catch (err) {
        console.error('Failed to copy:', err);
        showToast('Failed to copy to clipboard', 'error');
    }
    document.body.removeChild(textArea);
}

function showToast(message, type = 'info') {
    // Simple toast notification - can be enhanced with a toast library
    const toast = document.createElement('div');
    toast.className = `toast toast-top toast-end`;
    toast.innerHTML = `
        <div class="alert alert-${type}">
            <span>${message}</span>
        </div>
    `;
    document.body.appendChild(toast);
    setTimeout(() => {
        toast.remove();
    }, 3000);
}

// Tool Result Renderer Registry
class ToolRendererRegistry {
    constructor() {
        this.renderers = new Map();
        this.renderCache = new Map();
        this.cacheTimeout = 5 * 60 * 1000; // 5 minutes
        this.registerDefaultRenderers();
    }

    registerDefaultRenderers() {
        // Lazy loading of renderers
        const rendererMap = {
            'file_read': FileReadRenderer,
            'file_write': FileWriteRenderer,
            'file_edit': FileEditRenderer,
            'file_multi_edit': FileMultiEditRenderer,
            'bash': BashRenderer,
            'grep_tool': GrepRenderer,
            'glob_tool': GlobRenderer,
            'web_fetch': WebFetchRenderer,
            'image_recognition': ImageRecognitionRenderer,
            'thinking': ThinkingRenderer,
            'todo_read': TodoRenderer,
            'todo_write': TodoRenderer,
            'subagent': SubagentRenderer,
            'batch': BatchRenderer,
            'browser_navigate': BrowserNavigateRenderer,
            'browser_screenshot': BrowserScreenshotRenderer,
            'view_background_processes': BackgroundProcessesRenderer,
            'mcp_definition': MCPDefinitionRenderer,
            'mcp_references': MCPReferencesRenderer,
            'mcp_hover': MCPHoverRenderer,
            'mcp_diagnostics': MCPDiagnosticsRenderer
        };

        // Register class references for lazy instantiation
        Object.entries(rendererMap).forEach(([toolName, RendererClass]) => {
            this.renderers.set(toolName, RendererClass);
        });
    }

    register(toolName, rendererClass) {
        this.renderers.set(toolName, rendererClass);
    }

    getRenderer(toolName) {
        const RendererClass = this.renderers.get(toolName);
        if (!RendererClass) return null;
        
        // Lazy instantiation
        if (typeof RendererClass === 'function') {
            const instance = new RendererClass();
            this.renderers.set(toolName, instance);
            return instance;
        }
        
        return RendererClass;
    }

    render(toolResult) {
        // Check cache first
        const cacheKey = this.getCacheKey(toolResult);
        const cached = this.renderCache.get(cacheKey);
        if (cached && Date.now() - cached.timestamp < this.cacheTimeout) {
            return cached.html;
        }

        // Render
        const renderer = this.getRenderer(toolResult.toolName);
        const html = renderer ? renderer.render(toolResult) : this.renderFallback(toolResult);
        
        // Cache the result
        this.renderCache.set(cacheKey, {
            html: html,
            timestamp: Date.now()
        });

        // Clean old cache entries
        this.cleanCache();

        return html;
    }

    getCacheKey(toolResult) {
        return `${toolResult.toolName}_${JSON.stringify(toolResult.metadata)}`;
    }

    cleanCache() {
        const now = Date.now();
        for (const [key, value] of this.renderCache.entries()) {
            if (now - value.timestamp > this.cacheTimeout) {
                this.renderCache.delete(key);
            }
        }
    }

    renderFallback(toolResult) {
        if (!toolResult.success) {
            return RendererFactory.createErrorCard(
                `Error (${toolResult.toolName})`,
                toolResult.error
            );
        }
        
        return RendererFactory.createCard({
            title: `🔧 ${toolResult.toolName}`,
            badge: { text: 'Unknown Tool', class: 'badge-info' },
            content: RendererFactory.createCollapsible(
                'Raw Metadata',
                `<pre class="text-xs overflow-x-auto"><code>${JSON.stringify(toolResult.metadata, null, 2)}</code></pre>`,
                false
            )
        });
    }
}

// Renderer Factory for common patterns
class RendererFactory {
    static escapeHtml(text) {
        if (!text) return '';
        const div = document.createElement('div');
        div.textContent = text;
        return div.innerHTML;
    }

    static escapeUrl(url) {
        if (!url) return '';
        try {
            // Validate URL
            const parsed = new URL(url);
            // Only allow http(s) and file protocols
            if (!['http:', 'https:', 'file:'].includes(parsed.protocol)) {
                return '#';
            }
            return url;
        } catch {
            return '#';
        }
    }

    static createCard(options) {
        const {
            title,
            badge,
            actions,
            content,
            className = 'bg-base-200'
        } = options;

        const badgeHtml = badge ? 
            `<div class="badge ${badge.class || 'badge-info'} badge-sm">${badge.text}</div>` : '';
        
        const actionsHtml = actions ? 
            `<div class="card-actions">${actions}</div>` : '';

        return `
            <div class="card ${className} border" role="article">
                <div class="card-body">
                    <div class="flex items-center justify-between mb-3">
                        <div class="flex items-center gap-2">
                            <h4 class="font-semibold">${title}</h4>
                            ${badgeHtml}
                        </div>
                        ${actionsHtml}
                    </div>
                    ${content}
                </div>
            </div>
        `;
    }

    static createErrorCard(title, message) {
        return `
            <div class="alert alert-error" role="alert">
                <div class="flex items-center gap-2">
                    <svg xmlns="http://www.w3.org/2000/svg" class="h-5 w-5" fill="none" viewBox="0 0 24 24" stroke="currentColor" aria-hidden="true">
                        <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M12 8v4m0 4h.01M21 12a9 9 0 11-18 0 9 9 0 0118 0z" />
                    </svg>
                    <strong>${this.escapeHtml(title)}:</strong>
                </div>
                <div class="mt-2 text-sm">${this.escapeHtml(message)}</div>
            </div>
        `;
    }

    static createCollapsible(title, content, collapsed = false, badge = null) {
        const collapseId = `collapse-${Math.random().toString(36).substr(2, 9)}`;
        const badgeHtml = badge ? 
            `<div class="badge badge-sm ${badge.class || 'badge-info'}" aria-label="${badge.text}">${badge.text}</div>` : '';
        
        return `
            <div class="collapse collapse-arrow bg-base-100 mt-2" role="region">
                <input type="checkbox" id="${collapseId}" ${collapsed ? '' : 'checked'} 
                       aria-expanded="${!collapsed}" aria-controls="${collapseId}-content">
                <label for="${collapseId}" class="collapse-title text-sm font-medium flex items-center justify-between cursor-pointer">
                    <span>${title}</span>
                    ${badgeHtml}
                </label>
                <div class="collapse-content" id="${collapseId}-content">
                    ${content}
                </div>
            </div>
        `;
    }

    static createCopyButton(content, className = 'btn-xs') {
        const escapedContent = this.escapeHtml(JSON.stringify(content));
        return `
            <button class="btn btn-ghost ${className}" 
                    onclick="copyToClipboard(${escapedContent})" 
                    title="Copy to clipboard"
                    aria-label="Copy to clipboard">
                <svg xmlns="http://www.w3.org/2000/svg" class="h-4 w-4" fill="none" viewBox="0 0 24 24" stroke="currentColor" aria-hidden="true">
                    <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M8 16H6a2 2 0 01-2-2V6a2 2 0 012-2h8a2 2 0 012 2v2m-6 12h8a2 2 0 002-2v-8a2 2 0 00-2-2h-8a2 2 0 00-2 2v8a2 2 0 002 2z" />
                </svg>
            </button>
        `;
    }

    static createCodeBlock(code, language = '', showLineNumbers = true, maxHeight = null) {
        const lines = code.split('\n');
        const heightStyle = maxHeight ? `max-height: ${maxHeight}px; overflow-y: auto;` : '';
        
        let codeHtml;
        if (showLineNumbers) {
            codeHtml = lines.map((line, index) => {
                const lineNumber = (index + 1).toString().padStart(3, ' ');
                return `<span class="line-number" aria-hidden="true">${lineNumber}</span><span class="line-content">${this.escapeHtml(line)}</span>`;
            }).join('\n');
        } else {
            codeHtml = this.escapeHtml(code);
        }

        return `
            <div class="mockup-code bg-base-300 text-sm" style="${heightStyle}" role="region" aria-label="Code block">
                <pre><code class="language-${language}">${codeHtml}</code></pre>
            </div>
        `;
    }

    static formatFileSize(bytes) {
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

    static formatTimestamp(timestamp) {
        if (!timestamp) return '';
        return new Date(timestamp).toLocaleString();
    }

    static formatDuration(duration) {
        if (typeof duration === 'string') {
            return duration;
        }
        // If it's in nanoseconds, convert to seconds
        if (duration > 1000000000) {
            return `${(duration / 1000000000).toFixed(3)}s`;
        }
        return `${duration}ms`;
    }

    static formatMarkdown(text) {
        if (!text) return '';
        // Use marked.js if available
        if (typeof marked !== 'undefined') {
            return marked.parse(text);
        }
        // Fallback to basic formatting
        return this.escapeHtml(text)
            .replace(/\*\*(.*?)\*\*/g, '<strong>$1</strong>')
            .replace(/\*(.*?)\*/g, '<em>$1</em>')
            .replace(/`(.*?)`/g, '<code class="bg-gray-200 px-1 rounded">$1</code>')
            .replace(/\n\n/g, '</p><p>')
            .replace(/\n/g, '<br>')
            .replace(/^/, '<p>')
            .replace(/$/, '</p>');
    }

    static detectLanguageFromPath(filePath) {
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

    static getFileIcon(path) {
        if (!path) return '📄';
        const ext = path.split('.').pop().toLowerCase();
        const iconMap = {
            'js': '📜', 'ts': '📜', 'py': '🐍', 'go': '🐹', 'java': '☕',
            'html': '🌐', 'css': '🎨', 'json': '📋', 'xml': '📄',
            'md': '📝', 'txt': '📄', 'log': '📊',
            'jpg': '🖼️', 'jpeg': '🖼️', 'png': '🖼️', 'gif': '🖼️',
            'pdf': '📕', 'doc': '📘', 'docx': '📘',
            'zip': '📦', 'tar': '📦', 'gz': '📦'
        };
        return iconMap[ext] || '📄';
    }
}

// Base Renderer Class with common utilities
class BaseRenderer {
    render(toolResult) {
        try {
            if (!toolResult.success) {
                return this.renderError(toolResult);
            }
            return this.renderSuccess(toolResult);
        } catch (error) {
            console.error('Renderer error:', error);
            return RendererFactory.createErrorCard(
                `Renderer Error (${toolResult.toolName})`,
                'Failed to render tool result'
            );
        }
    }

    renderError(toolResult) {
        return RendererFactory.createErrorCard(
            `Error (${toolResult.toolName})`,
            toolResult.error || 'Unknown error'
        );
    }

    renderSuccess(toolResult) {
        return `<div class="text-sm text-base-content/70">Tool executed successfully</div>`;
    }

    // Delegate common methods to factory
    escapeHtml(text) {
        return RendererFactory.escapeHtml(text);
    }

    escapeUrl(url) {
        return RendererFactory.escapeUrl(url);
    }

    createCollapsible(title, content, collapsed = false, badge = null) {
        return RendererFactory.createCollapsible(title, content, collapsed, badge);
    }

    createCodeBlock(code, language = '', showLineNumbers = true, maxHeight = null) {
        return RendererFactory.createCodeBlock(code, language, showLineNumbers, maxHeight);
    }

    createCopyButton(content, className = 'btn-xs') {
        return RendererFactory.createCopyButton(content, className);
    }

    formatFileSize(bytes) {
        return RendererFactory.formatFileSize(bytes);
    }

    formatTimestamp(timestamp) {
        return RendererFactory.formatTimestamp(timestamp);
    }

    formatDuration(duration) {
        return RendererFactory.formatDuration(duration);
    }

    formatMarkdown(text) {
        return RendererFactory.formatMarkdown(text);
    }

    detectLanguageFromPath(filePath) {
        return RendererFactory.detectLanguageFromPath(filePath);
    }

    getFileIcon(path) {
        return RendererFactory.getFileIcon(path);
    }

    // Safe metadata access
    getMetadata(toolResult, ...paths) {
        let value = toolResult.metadata;
        for (const path of paths) {
            if (!value) return null;
            value = value[path];
        }
        return value;
    }

    // Check multiple metadata paths
    getMetadataAny(toolResult, paths) {
        for (const path of paths) {
            const value = this.getMetadata(toolResult, ...path.split('.'));
            if (value !== null && value !== undefined) return value;
        }
        return null;
    }
}

// File Read Renderer
class FileReadRenderer extends BaseRenderer {
    renderSuccess(toolResult) {
        const meta = toolResult.metadata;
        if (!meta) return super.renderSuccess(toolResult);

        const language = meta.language || this.detectLanguageFromPath(meta.filePath);
        const fileContent = (meta.lines || []).join('\n');
        const startLine = meta.offset || 1;
        
        // Create line-numbered code
        const lines = meta.lines || [];
        const codeWithLineNumbers = lines.map((line, index) => {
            const lineNumber = (startLine + index).toString().padStart(4, ' ');
            return `<span class="line-number text-base-content/50">${lineNumber}</span><span class="line-content">${this.escapeHtml(line)}</span>`;
        }).join('\n');

        const badges = [];
        if (meta.truncated) badges.push({ text: 'Truncated', class: 'badge-warning' });

        const metadata = [
            { label: 'Path', value: meta.filePath },
            startLine > 1 ? { label: 'Starting at line', value: startLine } : null,
            lines.length > 0 ? { label: 'Lines', value: lines.length } : null,
            language ? { label: 'Language', value: language } : null
        ].filter(Boolean);

        return RendererFactory.createCard({
            title: '📄 File Read',
            badge: badges[0],
            actions: this.createCopyButton(fileContent),
            content: `
                <div class="text-xs text-base-content/60 mb-3 font-mono">
                    <div class="flex items-center gap-4 flex-wrap">
                        ${metadata.map(item => 
                            `<span><strong>${item.label}:</strong> ${this.escapeHtml(item.value)}</span>`
                        ).join('')}
                    </div>
                </div>
                
                <div class="mockup-code bg-base-300 text-sm max-h-96 overflow-y-auto">
                    <pre><code class="language-${language}">${codeWithLineNumbers}</code></pre>
                </div>
            `
        });
    }
}

// File Write Renderer
class FileWriteRenderer extends BaseRenderer {
    renderSuccess(toolResult) {
        const meta = toolResult.metadata;
        if (!meta) return super.renderSuccess(toolResult);

        const language = meta.language || this.detectLanguageFromPath(meta.filePath);
        const sizeText = meta.size ? this.formatFileSize(meta.size) : '';

        const metadata = [
            { label: 'Path', value: meta.filePath },
            sizeText ? { label: 'Size', value: sizeText } : null,
            language ? { label: 'Language', value: language } : null
        ].filter(Boolean);

        return RendererFactory.createCard({
            title: '📝 File Written',
            badge: { text: 'Success', class: 'badge-success' },
            actions: meta.content ? this.createCopyButton(meta.content) : '',
            content: `
                <div class="text-xs text-base-content/60 mb-3 font-mono">
                    <div class="flex items-center gap-4">
                        ${metadata.map(item => 
                            `<span><strong>${item.label}:</strong> ${this.escapeHtml(item.value)}</span>`
                        ).join('')}
                    </div>
                </div>
                
                ${meta.content ? this.createCollapsible(
                    'View Content', 
                    this.createCodeBlock(meta.content, language, true, 300),
                    true
                ) : ''}
            `
        });
    }
}

// File Edit Renderer
class FileEditRenderer extends BaseRenderer {
    renderSuccess(toolResult) {
        const meta = toolResult.metadata;
        if (!meta) return super.renderSuccess(toolResult);

        const language = meta.language || this.detectLanguageFromPath(meta.filePath);
        const edits = meta.edits || [];
        
        return RendererFactory.createCard({
            title: '✏️ File Edit',
            badge: { text: `${edits.length} edit${edits.length !== 1 ? 's' : ''}`, class: 'badge-info' },
            content: `
                <div class="text-xs text-base-content/60 mb-3 font-mono">
                    <span><strong>Path:</strong> ${this.escapeHtml(meta.filePath)}</span>
                </div>
                
                ${edits.length > 0 ? this.createCollapsible(
                    'View Changes',
                    this.renderEdits(edits, language),
                    false,
                    { text: `${edits.length} changes`, class: 'badge-info' }
                ) : ''}
            `
        });
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

// File Multi Edit Renderer
class FileMultiEditRenderer extends BaseRenderer {
    renderSuccess(toolResult) {
        const meta = toolResult.metadata;
        if (!meta) return super.renderSuccess(toolResult);

        const replacements = meta.occurrence || meta.replacements || 0;
        
        return RendererFactory.createCard({
            title: '🔄 File Multi Edit',
            badge: { text: `${replacements} replacements`, class: 'badge-info' },
            content: `
                <div class="text-xs text-base-content/60 font-mono">
                    <div class="flex items-center gap-4">
                        <span><strong>Path:</strong> ${this.escapeHtml(meta.filePath)}</span>
                        <span><strong>Pattern:</strong> ${this.escapeHtml(meta.oldText || 'N/A')}</span>
                    </div>
                </div>
            `
        });
    }
}

// Bash Command Renderer
class BashRenderer extends BaseRenderer {
    renderSuccess(toolResult) {
        const meta = toolResult.metadata;
        if (!meta) return super.renderSuccess(toolResult);

        const isBackground = meta.pid !== undefined;
        const hasOutput = meta.output && meta.output.trim();
        const exitCode = meta.exitCode || 0;
        const isSuccess = exitCode === 0;
        
        if (isBackground) {
            return this.renderBackgroundProcess(meta);
        }

        const metadata = [
            { label: 'Command', value: meta.command },
            meta.workingDir ? { label: 'Directory', value: meta.workingDir } : null,
            meta.executionTime ? { label: 'Duration', value: this.formatDuration(meta.executionTime) } : null
        ].filter(Boolean);
        
        return RendererFactory.createCard({
            title: '🖥️ Command Execution',
            badge: { 
                text: `Exit Code: ${exitCode}`, 
                class: isSuccess ? 'badge-success' : 'badge-error' 
            },
            actions: hasOutput ? this.createCopyButton(meta.output) : '',
            content: `
                <div class="text-xs text-base-content/60 mb-3 font-mono">
                    <div class="flex items-center gap-4 flex-wrap">
                        ${metadata.map(item => 
                            `<span><strong>${item.label}:</strong> ${this.escapeHtml(item.value)}</span>`
                        ).join('')}
                    </div>
                </div>
                
                ${hasOutput ? this.createCollapsible(
                    'Command Output',
                    this.createTerminalOutput(meta.output),
                    false,
                    { text: 'View Output', class: 'badge-info' }
                ) : '<div class="text-sm text-base-content/60">No output</div>'}
            `
        });
    }

    renderBackgroundProcess(meta) {
        const metadata = [
            { label: 'Command', value: meta.command },
            { label: 'Log File', value: meta.logPath || meta.logFile || 'N/A' },
            meta.startTime ? { label: 'Started', value: this.formatTimestamp(meta.startTime) } : null
        ].filter(Boolean);

        return RendererFactory.createCard({
            title: '🔄 Background Process',
            badge: { text: `PID: ${meta.pid}`, class: 'badge-info' },
            content: `
                <div class="text-xs text-base-content/60 font-mono">
                    <div class="space-y-1">
                        ${metadata.map(item => 
                            `<div><strong>${item.label}:</strong> ${this.escapeHtml(item.value)}</div>`
                        ).join('')}
                    </div>
                </div>
            `
        });
    }

    createTerminalOutput(output) {
        // Convert ANSI codes to HTML (basic implementation)
        const ansiToHtml = (text) => {
            return text
                .replace(/\x1b\[31m/g, '<span class="text-red-500">')    // Red
                .replace(/\x1b\[32m/g, '<span class="text-green-500">')  // Green
                .replace(/\x1b\[33m/g, '<span class="text-yellow-500">') // Yellow
                .replace(/\x1b\[34m/g, '<span class="text-blue-500">')   // Blue
                .replace(/\x1b\[35m/g, '<span class="text-purple-500">') // Magenta
                .replace(/\x1b\[36m/g, '<span class="text-cyan-500">')   // Cyan
                .replace(/\x1b\[37m/g, '<span class="text-gray-500">')   // White
                .replace(/\x1b\[0m/g, '</span>')                        // Reset
                .replace(/\x1b\[\d+m/g, '');                            // Remove other codes
        };

        return `
            <div class="mockup-code bg-gray-900 text-green-400 text-sm max-h-96 overflow-y-auto">
                <pre><code>${ansiToHtml(this.escapeHtml(output))}</code></pre>
            </div>
        `;
    }
}

// Grep Search Results Renderer
class GrepRenderer extends BaseRenderer {
    renderSuccess(toolResult) {
        const meta = toolResult.metadata;
        if (!meta) return super.renderSuccess(toolResult);

        const results = meta.results || [];
        const totalMatches = results.reduce((sum, result) => sum + (result.matches ? result.matches.length : 1), 0);

        const badges = [];
        badges.push({ text: `${totalMatches} matches in ${results.length} files`, class: 'badge-info' });
        if (meta.truncated) badges.push({ text: 'Truncated', class: 'badge-warning' });

        const metadata = [
            { label: 'Pattern', value: meta.pattern },
            meta.path ? { label: 'Path', value: meta.path } : null,
            meta.include ? { label: 'Include', value: meta.include } : null
        ].filter(Boolean);
        
        return RendererFactory.createCard({
            title: '🔍 Search Results',
            badge: badges[0],
            content: `
                <div class="text-xs text-base-content/60 mb-3 font-mono">
                    <div class="flex items-center gap-4 flex-wrap">
                        ${metadata.map(item => 
                            `<span><strong>${item.label}:</strong> ${this.escapeHtml(item.value)}</span>`
                        ).join('')}
                    </div>
                </div>
                
                ${results.length > 0 ? this.renderSearchResults(results, meta.pattern) : 
                    '<div class="text-sm text-base-content/60">No matches found</div>'}
            `
        });
    }

    renderSearchResults(results, pattern) {
        const fileGroups = this.groupResultsByFile(results);
        
        return Object.entries(fileGroups).map(([file, matches]) => {
            const matchCount = matches.length;
            const fileContent = matches.map(match => {
                const highlightedContent = this.highlightPattern(match.content || match.line, pattern);
                return `
                    <div class="flex items-start gap-2 py-1 hover:bg-base-100 rounded px-2">
                        <span class="text-xs text-base-content/50 font-mono min-w-[3rem]">${match.lineNumber || match.line_number || '?'}:</span>
                        <span class="text-sm font-mono flex-1">${highlightedContent}</span>
                    </div>
                `;
            }).join('');

            return this.createCollapsible(
                `📄 ${file}`,
                fileContent,
                matchCount > 5, // Collapse if more than 5 matches
                { text: `${matchCount} matches`, class: 'badge-info' }
            );
        }).join('');
    }

    groupResultsByFile(results) {
        const grouped = {};
        results.forEach(result => {
            const file = result.file || result.filename || 'Unknown';
            if (!grouped[file]) {
                grouped[file] = [];
            }
            
            if (result.matches) {
                // Multiple matches per file
                grouped[file].push(...result.matches);
            } else {
                // Single match
                grouped[file].push(result);
            }
        });
        return grouped;
    }

    highlightPattern(text, pattern) {
        if (!pattern || !text) return this.escapeHtml(text);
        
        try {
            const escaped = this.escapeHtml(text);
            const regex = new RegExp(`(${pattern.replace(/[.*+?^${}()|[\]\\]/g, '\\$&')})`, 'gi');
            return escaped.replace(regex, '<mark class="bg-yellow-200 text-black">$1</mark>');
        } catch (e) {
            return this.escapeHtml(text);
        }
    }
}

// Glob File Listing Renderer
class GlobRenderer extends BaseRenderer {
    renderSuccess(toolResult) {
        const meta = toolResult.metadata;
        if (!meta) return super.renderSuccess(toolResult);

        const files = meta.files || [];
        const totalSize = files.reduce((sum, file) => sum + (file.size || 0), 0);

        const badges = [];
        badges.push({ text: `${files.length} files`, class: 'badge-info' });
        if (meta.truncated) badges.push({ text: 'Truncated', class: 'badge-warning' });

        const metadata = [
            { label: 'Pattern', value: meta.pattern },
            meta.path ? { label: 'Path', value: meta.path } : null,
            totalSize > 0 ? { label: 'Total Size', value: this.formatFileSize(totalSize) } : null
        ].filter(Boolean);
        
        return RendererFactory.createCard({
            title: '📁 File Listing',
            badge: badges[0],
            content: `
                <div class="text-xs text-base-content/60 mb-3 font-mono">
                    <div class="flex items-center gap-4 flex-wrap">
                        ${metadata.map(item => 
                            `<span><strong>${item.label}:</strong> ${this.escapeHtml(item.value)}</span>`
                        ).join('')}
                    </div>
                </div>
                
                ${files.length > 0 ? this.renderFileList(files) : 
                    '<div class="text-sm text-base-content/60">No files found</div>'}
            `
        });
    }

    renderFileList(files) {
        const fileContent = files.map(file => {
            const icon = this.getFileIcon(file.path || file.name);
            const sizeText = file.size ? this.formatFileSize(file.size) : '';
            const modTime = file.modTime || file.modified ? 
                new Date(file.modTime || file.modified).toLocaleDateString() : '';

            return `
                <div class="flex items-center justify-between py-2 hover:bg-base-100 rounded px-2">
                    <div class="flex items-center gap-2">
                        <span class="text-lg" aria-hidden="true">${icon}</span>
                        <span class="font-mono text-sm">${this.escapeHtml(file.path || file.name)}</span>
                    </div>
                    <div class="flex items-center gap-4 text-xs text-base-content/60">
                        ${sizeText ? `<span>${sizeText}</span>` : ''}
                        ${modTime ? `<span>${modTime}</span>` : ''}
                    </div>
                </div>
            `;
        }).join('');

        return this.createCollapsible(
            'Files',
            fileContent,
            files.length > 10,
            { text: `${files.length} files`, class: 'badge-info' }
        );
    }
}

// Thinking Process Renderer
class ThinkingRenderer extends BaseRenderer {
    renderSuccess(toolResult) {
        const meta = toolResult.metadata;
        if (!meta) return super.renderSuccess(toolResult);

        return `
            <div class="card bg-blue-50 border border-blue-200">
                <div class="card-body">
                    <div class="flex items-center gap-2 mb-3">
                        <h4 class="font-semibold text-blue-700">🧠 Thinking</h4>
                        <div class="badge badge-info badge-sm">Internal Process</div>
                    </div>
                    
                    <div class="bg-white p-4 rounded-lg border border-blue-100">
                        <div class="text-sm text-gray-700 italic leading-relaxed">
                            ${this.formatThoughtContent(meta.thought)}
                        </div>
                    </div>
                </div>
            </div>
        `;
    }

    formatThoughtContent(thought) {
        if (!thought) return '';
        
        // Use markdown parser if available
        return this.formatMarkdown(thought);
    }
}

// Web Fetch Renderer
class WebFetchRenderer extends BaseRenderer {
    renderSuccess(toolResult) {
        const meta = toolResult.metadata;
        if (!meta || !meta.url) return super.renderSuccess(toolResult);

        const hasPrompt = meta.prompt && meta.prompt.trim();
        const savedPath = meta.savedPath || meta.filePath;
        const safeUrl = this.escapeUrl(meta.url);

        const metadata = [
            { label: 'URL', value: meta.url },
            meta.contentType ? { label: 'Content Type', value: meta.contentType } : null,
            savedPath ? { label: 'Saved to', value: savedPath } : null,
            hasPrompt ? { label: 'Extraction Prompt', value: meta.prompt } : null
        ].filter(Boolean);
        
        return RendererFactory.createCard({
            title: '🌐 Web Fetch',
            badge: { text: 'Success', class: 'badge-success' },
            actions: safeUrl !== '#' ? `
                <a href="${safeUrl}" target="_blank" rel="noopener noreferrer" class="btn btn-ghost btn-xs" aria-label="Open URL in new tab">
                    <svg xmlns="http://www.w3.org/2000/svg" class="h-4 w-4" fill="none" viewBox="0 0 24 24" stroke="currentColor" aria-hidden="true">
                        <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M10 6H6a2 2 0 00-2 2v10a2 2 0 002 2h10a2 2 0 002-2v-4M14 4h6m0 0v6m0-6L10 14" />
                    </svg>
                </a>
            ` : '',
            content: `
                <div class="text-xs text-base-content/60 mb-3 font-mono">
                    <div class="space-y-1">
                        ${metadata.map(item => 
                            `<div><strong>${item.label}:</strong> ${this.escapeHtml(item.value)}</div>`
                        ).join('')}
                    </div>
                </div>
                
                ${meta.content ? this.createCollapsible(
                    'Fetched Content',
                    this.renderWebContent(meta),
                    true,
                    { text: 'View Content', class: 'badge-info' }
                ) : ''}
            `
        });
    }

    renderWebContent(meta) {
        if (meta.contentType && meta.contentType.includes('image')) {
            const safeUrl = this.escapeUrl(meta.url);
            return safeUrl !== '#' ? 
                `<img src="${safeUrl}" alt="Fetched image" class="max-w-full h-auto rounded">` :
                '<div class="text-sm text-base-content/60">Invalid image URL</div>';
        }
        
        if (meta.content) {
            const language = this.detectContentLanguage(meta.contentType);
            return this.createCodeBlock(meta.content, language, true, 400);
        }
        
        return '<div class="text-sm text-base-content/60">Content preview not available</div>';
    }

    detectContentLanguage(contentType) {
        if (!contentType) return '';
        if (contentType.includes('json')) return 'json';
        if (contentType.includes('xml')) return 'xml';
        if (contentType.includes('html')) return 'html';
        if (contentType.includes('css')) return 'css';
        if (contentType.includes('javascript')) return 'javascript';
        return '';
    }
}

// Todo Management Renderer
class TodoRenderer extends BaseRenderer {
    renderSuccess(toolResult) {
        const meta = toolResult.metadata;
        if (!meta) return super.renderSuccess(toolResult);

        const action = meta.action || 'updated';
        const todos = meta.todos || meta.todoList || [];
        
        return RendererFactory.createCard({
            title: '📋 Todo List',
            badge: { text: action, class: 'badge-info' },
            content: todos.length > 0 ? this.renderTodoList(todos) : 
                '<div class="text-sm text-base-content/60">No todos available</div>'
        });
    }

    renderTodoList(todos) {
        const todoContent = todos.map(todo => {
            const statusIcon = this.getTodoStatusIcon(todo.status);
            const priorityClass = this.getPriorityClass(todo.priority);
            const isCompleted = todo.status === 'completed';
            
            return `
                <div class="flex items-start gap-3 p-2 hover:bg-base-100 rounded" role="listitem">
                    <span class="text-lg" aria-label="${todo.status}">${statusIcon}</span>
                    <div class="flex-1">
                        <div class="text-sm ${isCompleted ? 'line-through text-base-content/60' : ''}">${this.escapeHtml(todo.content)}</div>
                        <div class="flex items-center gap-2 mt-1">
                            <div class="badge badge-xs ${priorityClass}" aria-label="Priority: ${todo.priority}">${todo.priority}</div>
                            <div class="badge badge-xs badge-outline" aria-label="Status: ${todo.status}">${todo.status}</div>
                        </div>
                    </div>
                </div>
            `;
        }).join('');

        return this.createCollapsible('Todo Items', 
            `<div role="list">${todoContent}</div>`, 
            false, 
            { text: `${todos.length} items`, class: 'badge-info' }
        );
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
        if (!meta) return super.renderSuccess(toolResult);

        const modelStrength = meta.modelStrength || meta.model_strength || 'unknown';
        
        return RendererFactory.createCard({
            title: '🤖 Sub-agent',
            badge: { text: `${modelStrength} model`, class: 'badge-info' },
            content: `
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
            `
        });
    }
}

// Batch Operation Renderer
class BatchRenderer extends BaseRenderer {
    renderSuccess(toolResult) {
        const meta = toolResult.metadata;
        if (!meta) return super.renderSuccess(toolResult);

        const description = meta.description || 'Batch operation';
        const subResults = meta.subResults || meta.results || [];
        const successCount = meta.successCount || subResults.filter(r => r.success).length;
        const failureCount = meta.failureCount || subResults.filter(r => !r.success).length;
        
        return RendererFactory.createCard({
            title: '📦 Batch Operation',
            badge: { text: description, class: 'badge-info' },
            content: `
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
            `
        });
    }

    renderSubResults(subResults) {
        return subResults.map((result, index) => {
            const statusIcon = result.success ? '✅' : '❌';
            const statusClass = result.success ? 'text-green-600' : 'text-red-600';
            
            return `
                <div class="border rounded p-3 mb-2">
                    <div class="flex items-center gap-2 mb-2">
                        <span class="${statusClass}" aria-label="${result.success ? 'Success' : 'Failed'}">${statusIcon}</span>
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
        if (!meta) return super.renderSuccess(toolResult);

        const imagePath = this.getMetadataAny(toolResult, ['imagePath', 'image_path', 'path']);
        const analysis = meta.analysis || meta.result;

        const metadata = [
            imagePath ? { label: 'Image', value: imagePath } : null,
            meta.prompt ? { label: 'Prompt', value: meta.prompt } : null
        ].filter(Boolean);
        
        return RendererFactory.createCard({
            title: '👁️ Image Recognition',
            badge: { text: 'Analyzed', class: 'badge-success' },
            content: `
                <div class="text-xs text-base-content/60 mb-3 font-mono">
                    ${metadata.map(item => 
                        `<div><strong>${item.label}:</strong> ${this.escapeHtml(item.value)}</div>`
                    ).join('')}
                </div>
                
                ${analysis ? `
                    <div class="bg-base-100 p-3 rounded">
                        <div class="text-sm">${this.escapeHtml(analysis)}</div>
                    </div>
                ` : ''}
            `
        });
    }
}

// Browser Navigation Renderer
class BrowserNavigateRenderer extends BaseRenderer {
    renderSuccess(toolResult) {
        const meta = toolResult.metadata;
        if (!meta || !meta.url) return super.renderSuccess(toolResult);

        const url = meta.url;
        const title = meta.title || meta.pageTitle;
        const safeUrl = this.escapeUrl(url);

        const metadata = [
            { label: 'URL', value: url },
            title ? { label: 'Title', value: title } : null
        ].filter(Boolean);
        
        return RendererFactory.createCard({
            title: '🌐 Browser Navigation',
            badge: { text: 'Success', class: 'badge-success' },
            actions: safeUrl !== '#' ? `
                <a href="${safeUrl}" target="_blank" rel="noopener noreferrer" class="btn btn-ghost btn-xs" aria-label="Open URL in new tab">
                    <svg xmlns="http://www.w3.org/2000/svg" class="h-4 w-4" fill="none" viewBox="0 0 24 24" stroke="currentColor" aria-hidden="true">
                        <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M10 6H6a2 2 0 00-2 2v10a2 2 0 002 2h10a2 2 0 002-2v-4M14 4h6m0 0v6m0-6L10 14" />
                    </svg>
                </a>
            ` : '',
            content: `
                <div class="text-xs text-base-content/60 font-mono">
                    <div class="space-y-1">
                        ${metadata.map(item => 
                            `<div><strong>${item.label}:</strong> ${this.escapeHtml(item.value)}</div>`
                        ).join('')}
                    </div>
                </div>
            `
        });
    }
}

// Browser Screenshot Renderer
class BrowserScreenshotRenderer extends BaseRenderer {
    renderSuccess(toolResult) {
        const meta = toolResult.metadata;
        if (!meta) return super.renderSuccess(toolResult);

        const filePath = this.getMetadataAny(toolResult, ['filePath', 'file_path', 'path']);
        const dimensions = meta.dimensions || meta.size;

        const metadata = [
            filePath ? { label: 'File', value: filePath } : null,
            dimensions ? { label: 'Dimensions', value: dimensions } : null
        ].filter(Boolean);
        
        return RendererFactory.createCard({
            title: '📸 Browser Screenshot',
            badge: { text: 'Captured', class: 'badge-success' },
            content: `
                <div class="text-xs text-base-content/60 mb-3 font-mono">
                    <div class="space-y-1">
                        ${metadata.map(item => 
                            `<div><strong>${item.label}:</strong> ${this.escapeHtml(item.value)}</div>`
                        ).join('')}
                    </div>
                </div>
                
                ${filePath && this.isImageFile(filePath) ? `
                    <div class="mt-3">
                        <img src="${this.escapeUrl('file://' + filePath)}" alt="Screenshot" 
                             class="max-w-full h-auto rounded border" 
                             onerror="this.style.display='none'; this.nextElementSibling.style.display='block';">
                        <div style="display:none" class="text-sm text-base-content/60">Unable to load screenshot</div>
                    </div>
                ` : ''}
            `
        });
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
        if (!meta) return super.renderSuccess(toolResult);

        const processes = meta.processes || [];
        const processCount = meta.processCount || processes.length;
        
        return RendererFactory.createCard({
            title: '⚙️ Background Processes',
            badge: { text: `${processCount} processes`, class: 'badge-info' },
            content: processes.length > 0 ? this.renderProcessList(processes) : 
                '<div class="text-sm text-base-content/60">No background processes</div>'
        });
    }

    renderProcessList(processes) {
        const processContent = processes.map(process => {
            const statusIcon = process.status === 'running' ? '🟢' : '🔴';
            const statusClass = process.status === 'running' ? 'text-green-600' : 'text-red-600';
            
            return `
                <div class="flex items-center justify-between p-2 hover:bg-base-100 rounded">
                    <div class="flex items-center gap-3">
                        <span aria-label="${process.status}">${statusIcon}</span>
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

// MCP Tool Renderers (kept simple as requested)
class MCPDefinitionRenderer extends BaseRenderer {
    renderSuccess(toolResult) {
        const meta = toolResult.metadata;
        if (!meta) return super.renderSuccess(toolResult);

        return RendererFactory.createCard({
            title: '🔍 Code Definition',
            badge: { text: 'MCP', class: 'badge-info' },
            content: '<div class="text-sm text-base-content/60">Code definition retrieved</div>'
        });
    }
}

class MCPReferencesRenderer extends BaseRenderer {
    renderSuccess(toolResult) {
        const meta = toolResult.metadata;
        if (!meta) return super.renderSuccess(toolResult);

        return RendererFactory.createCard({
            title: '📚 Code References',
            badge: { text: 'MCP', class: 'badge-info' },
            content: '<div class="text-sm text-base-content/60">Code references found</div>'
        });
    }
}

class MCPHoverRenderer extends BaseRenderer {
    renderSuccess(toolResult) {
        const meta = toolResult.metadata;
        if (!meta) return super.renderSuccess(toolResult);

        return RendererFactory.createCard({
            title: '💡 Code Hover Info',
            badge: { text: 'MCP', class: 'badge-info' },
            content: '<div class="text-sm text-base-content/60">Hover information retrieved</div>'
        });
    }
}

class MCPDiagnosticsRenderer extends BaseRenderer {
    renderSuccess(toolResult) {
        const meta = toolResult.metadata;
        if (!meta) return super.renderSuccess(toolResult);

        return RendererFactory.createCard({
            title: '🔍 Code Diagnostics',
            badge: { text: 'MCP', class: 'badge-info' },
            content: '<div class="text-sm text-base-content/60">Diagnostics retrieved</div>'
        });
    }
}

// Initialize the registry
const advancedToolRendererRegistry = new ToolRendererRegistry();