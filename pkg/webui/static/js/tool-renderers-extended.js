// Extended Tool Result Renderers - Part 2

// File Multi Edit Renderer
class FileMultiEditRenderer extends BaseRenderer {
    renderSuccess(toolResult) {
        const meta = toolResult.metadata;
        if (!meta) return this.renderFallback(toolResult);

        const replacements = meta.occurrence || meta.replacements || 0;
        
        return `
            <div class="card bg-base-200 border">
                <div class="card-body">
                    <div class="flex items-center justify-between mb-3">
                        <div class="flex items-center gap-2">
                            <h4 class="font-semibold">🔄 File Multi Edit</h4>
                            <div class="badge badge-info badge-sm">${replacements} replacements</div>
                        </div>
                    </div>
                    
                    <div class="text-xs text-base-content/60 font-mono">
                        <div class="flex items-center gap-4">
                            <span><strong>Path:</strong> ${this.escapeHtml(meta.filePath)}</span>
                            <span><strong>Pattern:</strong> ${this.escapeHtml(meta.oldText || 'N/A')}</span>
                        </div>
                    </div>
                </div>
            </div>
        `;
    }
}

// Bash Command Renderer
class BashRenderer extends BaseRenderer {
    renderSuccess(toolResult) {
        const meta = toolResult.metadata;
        if (!meta) return this.renderFallback(toolResult);

        const isBackground = meta.pid !== undefined;
        const hasOutput = meta.output && meta.output.trim();
        const exitCode = meta.exitCode || 0;
        const isSuccess = exitCode === 0;
        
        if (isBackground) {
            return this.renderBackgroundProcess(meta);
        }
        
        return `
            <div class="card bg-base-200 border">
                <div class="card-body">
                    <div class="flex items-center justify-between mb-3">
                        <div class="flex items-center gap-2">
                            <h4 class="font-semibold">🖥️ Command Execution</h4>
                            <div class="badge badge-sm ${isSuccess ? 'badge-success' : 'badge-error'}">
                                Exit Code: ${exitCode}
                            </div>
                        </div>
                        ${hasOutput ? this.createCopyButton(meta.output) : ''}
                    </div>
                    
                    <div class="text-xs text-base-content/60 mb-3 font-mono">
                        <div class="flex items-center gap-4 flex-wrap">
                            <span><strong>Command:</strong> ${this.escapeHtml(meta.command)}</span>
                            ${meta.workingDir ? `<span><strong>Directory:</strong> ${this.escapeHtml(meta.workingDir)}</span>` : ''}
                            ${meta.executionTime ? `<span><strong>Duration:</strong> ${this.formatDuration(meta.executionTime)}</span>` : ''}
                        </div>
                    </div>
                    
                    ${hasOutput ? this.createCollapsible(
                        'Command Output',
                        this.createTerminalOutput(meta.output),
                        false,
                        { text: 'View Output', class: 'badge-info' }
                    ) : '<div class="text-sm text-base-content/60">No output</div>'}
                </div>
            </div>
        `;
    }

