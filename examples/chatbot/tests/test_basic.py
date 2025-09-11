"""Basic tests for the kodelet chatbot."""

import pytest
from unittest.mock import Mock, patch
import sys
import os

# Add src to path so we can import modules
sys.path.insert(0, os.path.join(os.path.dirname(__file__), '..'))

from src.kodelet_client import KodeletClient, KodeletMessage, KodeletConversation


class TestKodeletClient:
    """Tests for KodeletClient."""
    
    def test_initialization(self):
        """Test client initialization."""
        client = KodeletClient()
        assert client.kodelet_binary == "kodelet"
        assert client.api_host == "localhost"
        assert client.api_port == 8080
        assert client.base_url == "http://localhost:8080"
    
    def test_initialization_with_params(self):
        """Test client initialization with custom parameters."""
        client = KodeletClient(
            kodelet_binary="/custom/path/kodelet",
            api_host="0.0.0.0",
            api_port=9090
        )
        assert client.kodelet_binary == "/custom/path/kodelet"
        assert client.api_host == "0.0.0.0"
        assert client.api_port == 9090
        assert client.base_url == "http://0.0.0.0:9090"
    
    def test_parse_kodelet_output_simple(self):
        """Test parsing simple kodelet output."""
        client = KodeletClient()
        output = """[user]: Hello
        
This is a response from kodelet.

ID: test-conv-123
To resume this conversation: kodelet run --resume test-conv-123"""
        
        response, conv_id = client._parse_kodelet_output(output)
        assert response == "This is a response from kodelet."
        assert conv_id == "test-conv-123"
    
    def test_parse_kodelet_output_with_stats(self):
        """Test parsing kodelet output with usage statistics."""
        client = KodeletClient()
        output = """[user]: What is 2+2?
        
The answer is 4.

Input Tokens: 10
Output Tokens: 15
Total Cost: $0.001
═════════════════════════
ID: test-conv-456
To resume this conversation: kodelet run --resume test-conv-456
To delete this conversation: kodelet conversation delete test-conv-456"""
        
        response, conv_id = client._parse_kodelet_output(output)
        assert response == "The answer is 4."
        assert conv_id == "test-conv-456"


class TestKodeletMessage:
    """Tests for KodeletMessage."""
    
    def test_message_creation(self):
        """Test message creation."""
        message = KodeletMessage("user", "Hello world")
        assert message.role == "user"
        assert message.content == "Hello world"
        assert message.tool_calls == []
        assert message.thinking_text is None
    
    def test_message_with_thinking(self):
        """Test message with thinking text."""
        message = KodeletMessage(
            "assistant", 
            "Here's my response", 
            thinking_text="Let me think about this..."
        )
        assert message.role == "assistant"
        assert message.content == "Here's my response"
        assert message.thinking_text == "Let me think about this..."


class TestKodeletConversation:
    """Tests for KodeletConversation."""
    
    def test_conversation_creation(self):
        """Test conversation creation."""
        conv = KodeletConversation(
            id="test-123",
            created_at="2024-01-01T00:00:00Z",
            updated_at="2024-01-01T01:00:00Z",
            provider="anthropic",
            message_count=5,
            summary="Test conversation"
        )
        assert conv.id == "test-123"
        assert conv.provider == "anthropic"
        assert conv.message_count == 5
        assert conv.summary == "Test conversation"
        assert conv.messages == []
    
    def test_conversation_with_messages(self):
        """Test conversation with messages."""
        messages = [
            KodeletMessage("user", "Hello"),
            KodeletMessage("assistant", "Hi there!")
        ]
        conv = KodeletConversation(
            id="test-456",
            created_at="2024-01-01T00:00:00Z",
            updated_at="2024-01-01T01:00:00Z",
            provider="openai",
            message_count=2,
            messages=messages
        )
        assert len(conv.messages) == 2
        assert conv.messages[0].content == "Hello"
        assert conv.messages[1].content == "Hi there!"


# Mock tests for methods that require subprocess/requests
class TestKodeletClientMocked:
    """Tests for KodeletClient with mocked dependencies."""
    
    @patch('src.kodelet_client.subprocess.run')
    def test_run_query_success(self, mock_subprocess):
        """Test successful query execution."""
        # Mock subprocess response
        mock_result = Mock()
        mock_result.returncode = 0
        mock_result.stdout = """[user]: Hello
        
Hi there! How can I help you?

ID: test-conv-789
To resume this conversation: kodelet run --resume test-conv-789"""
        mock_subprocess.return_value = mock_result
        
        client = KodeletClient()
        response, conv_id = client.run_query("Hello")
        
        assert response == "Hi there! How can I help you?"
        assert conv_id == "test-conv-789"
        mock_subprocess.assert_called_once()
    
    @patch('src.kodelet_client.subprocess.run')
    def test_run_query_error(self, mock_subprocess):
        """Test query execution with error."""
        # Mock subprocess error response
        mock_result = Mock()
        mock_result.returncode = 1
        mock_result.stderr = "API key not configured"
        mock_subprocess.return_value = mock_result
        
        client = KodeletClient()
        response, conv_id = client.run_query("Hello")
        
        assert response == "Error: API key not configured"
        assert conv_id is None
    
    @patch('src.kodelet_client.requests.get')
    def test_is_serve_running_true(self, mock_get):
        """Test serve status check when running."""
        mock_response = Mock()
        mock_response.status_code = 200
        mock_get.return_value = mock_response
        
        client = KodeletClient()
        assert client._is_serve_running() is True
    
    @patch('src.kodelet_client.requests.get')
    def test_is_serve_running_false(self, mock_get):
        """Test serve status check when not running."""
        mock_get.side_effect = Exception("Connection refused")
        
        client = KodeletClient()
        assert client._is_serve_running() is False


if __name__ == "__main__":
    pytest.main([__file__])