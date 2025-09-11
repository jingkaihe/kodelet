"""Conversation state management for the streamlit chatbot."""

import streamlit as st
from typing import Dict, List, Optional, Tuple
from .kodelet_client import KodeletClient, KodeletConversation, KodeletMessage


class ConversationManager:
    """Manages conversation state for the streamlit app."""
    
    def __init__(self, kodelet_client: KodeletClient):
        self.client = kodelet_client
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
            
    def get_current_messages(self) -> List[Dict[str, str]]:
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
    
    def send_message(self, user_message: str, images: Optional[List[str]] = None) -> Tuple[str, bool]:
        """
        Send a message using kodelet and update the conversation.
        
        Args:
            user_message: The user's message
            images: Optional list of image paths
            
        Returns:
            Tuple of (response, success)
        """
        # Add user message to UI immediately
        self.add_message("user", user_message)
        
        # Send to kodelet
        response, conversation_id = self.client.run_query(
            user_message, 
            self.get_current_conversation_id(),
            images
        )
        
        # Check if we got an error
        if response.startswith("Error:"):
            self.add_message("assistant", response)
            return response, False
        
        # Update conversation ID if we got one
        if conversation_id:
            self.set_current_conversation_id(conversation_id)
            
        # Add response to UI
        self.add_message("assistant", response)
        
        # Refresh conversation list to show updated conversation
        self.refresh_conversation_list()
        
        return response, True
    
    def load_conversation(self, conversation_id: str) -> bool:
        """
        Load an existing conversation from the API.
        
        Args:
            conversation_id: The conversation ID to load
            
        Returns:
            True if successful, False otherwise
        """
        conversation = self.client.get_conversation(conversation_id)
        if not conversation:
            return False
            
        # Clear current conversation
        st.session_state.messages = []
        
        # Load messages from the conversation
        for message in conversation.messages:
            # Add thinking text if present (for Claude)
            if message.thinking_text:
                self.add_message("assistant", f"*Thinking: {message.thinking_text}*\n\n{message.content}")
            else:
                self.add_message(message.role, message.content)
                
            # Add tool calls if present
            if message.tool_calls:
                for tool_call in message.tool_calls:
                    tool_info = f"🔧 Tool: {tool_call.get('function', {}).get('name', 'Unknown')}"
                    if 'function' in tool_call and 'arguments' in tool_call['function']:
                        tool_info += f"\nArgs: {tool_call['function']['arguments']}"
                    self.add_message("assistant", tool_info)
        
        # Set the current conversation ID
        self.set_current_conversation_id(conversation_id)
        
        return True
    
    def get_conversation_list(self, refresh: bool = False) -> List[KodeletConversation]:
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
        if (refresh or 
            current_time - st.session_state.conversation_list_last_updated > 30):
            st.session_state.conversation_list = self.client.get_conversations()
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
        success = self.client.delete_conversation(conversation_id)
        
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
            first_user_msg = next((msg for msg in conversation.messages if msg.role == "user"), None)
            if first_user_msg:
                content = first_user_msg.content
                return content[:100] + "..." if len(content) > 100 else content
        
        return f"{conversation.message_count} messages"
    
    def start_kodelet_serve(self) -> bool:
        """Start kodelet serve if not already running."""
        return self.client.start_serve()
    
    def stop_kodelet_serve(self):
        """Stop kodelet serve."""
        self.client.stop_serve()