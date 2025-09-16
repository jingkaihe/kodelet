"""Conversation state management for the streamlit chatbot."""

from typing import Optional

import streamlit as st

from .kodelet_api import (
    run_headless_query, 
    start_serve, 
    stop_serve, 
    is_serve_running,
    get_conversations,
    get_conversation, 
    delete_conversation as delete_conv,
    StreamEntry,
    KodeletConversation
)


class ConversationManager:
    """Manages conversation state for the streamlit app."""

    def __init__(self):
        self._initialize_session_state()

    def _initialize_session_state(self):
        """Initialize streamlit session state variables."""
        if "messages" not in st.session_state:
            st.session_state.messages = []
        if "current_conversation_id" not in st.session_state:
            st.session_state.current_conversation_id = None
        if "conversation_list" not in st.session_state:
            st.session_state.conversation_list = []
        if "conversation_list_last_updated" not in st.session_state:
            st.session_state.conversation_list_last_updated = 0

    def get_current_messages(self) -> list[dict[str, str]]:
        """Get current conversation messages."""
        return st.session_state.messages

    def add_message(self, role: str, content: str):
        """Add a message to the current conversation."""
        st.session_state.messages.append({"role": role, "content": content})

    def clear_current_conversation(self):
        """Clear the current conversation and start a new one."""
        st.session_state.messages = []
        st.session_state.current_conversation_id = None

    def get_current_conversation_id(self) -> Optional[str]:
        """Get the current conversation ID."""
        return st.session_state.current_conversation_id

    def set_current_conversation_id(self, conversation_id: str):
        """Set the current conversation ID."""
        st.session_state.current_conversation_id = conversation_id

    def send_message(self, user_message: str, images: Optional[list[str]] = None):
        """
        Send a message using kodelet and return the stream entry generator.
        
        Args:
            user_message: The user's message
            images: Optional list of image paths
            
        Returns:
            Generator that yields StreamEntry objects as they arrive
        """
        # Add user message to UI immediately
        self.add_message("user", user_message)

        # Return the generator - let the UI handle the streaming
        return run_headless_query(
            user_message,
            self.get_current_conversation_id(),
            images
        )

    def process_streaming_message(self, entry: StreamEntry) -> bool:
        """
        Process a single streaming entry and update the conversation.
        
        Args:
            entry: StreamEntry object from headless/stream mode
            
        Returns:
            True if entry was processed successfully, False if error
        """
        # Handle different types of stream entries
        if entry.kind == "text":
            if entry.content and entry.content.startswith("Error:"):
                self.add_message("assistant", entry.content)
                return False
            
            if entry.role == "assistant" and entry.content:
                self.add_message("assistant", entry.content)
                
        elif entry.kind == "thinking":
            if entry.content:
                thinking_content = f"*Thinking: {entry.content}*"
                self.add_message("assistant", thinking_content)
                
        elif entry.kind == "tool-use":
            if entry.tool_name:
                tool_info = f"🔧 Tool: {entry.tool_name}"
                if entry.input:
                    tool_info += f"\nInput: {entry.input}"
                self.add_message("assistant", tool_info)
                
        elif entry.kind == "tool-result":
            if entry.tool_name and entry.result:
                result_info = f"📋 Tool Result ({entry.tool_name}):\n{entry.result}"
                self.add_message("assistant", result_info)

        # Extract conversation ID if present and we don't have one yet
        if entry.conversation_id and not self.get_current_conversation_id():
            self.set_current_conversation_id(entry.conversation_id)

        return True

    def finalize_streaming_conversation(self):
        """
        Finalize the streaming conversation by refreshing the conversation list.
        Note: Conversation ID should already be set from StreamEntry data during streaming.
        """
        # Refresh conversation list to show updated conversation
        self.refresh_conversation_list()

    def load_conversation(self, conversation_id: str) -> bool:
        """
        Load an existing conversation from the API.
        
        Args:
            conversation_id: The conversation ID to load
            
        Returns:
            True if successful, False otherwise
        """
        conversation = get_conversation(conversation_id)
        if not conversation:
            return False

        # Clear current conversation
        st.session_state.messages = []

        # Load messages from the conversation
        for message in conversation.messages:
            # Add thinking text if present (for Claude)
            if message.get("thinking_text"):
                self.add_message("assistant", f"*Thinking: {message['thinking_text']}*\n\n{message['content']}")
            else:
                self.add_message(message["role"], message["content"])

            # Add tool calls if present
            if message.get("tool_calls"):
                for tool_call in message["tool_calls"]:
                    tool_info = f"🔧 Tool: {tool_call.get('function', {}).get('name', 'Unknown')}"
                    if 'function' in tool_call and 'arguments' in tool_call['function']:
                        tool_info += f"\nArgs: {tool_call['function']['arguments']}"
                    self.add_message("assistant", tool_info)

        # Set the current conversation ID
        self.set_current_conversation_id(conversation_id)

        return True

    def get_conversation_list(self, refresh: bool = False) -> list[KodeletConversation]:
        """
        Get the list of conversations, with optional refresh.
        
        Args:
            refresh: Force refresh from API
            
        Returns:
            List of conversations
        """
        import time
        current_time = time.time()

        # Refresh if forced or if it's been more than 30 seconds
        last_updated = getattr(st.session_state, 'conversation_list_last_updated', 0)
        if (refresh or current_time - last_updated > 30):
            st.session_state.conversation_list = get_conversations()
            st.session_state.conversation_list_last_updated = current_time

        return st.session_state.conversation_list

    def refresh_conversation_list(self):
        """Force refresh the conversation list."""
        self.get_conversation_list(refresh=True)

    def delete_conversation(self, conversation_id: str) -> bool:
        """
        Delete a conversation.
        
        Args:
            conversation_id: The conversation ID to delete
            
        Returns:
            True if successful, False otherwise
        """
        success = delete_conv(conversation_id)

        if success:
            # If we're currently viewing this conversation, clear it
            if st.session_state.current_conversation_id == conversation_id:
                self.clear_current_conversation()

            # Refresh the conversation list
            self.refresh_conversation_list()

        return success

    def get_conversation_preview(self, conversation: KodeletConversation) -> str:
        """
        Get a preview string for a conversation.
        
        Args:
            conversation: The conversation to preview
            
        Returns:
            Preview string
        """
        if conversation.summary:
            return conversation.summary[:100] + "..." if len(conversation.summary) > 100 else conversation.summary

        # If no summary, try to create one from first message
        if conversation.messages:
            first_user_msg = next((msg for msg in conversation.messages if msg["role"] == "user"), None)
            if first_user_msg:
                content = first_user_msg["content"]
                return content[:100] + "..." if len(content) > 100 else content

        return f"{conversation.message_count} messages"

    def start_kodelet_serve(self) -> bool:
        """Start kodelet serve if not already running."""
        return start_serve()

    def stop_kodelet_serve(self):
        """Stop kodelet serve."""
        stop_serve()

    def is_serve_running(self) -> bool:
        """Check if kodelet serve is running."""
        return is_serve_running()
