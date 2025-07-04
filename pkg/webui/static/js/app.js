// Shared utility functions and Alpine.js data functions

// API helper functions
const apiCall = async (endpoint, options = {}) => {
    const response = await fetch(endpoint, {
        headers: {
            'Content-Type': 'application/json',
            ...options.headers
        },
        ...options
    });
    
    if (!response.ok) {
        const error = await response.json();
        throw new Error(error.error || `HTTP ${response.status}`);
    }
    
    return response.json();
};

// Date formatting utility
const formatDate = (dateString) => {
    if (!dateString) return 'N/A';
    
    const date = new Date(dateString);
    const now = new Date();
    const diff = now - date;
    
    // If less than a day, show relative time
    if (diff < 24 * 60 * 60 * 1000) {
        const hours = Math.floor(diff / (60 * 60 * 1000));
        const minutes = Math.floor((diff % (60 * 60 * 1000)) / (60 * 1000));
        
        if (hours > 0) {
            return `${hours}h ${minutes}m ago`;
        } else if (minutes > 0) {
            return `${minutes}m ago`;
        } else {
            return 'Just now';
        }
    }
    
    // Otherwise show formatted date
    return date.toLocaleDateString('en-US', {
        year: 'numeric',
        month: 'short',
        day: 'numeric',
        hour: '2-digit',
        minute: '2-digit'
    });
};

// Cost formatting utility
const formatCost = (usage) => {
    if (!usage) return '$0.00';
    
    const total = (usage.inputCost || 0) + (usage.outputCost || 0) + 
                  (usage.cacheCreationCost || 0) + (usage.cacheReadCost || 0);
    
    return new Intl.NumberFormat('en-US', {
        style: 'currency',
        currency: 'USD',
        minimumFractionDigits: 4
    }).format(total);
};

// Copy to clipboard utility
const copyToClipboard = async (text) => {
    try {
        await navigator.clipboard.writeText(text);
        // Show a temporary toast notification
        showToast('Copied to clipboard!', 'success');
    } catch (err) {
        console.error('Failed to copy:', err);
        showToast('Failed to copy to clipboard', 'error');
    }
};

// Toast notification utility
const showToast = (message, type = 'info') => {
    const toast = document.createElement('div');
    toast.className = `toast toast-top toast-end`;
    toast.innerHTML = `
        <div class="alert alert-${type === 'error' ? 'error' : type === 'success' ? 'success' : 'info'}">
            <span>${message}</span>
        </div>
    `;
    
    document.body.appendChild(toast);
    
    setTimeout(() => {
        toast.remove();
    }, 3000);
};

// Markdown rendering utility
const renderMarkdown = (text) => {
    if (typeof marked !== 'undefined') {
        return marked.parse(text);
    }
    // Fallback to simple HTML escaping and line breaks
    return text.replace(/&/g, '&amp;')
              .replace(/</g, '&lt;')
              .replace(/>/g, '&gt;')
              .replace(/\n/g, '<br>');
};

// Conversation List Alpine.js Component
function conversationListApp() {
    return {
        // State
        conversations: [],
        stats: null,
        loading: false,
        error: null,
        
        // Filters
        searchTerm: '',
        sortBy: 'updated',
        sortOrder: 'desc',
        limit: 25,
        offset: 0,
        hasMore: false,
        
        // Initialization
        init() {
            this.loadConversations();
            this.loadStatistics();
        },
        
        // Load conversations
        async loadConversations() {
            this.loading = true;
            this.error = null;
            
            try {
                const params = new URLSearchParams({
                    sortBy: this.sortBy,
                    sortOrder: this.sortOrder,
                    limit: this.limit,
                    offset: this.offset
                });
                
                if (this.searchTerm) {
                    params.append('search', this.searchTerm);
                }
                
                const response = await apiCall(`/api/conversations?${params}`);
                this.conversations = response.conversations || [];
                this.hasMore = response.hasMore || false;
            } catch (err) {
                this.error = err.message;
                console.error('Failed to load conversations:', err);
            } finally {
                this.loading = false;
            }
        },
        
        // Load statistics
        async loadStatistics() {
            try {
                this.stats = await apiCall('/api/stats');
            } catch (err) {
                console.error('Failed to load statistics:', err);
            }
        },
        
        // Search conversations
        search() {
            this.offset = 0;
            this.loadConversations();
        },
        
        // Clear filters
        clearFilters() {
            this.searchTerm = '';
            this.sortBy = 'updated';
            this.sortOrder = 'desc';
            this.limit = 25;
            this.offset = 0;
            this.loadConversations();
        },
        
        // Pagination
        nextPage() {
            if (this.hasMore) {
                this.offset += this.limit;
                this.loadConversations();
            }
        },
        
        previousPage() {
            if (this.offset > 0) {
                this.offset = Math.max(0, this.offset - this.limit);
                this.loadConversations();
            }
        },
        
        // Delete conversation
        async deleteConversation(conversationId) {
            if (!confirm('Are you sure you want to delete this conversation?')) {
                return;
            }
            
            try {
                await apiCall(`/api/conversations/${conversationId}`, {
                    method: 'DELETE'
                });
                
                // Remove from local state
                this.conversations = this.conversations.filter(c => c.id !== conversationId);
                showToast('Conversation deleted successfully', 'success');
            } catch (err) {
                showToast(`Failed to delete conversation: ${err.message}`, 'error');
            }
        },
        
        // Utility functions
        formatDate,
        formatDateRange() {
            if (!this.stats || !this.stats.oldestConversation || !this.stats.newestConversation) {
                return 'N/A';
            }
            
            const oldest = new Date(this.stats.oldestConversation).toLocaleDateString('en-US', {
                year: 'numeric',
                month: 'short'
            });
            const newest = new Date(this.stats.newestConversation).toLocaleDateString('en-US', {
                year: 'numeric',
                month: 'short'
            });
            
            return oldest === newest ? oldest : `${oldest} - ${newest}`;
        }
    };
}

// Make functions available globally
window.conversationListApp = conversationListApp;
window.formatDate = formatDate;
window.formatCost = formatCost;
window.copyToClipboard = copyToClipboard;
window.showToast = showToast;
window.renderMarkdown = renderMarkdown;
window.apiCall = apiCall;