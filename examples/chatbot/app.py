"""
Kodelet Chatbot - A streamlit-based web UI powered by kodelet

This application provides a chat interface that integrates with kodelet's
CLI and API to provide intelligent assistance with software engineering tasks.
"""

import streamlit as st
import atexit
from src.conversation_manager import ConversationManager
from src.ui_components import (
    render_sidebar,
    render_chat_interface,
    render_input_area,
    render_status_bar,
    render_settings_panel
)

# Configure streamlit page
st.set_page_config(
    page_title="Kodelet Chatbot",
    page_icon="🤖",
    layout="wide",
    initial_sidebar_state="expanded"
)

# Custom CSS for better UI
st.markdown("""
<style>
    .main .block-container {
        padding-top: 2rem;
        padding-bottom: 2rem;
    }
    
    .stChatMessage {
        margin-bottom: 1rem;
    }
    
    .stChatInputContainer {
        position: fixed;
        bottom: 0;
        background: white;
        padding: 1rem;
        width: 100%;
        z-index: 1000;
    }
    
    .conversation-item {
        margin-bottom: 0.5rem;
        padding: 0.5rem;
        border-radius: 0.5rem;
        border: 1px solid #e0e0e0;
    }
    
    .conversation-item:hover {
        background-color: #f5f5f5;
    }
    
    div[data-testid="stSidebar"] > div {
        padding-top: 2rem;
    }
</style>
""", unsafe_allow_html=True)


@st.cache_resource
def get_conversation_manager():
    """Initialize and cache the conversation manager."""
    manager = ConversationManager()
    
    # Register cleanup function
    def cleanup():
        try:
            manager.stop_kodelet_serve()
        except:
            pass
    
    atexit.register(cleanup)
    return manager


def main():
    """Main application function."""
    
    # Get conversation manager
    conversation_manager = get_conversation_manager()
    
    # Ensure session state is initialized for this session
    conversation_manager._initialize_session_state()
    
    # Initialize session state
    if "initialized" not in st.session_state:
        st.session_state.initialized = True
        # Try to start kodelet serve automatically
        conversation_manager.start_kodelet_serve()
    
    # Render sidebar and handle conversation selection
    selected_conversation_id = render_sidebar(conversation_manager)
    
    # Handle conversation loading
    if selected_conversation_id:
        current_id = conversation_manager.get_current_conversation_id()
        if current_id != selected_conversation_id:
            with st.spinner("Loading conversation..."):
                success = conversation_manager.load_conversation(selected_conversation_id)
            if success:
                st.success("Conversation loaded!")
                st.rerun()
            else:
                st.error("Failed to load conversation")
    
    # Main content area
    main_col, status_col = st.columns([4, 1])
    
    with main_col:
        # Page header
        st.title("🤖 Kodelet Chatbot")
        st.markdown("*Intelligent assistance powered by kodelet*")
        
        # Settings panel (if enabled)
        render_settings_panel()
        
        # Chat interface
        render_chat_interface(conversation_manager)
        
        # Add some spacing before input
        st.markdown("<br>" * 2, unsafe_allow_html=True)
        
        # Input area
        render_input_area(conversation_manager)
    
    with status_col:
        # Status information
        st.subheader("Status")
        render_status_bar(conversation_manager)
        
        # Quick help
        st.subheader("Quick Help")
        st.markdown("""
        **Getting Started:**
        1. Make sure API is online
        2. Type a message below
        3. View/resume past conversations in sidebar
        
        **Features:**
        - 🖼️ Image uploads
        - 💬 Multi-turn conversations  
        - 🔧 Tool execution
        - 📚 Conversation history
        
        **Tips:**
        - Use natural language
        - Ask for code help
        - Request file operations
        - Upload screenshots/diagrams
        """)


if __name__ == "__main__":
    main()