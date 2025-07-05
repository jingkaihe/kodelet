// Component definitions to fix console errors
// This file defines the exposed components that are referenced but not found

// Create the exposed namespace if it doesn't exist
window.exposed = window.exposed || {};

// Markdown component - handles markdown rendering
window.exposed.markdown = {
    render: function(content) {
        // Use the existing renderMarkdown function if available
        if (typeof renderMarkdown === 'function') {
            return renderMarkdown(content);
        }
        // Fallback to basic HTML escaping
        return content.replace(/&/g, '&amp;')
                     .replace(/</g, '&lt;')
                     .replace(/>/g, '&gt;')
                     .replace(/\n/g, '<br>');
    }
};

// ReaderToggle component - placeholder for file reader toggle functionality
window.exposed.ReaderToggle = {
    toggle: function(elementId) {
        const element = document.getElementById(elementId);
        if (element) {
            element.classList.toggle('hidden');
        }
    },
    init: function() {
        console.log('ReaderToggle initialized');
    }
};

// FileListPanel component - placeholder for file list panel functionality
window.exposed.FileListPanel = {
    files: [],
    
    render: function(files) {
        this.files = files || [];
        return this.files.map(file => `
            <div class="file-item">
                <span class="file-name">${file.name || file}</span>
            </div>
        `).join('');
    },
    
    init: function() {
        console.log('FileListPanel initialized');
    },
    
    update: function(files) {
        this.files = files || [];
        this.render(this.files);
    }
};

// Initialize components
document.addEventListener('DOMContentLoaded', function() {
    if (window.exposed.ReaderToggle) {
        window.exposed.ReaderToggle.init();
    }
    if (window.exposed.FileListPanel) {
        window.exposed.FileListPanel.init();
    }
});