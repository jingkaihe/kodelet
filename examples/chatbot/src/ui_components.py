"""UI components for the streamlit chatbot."""

import streamlit as st
from datetime import datetime
from typing import List, Optional
from .kodelet_client import KodeletConversation
from .conversation_manager import ConversationManager


def render_sidebar(conversation_manager: ConversationManager) -> Optional[str]:
    """
    Render the sidebar with conversation list and controls.
    
    Args:
        conversation_manager: The conversation manager instance
        
    Returns:
        Selected conversation ID or None
    """
    st.sidebar.title("💬 Kodelet Chatbot")
    
    # New conversation button
    if st.sidebar.button("🗂️ New Conversation", use_container_width=True):
        conversation_manager.clear_current_conversation()
        st.rerun()
    
    # Refresh conversations button
    col1, col2 = st.sidebar.columns([1, 1])
    with col1:
        if st.button("🔄 Refresh", use_container_width=True):
            conversation_manager.refresh_conversation_list()
            st.rerun()
    
    with col2:
        # Start/check kodelet serve
        if conversation_manager.client._is_serve_running():
            st.success("📡 API Online", icon="✅")
        else:
            if st.button("📡 Start API", use_container_width=True):
                with st.spinner("Starting kodelet serve..."):
                    success = conversation_manager.start_kodelet_serve()
                if success:
                    st.success("API started!")
                    st.rerun()
                else:
                    st.error("Failed to start API")
    
    # Conversation list
    st.sidebar.subheader("Recent Conversations")
    
    conversations = conversation_manager.get_conversation_list()
    
    if not conversations:
        if conversation_manager.client._is_serve_running():
            st.sidebar.info("No conversations found")
        else:
            st.sidebar.warning("Start API to load conversations")
        return None
    
    # Display conversations
    selected_conversation_id = None
    current_conv_id = conversation_manager.get_current_conversation_id()
    
    for conversation in conversations:
        # Create a container for each conversation
        container = st.sidebar.container()
        
        with container:
            # Format datetime
            try:
                updated_dt = datetime.fromisoformat(conversation.updated_at.replace('Z', '+00:00'))
                time_str = updated_dt.strftime("%m/%d %H:%M")
            except:
                time_str = "Unknown"
            
            # Get preview text
            preview = conversation_manager.get_conversation_preview(conversation)
            
            # Create display text
            provider_icon = "🤖" if conversation.provider == "anthropic" else "🧠"
            display_text = f"{provider_icon} {preview}"
            
            # Show conversation button with highlighting for current conversation
            button_type = "primary" if current_conv_id == conversation.id else "secondary"
            
            if st.button(
                display_text,
                key=f"conv_{conversation.id}",
                help=f"ID: {conversation.id}\nUpdated: {time_str}\nMessages: {conversation.message_count}",
                use_container_width=True,
                type=button_type
            ):
                selected_conversation_id = conversation.id
            
            # Add delete button
            col1, col2 = st.columns([4, 1])
            with col2:
                if st.button(
                    "🗑️", 
                    key=f"del_{conversation.id}",
                    help="Delete conversation",
                ):
                    if st.session_state.get(f"confirm_delete_{conversation.id}", False):
                        # Actually delete
                        with st.spinner("Deleting..."):
                            success = conversation_manager.delete_conversation(conversation.id)
                        if success:
                            st.success("Deleted!")
                            st.rerun()
                        else:
                            st.error("Failed to delete")
                        # Reset confirmation
                        st.session_state[f"confirm_delete_{conversation.id}"] = False
                    else:
                        # Ask for confirmation
                        st.session_state[f"confirm_delete_{conversation.id}"] = True
                        st.warning("Click again to confirm delete")
    
    return selected_conversation_id


