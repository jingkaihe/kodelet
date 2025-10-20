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
    def test_get_latest_conversation_id_success(self, mock_subprocess):
        """Test successful retrieval of latest conversation ID."""
        # Mock subprocess response for conversation list
        mock_result = Mock()
        mock_result.returncode = 0
        mock_result.stdout = '{"conversations": [{"id": "test-conv-123", "createdAt": "2024-01-01T00:00:00Z"}]}'
        mock_subprocess.return_value = mock_result
        
        client = KodeletClient()
        conv_id = client._get_latest_conversation_id()
        
        assert conv_id == "test-conv-123"
        mock_subprocess.assert_called_once()
    
    @patch('src.kodelet_client.KodeletClient.get_conversations')  # Mock the fallback too
    @patch('src.kodelet_client.subprocess.run')
    def test_get_latest_conversation_id_no_conversations(self, mock_subprocess, mock_get_conversations):
        """Test when there are no conversations."""
        # Mock subprocess response with empty list
        mock_result = Mock()
        mock_result.returncode = 0
        mock_result.stdout = '{"conversations": []}'
        mock_subprocess.return_value = mock_result
        
        # Mock the API fallback to also return empty list
        mock_get_conversations.return_value = []
        
        client = KodeletClient()
        conv_id = client._get_latest_conversation_id()
        
        assert conv_id is None
    
    @patch('src.kodelet_client.time.sleep')  # Mock sleep to speed up tests
    @patch('src.kodelet_client.subprocess.Popen')
    @patch('src.kodelet_client.KodeletClient._get_latest_conversation_id')
    @patch('src.kodelet_client.KodeletClient.get_conversation')
    def test_run_query_generator_success(self, mock_get_conv, mock_get_latest_id, mock_popen, mock_sleep):
        """Test run_query as generator with successful response."""
        # Mock process - make it exit immediately to avoid polling loop
        mock_process = Mock()
        mock_process.poll.return_value = 0  # Process already finished
        mock_process.wait.return_value = None
        mock_popen.return_value = mock_process
        
        # Mock conversation ID lookup
        mock_get_latest_id.return_value = "test-conv-456"
        
        # Mock conversation with one message
        mock_conv_with_message = Mock()
        mock_message = KodeletMessage("assistant", "Hello there!")
        mock_conv_with_message.messages = [mock_message]
        
        # Only the final check will run since process.poll() returns 0 immediately
        mock_get_conv.return_value = mock_conv_with_message
        
        client = KodeletClient()
        
        # Collect messages from generator
        messages = list(client.run_query("Hello"))
        
        assert len(messages) == 1
        assert messages[0].role == "assistant"
        assert messages[0].content == "Hello there!"
    
    @patch('src.kodelet_client.KodeletClient._get_latest_conversation_id')
    @patch('src.kodelet_client.subprocess.Popen')  
    def test_run_query_generator_error(self, mock_popen, mock_get_latest_id):
        """Test run_query generator with error."""
        # Mock process that fails immediately
        mock_process = Mock()
        mock_process.poll.return_value = 1  # Process failed
        mock_popen.return_value = mock_process
        
        # Mock that we can't get conversation ID (service not running)
        mock_get_latest_id.return_value = None
        
        client = KodeletClient()
        
        # Collect messages from generator
        messages = list(client.run_query("Hello"))
        
        # Should get error message about conversation ID
        assert len(messages) == 1
        assert "Error: Unable to determine conversation ID" in messages[0].content
    
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