    renderBackgroundProcess(meta) {
        return `
            <div class="card bg-base-200 border">
                <div class="card-body">
                    <div class="flex items-center justify-between mb-3">
                        <div class="flex items-center gap-2">
                            <h4 class="font-semibold">🔄 Background Process</h4>
                            <div class="badge badge-info badge-sm">PID: ${meta.pid}</div>
                        </div>
                    </div>
                    
                    <div class="text-xs text-base-content/60 font-mono">
                        <div class="space-y-1">
                            <div><strong>Command:</strong> ${this.escapeHtml(meta.command)}</div>
                            <div><strong>Log File:</strong> ${this.escapeHtml(meta.logPath || meta.logFile || 'N/A')}</div>
                            ${meta.startTime ? `<div><strong>Started:</strong> ${this.formatTimestamp(meta.startTime)}</div>` : ''}
                        </div>
                    </div>
                </div>
            </div>
        `;
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

    formatDuration(duration) {
        if (typeof duration === 'string') {
            return duration;
        }
        // If it's in nanoseconds, convert to seconds
        if (duration > 1000000000) {
            return `${(duration / 1000000000).toFixed(3)}s`;
        }
        return `${duration}ms`;
    }
}

// Grep Search Results Renderer
class GrepRenderer extends BaseRenderer {
    renderSuccess(toolResult) {
        const meta = toolResult.metadata;
        if (!meta) return this.renderFallback(toolResult);

        const results = meta.results || [];
        const totalMatches = results.reduce((sum, result) => sum + (result.matches ? result.matches.length : 1), 0);
        
        return `
            <div class="card bg-base-200 border">
                <div class="card-body">
                    <div class="flex items-center justify-between mb-3">
                        <div class="flex items-center gap-2">
                            <h4 class="font-semibold">🔍 Search Results</h4>
                            <div class="badge badge-info badge-sm">${totalMatches} matches in ${results.length} files</div>
                            ${meta.truncated ? '<div class="badge badge-warning badge-sm">Truncated</div>' : ''}
                        </div>
                    </div>
                    
                    <div class="text-xs text-base-content/60 mb-3 font-mono">
                        <div class="flex items-center gap-4 flex-wrap">
                            <span><strong>Pattern:</strong> ${this.escapeHtml(meta.pattern)}</span>
                            ${meta.path ? `<span><strong>Path:</strong> ${this.escapeHtml(meta.path)}</span>` : ''}
                            ${meta.include ? `<span><strong>Include:</strong> ${this.escapeHtml(meta.include)}</span>` : ''}
                        </div>
                    </div>
                    
                    ${results.length > 0 ? this.renderSearchResults(results, meta.pattern) : 
                        '<div class="text-sm text-base-content/60">No matches found</div>'}
                </div>
            </div>
        `;
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
        if (!meta) return this.renderFallback(toolResult);

        const files = meta.files || [];
        const totalSize = files.reduce((sum, file) => sum + (file.size || 0), 0);
        
        return `
            <div class="card bg-base-200 border">
                <div class="card-body">
                    <div class="flex items-center justify-between mb-3">
                        <div class="flex items-center gap-2">
                            <h4 class="font-semibold">📁 File Listing</h4>
                            <div class="badge badge-info badge-sm">${files.length} files</div>
                            ${meta.truncated ? '<div class="badge badge-warning badge-sm">Truncated</div>' : ''}
                        </div>
                    </div>
                    
                    <div class="text-xs text-base-content/60 mb-3 font-mono">
                        <div class="flex items-center gap-4 flex-wrap">
                            <span><strong>Pattern:</strong> ${this.escapeHtml(meta.pattern)}</span>
                            ${meta.path ? `<span><strong>Path:</strong> ${this.escapeHtml(meta.path)}</span>` : ''}
                            ${totalSize > 0 ? `<span><strong>Total Size:</strong> ${this.formatFileSize(totalSize)}</span>` : ''}
                        </div>
                    </div>
                    
                    ${files.length > 0 ? this.renderFileList(files) : 
                        '<div class="text-sm text-base-content/60">No files found</div>'}
                </div>
            </div>
        `;
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
                        <span class="text-lg">${icon}</span>
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

    getFileIcon(path) {
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

// Thinking Process Renderer
class ThinkingRenderer extends BaseRenderer {
    renderSuccess(toolResult) {
        const meta = toolResult.metadata;
        if (!meta) return this.renderFallback(toolResult);

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
        
        // Basic markdown-like formatting for thought content
        return this.escapeHtml(thought)
            .replace(/\n\n/g, '</p><p class="mt-2">')
            .replace(/\n/g, '<br>')
            .replace(/^/, '<p>')
            .replace(/$/, '</p>');
    }
}

// Web Fetch Renderer
class WebFetchRenderer extends BaseRenderer {
    renderSuccess(toolResult) {
        const meta = toolResult.metadata;
        if (!meta) return this.renderFallback(toolResult);

        const hasPrompt = meta.prompt && meta.prompt.trim();
        const savedPath = meta.savedPath || meta.filePath;
        
        return `
            <div class="card bg-base-200 border">
                <div class="card-body">
                    <div class="flex items-center justify-between mb-3">
                        <div class="flex items-center gap-2">
                            <h4 class="font-semibold">🌐 Web Fetch</h4>
                            <div class="badge badge-success badge-sm">Success</div>
                        </div>
                        <a href="${meta.url}" target="_blank" rel="noopener" class="btn btn-ghost btn-xs">
                            <svg xmlns="http://www.w3.org/2000/svg" class="h-4 w-4" fill="none" viewBox="0 0 24 24" stroke="currentColor">
                                <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M10 6H6a2 2 0 00-2 2v10a2 2 0 002 2h10a2 2 0 002-2v-4M14 4h6m0 0v6m0-6L10 14" />
                            </svg>
                        </a>
                    </div>
                    
                    <div class="text-xs text-base-content/60 mb-3 font-mono">
                        <div class="space-y-1">
                            <div><strong>URL:</strong> ${this.escapeHtml(meta.url)}</div>
                            ${meta.contentType ? `<div><strong>Content Type:</strong> ${meta.contentType}</div>` : ''}
                            ${savedPath ? `<div><strong>Saved to:</strong> ${this.escapeHtml(savedPath)}</div>` : ''}
                            ${hasPrompt ? `<div><strong>Extraction Prompt:</strong> ${this.escapeHtml(meta.prompt)}</div>` : ''}
                        </div>
                    </div>
                    
                    ${meta.content ? this.createCollapsible(
                        'Fetched Content',
                        this.renderWebContent(meta),
                        true,
                        { text: 'View Content', class: 'badge-info' }
                    ) : ''}
                </div>
            </div>
        `;
    }

    renderWebContent(meta) {
        if (meta.contentType && meta.contentType.includes('image')) {
            return `<img src="${meta.url}" alt="Fetched image" class="max-w-full h-auto rounded">`;
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

// Initialize the updated registry
const advancedToolRendererRegistry = new ToolRendererRegistry();