def render_chat_interface(conversation_manager: ConversationManager):
    """
    Render the main chat interface.
    
    Args:
        conversation_manager: The conversation manager instance
    """
    # Display current conversation info
    current_conv_id = conversation_manager.get_current_conversation_id()
    if current_conv_id:
        st.info(f"💬 Conversation: `{current_conv_id}`")
    else:
        st.info("💬 Start a new conversation by typing a message below")
    
    # Display chat messages
    messages = conversation_manager.get_current_messages()
    
    # Create a container for messages
    chat_container = st.container()
    
    with chat_container:
        for message in messages:
            with st.chat_message(message["role"]):
                # Render message content
                content = message["content"]
                
                # Handle special formatting for tool calls
                if message["role"] == "assistant" and content.startswith("🔧 Tool:"):
                    st.code(content, language="text")
                else:
                    st.markdown(content)


def render_input_area(conversation_manager: ConversationManager):
    """
    Render the input area for new messages.
    
    Args:
        conversation_manager: The conversation manager instance
    """
    # Image upload (optional)
    uploaded_files = st.file_uploader(
        "Upload images (optional)", 
        accept_multiple_files=True,
        type=['png', 'jpg', 'jpeg', 'gif'],
        help="Upload images to include with your message"
    )
    
    # Main chat input
    user_input = st.chat_input("Type your message here...")
    
    if user_input:
        # Handle uploaded files
        image_paths = []
        if uploaded_files:
            import tempfile
            import os
            
            for uploaded_file in uploaded_files:
                # Save uploaded file to temp directory
                with tempfile.NamedTemporaryFile(delete=False, suffix=f"_{uploaded_file.name}") as tmp_file:
                    tmp_file.write(uploaded_file.read())
                    image_paths.append(tmp_file.name)
        
        # Create a placeholder for streaming updates
        with st.spinner("🤖 Kodelet is thinking..."):
            # Create containers for real-time updates
            message_placeholder = st.empty()
            status_placeholder = st.empty()
            
            # Send the message with streaming
            try:
                response, success = conversation_manager.send_message(user_input, image_paths)
                
                if success:
                    message_placeholder.success("✅ Response received!")
                else:
                    message_placeholder.error("❌ Error occurred")
                    
                # Clear status after a moment
                import time
                time.sleep(1)
                message_placeholder.empty()
                status_placeholder.empty()
                
            except Exception as e:
                st.error(f"Unexpected error: {str(e)}")
        
        # Clean up temp files
        for path in image_paths:
            try:
                import os
                os.unlink(path)
            except:
                pass
        
        # Rerun to update the UI
        st.rerun()


def render_status_bar(conversation_manager: ConversationManager):
    """
    Render a status bar with system information.
    
    Args:
        conversation_manager: The conversation manager instance
    """
    col1, col2, col3 = st.columns([2, 2, 1])
    
    with col1:
        # API status
        if conversation_manager.client._is_serve_running():
            st.success("🟢 API Connected")
        else:
            st.error("🔴 API Disconnected")
    
    with col2:
        # Current conversation status
        current_conv_id = conversation_manager.get_current_conversation_id()
        if current_conv_id:
            messages = conversation_manager.get_current_messages()
            st.info(f"💬 {len(messages)} messages")
        else:
            st.info("💬 New conversation")
    
    with col3:
        # Settings button
        if st.button("⚙️ Settings"):
            st.session_state.show_settings = not st.session_state.get("show_settings", False)


def render_settings_panel():
    """Render the settings panel."""
    if not st.session_state.get("show_settings", False):
        return
    
    with st.expander("⚙️ Settings", expanded=True):
        st.subheader("Configuration")
        
        # Model selection (informational for now)
        st.selectbox(
            "Model Provider",
            ["anthropic", "openai"],
            help="Configure this via kodelet config or environment variables",
            disabled=True
        )
        
        # API settings
        col1, col2 = st.columns([1, 1])
        with col1:
            st.text_input("API Host", value="localhost", disabled=True)
        with col2:
            st.number_input("API Port", value=8080, disabled=True)
        
        st.info("💡 Settings are currently read-only. Configure kodelet via environment variables or config files.")
        
        # About
        st.subheader("About")
        st.write("**Kodelet Chatbot** v0.1.0")
        st.write("A streamlit-based web UI powered by kodelet")
        st.write("Built with ❤️ for the kodelet community")