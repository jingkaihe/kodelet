"""
Kodelet Chatbot - A streamlit-based web UI powered by kodelet

This application provides a chat interface that integrates with kodelet's
CLI and API to provide intelligent assistance with software engineering tasks.
"""

import sys
import os
import streamlit as st
import atexit

# Add src directory to Python path for imports
current_dir = os.path.dirname(os.path.abspath(__file__)) if '__file__' in globals() else os.getcwd()
src_path = os.path.join(current_dir, 'src')
if src_path not in sys.path:
    sys.path.insert(0, src_path)

from conversation_manager import ConversationManager
from ui_components import (
    render_sidebar,
    render_chat_interface,
    render_input_area,
    render_settings_panel
)

# Configure streamlit page
st.set_page_config(
    page_title="Kodelet Chatbot",
    page_icon="🤖",
    layout="wide",
    initial_sidebar_state="expanded"
)

# Custom CSS for better UI - aggressive sidebar overflow fix
st.markdown("""
<style>
    .main .block-container {
        padding-top: 2rem;
        padding-bottom: 2rem;
        max-width: 100%;
        padding-left: 420px !important; /* More space for sidebar */
        padding-right: 3rem;
        margin-left: 0 !important;
    }
    
    .stChatMessage {
        margin-bottom: 1rem;
    }
    
    .stChatInputContainer {
        position: fixed;
        bottom: 0;
        background: white;
        padding: 1rem;
        width: calc(100% - 400px); /* Subtract sidebar width */
        z-index: 1000;
        left: 400px; /* Account for sidebar */
        box-sizing: border-box;
    }
    
    /* Aggressive sidebar width constraints */
    .css-1d391kg, .css-1cypcdb, .css-6qob1r, .css-17eq0hr, .css-1lcbmhc {
        width: 380px !important;
        min-width: 380px !important;
        max-width: 380px !important;
    }
    
    /* Force sidebar container constraints */
    section[data-testid="stSidebar"] {
        width: 380px !important;
        min-width: 380px !important;
        max-width: 380px !important;
    }
    
    /* Sidebar styling with strict overflow control */
    [data-testid="stSidebar"] > div {
        width: 380px !important;
        min-width: 380px !important;
        max-width: 380px !important;
        padding: 1rem 0.75rem !important;
        overflow: hidden !important;
        box-sizing: border-box !important;
    }
    
    [data-testid="stSidebar"] .block-container {
        padding: 0.5rem !important;
        max-width: 380px !important;
        width: 100% !important;
        overflow: hidden !important;
        box-sizing: border-box !important;
    }
    
    /* Force all sidebar content to stay within bounds */
    [data-testid="stSidebar"] * {
        max-width: 100% !important;
        box-sizing: border-box !important;
        overflow: hidden !important;
    }
    
    /* Aggressive button constraints */
    [data-testid="stSidebar"] .stButton {
        width: 100% !important;
        max-width: 100% !important;
    }
    
    [data-testid="stSidebar"] .stButton > button {
        width: 100% !important;
        max-width: 100% !important;
        overflow: hidden !important;
        text-overflow: ellipsis !important;
        white-space: nowrap !important;
        padding: 0.5rem 0.5rem !important;
        font-size: 0.8rem !important;
        line-height: 1.1 !important;
        height: 2.5rem !important;
        display: block !important;
        text-align: left !important;
        box-sizing: border-box !important;
    }
    
    /* Force column layouts in sidebar to respect width */
    [data-testid="stSidebar"] .row-widget {
        width: 100% !important;
        max-width: 100% !important;
    }
    
    [data-testid="stSidebar"] .col {
        max-width: 100% !important;
        overflow: hidden !important;
    }
    
    /* Better conversation item styling */
    .conversation-item {
        margin-bottom: 0.5rem;
        padding: 0.75rem;
        border-radius: 0.5rem;
        border: 1px solid #e0e0e0;
        background: white;
        max-width: 100%;
        box-sizing: border-box;
        overflow: hidden;
    }
    
    .conversation-item:hover {
        background-color: #f5f5f5;
    }
    
    /* Status section in sidebar */
    .sidebar-status {
        background: #f8f9fa;
        border-radius: 0.5rem;
        padding: 0.75rem;
        margin-top: 1rem;
        border: 1px solid #e9ecef;
        max-width: 100%;
        box-sizing: border-box;
    }
    
    /* Button styling improvements */
    .stButton > button {
        border-radius: 0.5rem;
        border: 1px solid #e0e0e0;
        background: white;
        color: #262730;
        transition: all 0.2s ease;
    }
    
    .stButton > button:hover {
        background: #f0f0f0;
        border-color: #d0d0d0;
    }
    
    /* Primary button styling */
    .stButton > button[kind="primary"] {
        background: #ff4b4b;
        color: white;
        border-color: #ff4b4b;
    }
    
    .stButton > button[kind="primary"]:hover {
        background: #ff3030;
        border-color: #ff3030;
    }
    
    /* Ensure main content doesn't overlap with sidebar */
    .main {
        margin-left: 380px !important;
    }
    
    .main > div {
        margin-left: 0 !important;
        max-width: none !important;
    }
    
    /* Better chat interface spacing */
    .stChatMessage {
        max-width: 800px;
        margin: 0 auto 1rem auto;
    }
    
    /* Center the chat input */
    .stChatInput {
        max-width: 800px;
        margin: 0 auto;
    }
    
    /* Hide any potential overflow elements */
    [data-testid="stSidebar"] .element-container {
        max-width: 100% !important;
        overflow: hidden !important;
    }
    
    /* Target specific streamlit column layouts */
    [data-testid="stSidebar"] div[data-testid="column"] {
        max-width: 100% !important;
        overflow: hidden !important;
        flex-shrink: 1 !important;
    }
    
    /* Force text elements to wrap properly */
    [data-testid="stSidebar"] h1,
    [data-testid="stSidebar"] h2,
    [data-testid="stSidebar"] h3,
    [data-testid="stSidebar"] p,
    [data-testid="stSidebar"] div,
    [data-testid="stSidebar"] span {
        word-wrap: break-word !important;
        overflow-wrap: break-word !important;
        max-width: 100% !important;
        box-sizing: border-box !important;
        overflow: hidden !important;
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
    
    # Main content area - full width without right column
    with st.container():
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


if __name__ == "__main__":
    main()