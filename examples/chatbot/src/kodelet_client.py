"""Kodelet CLI and API client for the chatbot."""

import json
import re
import subprocess
import threading
import time
from typing import Dict, List, Optional, Tuple

import requests


class KodeletMessage:
    """Represents a message in a kodelet conversation."""
    
    def __init__(self, role: str, content: str, tool_calls: Optional[List] = None, 
                 thinking_text: Optional[str] = None):
        self.role = role
        self.content = content
        self.tool_calls = tool_calls or []
        self.thinking_text = thinking_text


class KodeletConversation:
    """Represents a kodelet conversation."""
    
    def __init__(self, id: str, created_at: str, updated_at: str, 
                 provider: str, message_count: int, summary: Optional[str] = None,
                 messages: Optional[List[KodeletMessage]] = None):
        self.id = id
        self.created_at = created_at
        self.updated_at = updated_at
        self.provider = provider
        self.message_count = message_count
        self.summary = summary
        self.messages = messages or []


class KodeletClient:
    """Client for interacting with kodelet CLI and API."""
    
    def __init__(self, kodelet_binary: str = "kodelet", api_host: str = "localhost", 
                 api_port: int = 8080):
        self.kodelet_binary = kodelet_binary
        self.api_host = api_host
        self.api_port = api_port
        self.base_url = f"http://{api_host}:{api_port}"
        self._serve_process = None
        self._serve_thread = None
        
    def start_serve(self) -> bool:
        """Start kodelet serve in background if not already running."""
        if self._serve_process is not None:
            return True
            
        # Check if already running
        if self._is_serve_running():
            return True
            
        try:
            # Start kodelet serve in background
            self._serve_process = subprocess.Popen(
                [self.kodelet_binary, "serve", "--host", self.api_host, "--port", str(self.api_port)],
                stdout=subprocess.PIPE,
                stderr=subprocess.PIPE,
                text=True
            )
            
            # Wait a bit for server to start
            time.sleep(2)
            
            # Verify it's running
            return self._is_serve_running()
            
        except Exception as e:
            print(f"Failed to start kodelet serve: {e}")
            return False
    
    def stop_serve(self):
        """Stop the kodelet serve process."""
        if self._serve_process:
            self._serve_process.terminate()
            self._serve_process.wait()
            self._serve_process = None
    
    def _is_serve_running(self) -> bool:
        """Check if kodelet serve is running."""
        try:
            response = requests.get(f"{self.base_url}/api/conversations", timeout=2)
            return response.status_code == 200
        except:
            return False
    
    def run_query(self, query: str, conversation_id: Optional[str] = None,
                  images: Optional[List[str]] = None) -> Tuple[str, Optional[str]]:
        """
        Run a kodelet query and return the response and conversation ID.
        
        Args:
            query: The query to send to kodelet
            conversation_id: Optional conversation ID to resume
            images: Optional list of image paths
            
        Returns:
            Tuple of (response, conversation_id)
        """
        cmd = [self.kodelet_binary, "run"]
        
        # Add resume flag if conversation ID provided
        if conversation_id:
            cmd.extend(["--resume", conversation_id])
            
        # Add images if provided
        if images:
            for image in images:
                cmd.extend(["--image", image])
                
        # Add the query
        cmd.append(query)
        
        try:
            result = subprocess.run(
                cmd, 
                capture_output=True, 
                text=True, 
                timeout=300  # 5 minute timeout
            )
            
            if result.returncode != 0:
                error_msg = result.stderr.strip() if result.stderr else "Unknown error"
                return f"Error: {error_msg}", conversation_id
                
            # Parse the output to extract response and conversation ID
            output = result.stdout
            response, parsed_conv_id = self._parse_kodelet_output(output)
            
            # Use parsed conversation ID if we got one, otherwise use the provided one
            final_conv_id = parsed_conv_id or conversation_id
            
            return response, final_conv_id
            
        except subprocess.TimeoutExpired:
            return "Error: Request timed out", conversation_id
        except Exception as e:
            return f"Error: {str(e)}", conversation_id
    
    def _parse_kodelet_output(self, output: str) -> Tuple[str, Optional[str]]:
        """
        Parse kodelet output to extract response and conversation ID.
        
        Args:
            output: Raw output from kodelet command
            
        Returns:
            Tuple of (response, conversation_id)
        """
        lines = output.split('\n')
        response_lines = []
        conversation_id = None
        
        # Skip initial user query line (starts with [user]:)
        skip_user_line = True
        
        for line in lines:
            # Skip the echoed user query
            if skip_user_line and line.strip().startswith('[user]:'):
                skip_user_line = False
                continue
                
            # Extract conversation ID
            if "ID:" in line and not line.strip().startswith('[user]:'):
                # Look for patterns like "ID: abc123" 
                id_match = re.search(r'ID:\s*([a-zA-Z0-9-]+)', line)
                if id_match:
                    conversation_id = id_match.group(1)
                continue
                    
            if "To resume this conversation:" in line:
                # Look for patterns like "kodelet run --resume abc123"
                resume_match = re.search(r'--resume\s+([a-zA-Z0-9-]+)', line)
                if resume_match:
                    conversation_id = resume_match.group(1)
                continue
                
            # Skip other metadata lines
            if any(skip_pattern in line for skip_pattern in [
                "To resume this conversation:",
                "To delete this conversation:",
                "Input Tokens:",
                "Output Tokens:",
                "Total Cost:",
                "═════════"
            ]):
                continue
                
            # Add to response if not empty
            if line.strip():
                response_lines.append(line)
        
        response = '\n'.join(response_lines).strip()
        return response, conversation_id
    
    def get_conversations(self, limit: int = 50, offset: int = 0, 
                         search: Optional[str] = None) -> List[KodeletConversation]:
        """Get list of conversations from the API."""
        if not self._is_serve_running():
            return []
            
        try:
            params = {"limit": limit, "offset": offset}
            if search:
                params["search"] = search
                
            response = requests.get(f"{self.base_url}/api/conversations", params=params)
            response.raise_for_status()
            
            data = response.json()
            conversations = []
            
            for conv_data in data.get("conversations", []):
                conv = KodeletConversation(
                    id=conv_data["id"],
                    created_at=conv_data["createdAt"],
                    updated_at=conv_data["updatedAt"],
                    provider=conv_data["provider"],
                    message_count=conv_data["messageCount"],
                    summary=conv_data.get("summary")
                )
                conversations.append(conv)
                
            return conversations
            
        except Exception as e:
            print(f"Error fetching conversations: {e}")
            return []
    
    def get_conversation(self, conversation_id: str) -> Optional[KodeletConversation]:
        """Get detailed conversation with messages."""
        if not self._is_serve_running():
            return None
            
        try:
            response = requests.get(f"{self.base_url}/api/conversations/{conversation_id}")
            response.raise_for_status()
            
            data = response.json()
            
            # Parse messages
            messages = []
            for msg_data in data.get("messages", []):
                message = KodeletMessage(
                    role=msg_data["role"],
                    content=msg_data["content"],
                    tool_calls=msg_data.get("toolCalls", []),
                    thinking_text=msg_data.get("thinkingText")
                )
                messages.append(message)
            
            conv = KodeletConversation(
                id=data["id"],
                created_at=data["createdAt"],
                updated_at=data["updatedAt"],
                provider=data["provider"],
                message_count=data["messageCount"],
                summary=data.get("summary"),
                messages=messages
            )
            
            return conv
            
        except Exception as e:
            print(f"Error fetching conversation {conversation_id}: {e}")
            return None
    
    def delete_conversation(self, conversation_id: str) -> bool:
        """Delete a conversation."""
        if not self._is_serve_running():
            return False
            
        try:
            response = requests.delete(f"{self.base_url}/api/conversations/{conversation_id}")
            return response.status_code == 204
        except Exception as e:
            print(f"Error deleting conversation {conversation_id}: {e}")
            return False