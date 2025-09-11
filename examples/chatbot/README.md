# Kodelet Chatbot

A streamlit-based web UI that provides a chat interface powered by kodelet. This example demonstrates how to integrate kodelet's CLI and API capabilities into a custom web application.

## Features

- 💬 **Interactive Chat Interface**: Chat with kodelet using a clean, modern web UI
- 🔄 **Multi-turn Conversations**: Seamless conversation continuity with automatic conversation management
- 📚 **Conversation History**: Browse, load, and delete past conversations
- 🖼️ **Image Support**: Upload images to include with your messages
- 🔧 **Tool Execution**: View tool execution results directly in the chat
- 📡 **API Integration**: Automatically manages kodelet serve for conversation history
- 🎨 **Modern UI**: Responsive design with sidebar navigation and status indicators

## Prerequisites

1. **Kodelet installed and configured**:
   ```bash
   # Install kodelet (see main README for installation instructions)
   curl -sSL https://raw.githubusercontent.com/jingkaihe/kodelet/main/install.sh | bash
   
   # Configure API keys (choose one)
   export ANTHROPIC_API_KEY="sk-ant-api..."  # For Claude
   export OPENAI_API_KEY="sk-..."            # For OpenAI/compatible
   ```

2. **Python 3.9+** and **uv** installed:
   ```bash
   # Install uv if not already installed
   curl -LsSf https://astral.sh/uv/install.sh | sh
   ```

## Quick Start

1. **Clone and setup**:
   ```bash
   cd examples/chatbot
   uv sync  # Install dependencies
   ```

2. **Run the chatbot**:
   ```bash
   uv run streamlit run app.py
   ```

3. **Open your browser** to `http://localhost:8501`

4. **Start chatting!** The app will automatically start kodelet serve in the background.

## Usage

### Basic Chat
1. Type your message in the chat input at the bottom
2. Press Enter or click Send to submit
3. Wait for kodelet's response (you'll see a "Thinking..." spinner)

### Conversation Management
- **New Conversation**: Click "🗂️ New Conversation" in the sidebar
- **Load Past Conversation**: Click on any conversation in the sidebar list
- **Delete Conversation**: Click the 🗑️ button next to a conversation (requires confirmation)

### Image Uploads
1. Use the "Upload images" file uploader above the chat input
2. Select one or more images (PNG, JPG, JPEG, GIF)
3. Type your message and send - images will be included automatically

### API Management
- The app automatically starts `kodelet serve` in the background
- If the API goes offline, click "📡 Start API" in the sidebar
- API status is shown in the sidebar and status bar

## Architecture

The chatbot uses a hybrid approach with real-time streaming:
- **CLI Integration**: Uses `subprocess.Popen` to run `kodelet run` and `kodelet run --resume` in background
- **API Polling**: Polls `kodelet serve` REST API for real-time message streaming as they arrive
- **State Management**: Streamlit session state for UI consistency
- **Background Process**: Automatically manages kodelet serve lifecycle

### Real-time Streaming Implementation

The `run_query` method now works as a generator that yields messages incrementally:

1. **For new conversations** (`kodelet run`):
   - Starts kodelet process in background
   - Polls `kodelet conversation list --limit 1 --json` to discover new conversation ID
   - Streams messages as they appear via `/api/conversations/{id}`

2. **For resumed conversations** (`kodelet run --resume {id}`):
   - Gets existing message count as offset
   - Polls `/api/conversations/{id}` for new messages beyond the offset
   - Yields only new assistant messages (user messages already in UI)

3. **Real-time UI Updates**:
   - Uses `st.empty()` containers for live message streaming **in the main chat area**
   - Updates content incrementally as kodelet generates responses
   - Shows progress indicators and message counts in real-time  
   - Messages appear **in the conversation thread**, not below input box
   - Finalizes conversation state when streaming completes

### Benefits of New Approach
- **Robust**: No more brittle CLI output parsing
- **Real-time**: Messages appear as kodelet generates them (truly streaming!)
- **Reliable**: Uses structured API instead of text parsing
- **Better UX**: Incremental message display with live status indicators
- **Responsive**: Users see progress immediately, not just final results
- **Proper Layout**: Streaming messages appear in main conversation thread ✅

### Key Components

- **`src/kodelet_client.py`**: Handles CLI and API communication with kodelet
- **`src/conversation_manager.py`**: Manages conversation state and UI synchronization
- **`src/ui_components.py`**: Reusable UI components for different parts of the interface
- **`app.py`**: Main streamlit application that ties everything together

## Configuration

The chatbot inherits kodelet's configuration. You can configure:

### API Keys (Required)
```bash
# For Claude models
export ANTHROPIC_API_KEY="sk-ant-api..."

# For OpenAI/compatible models  
export OPENAI_API_KEY="sk-..."
export OPENAI_API_BASE="https://api.x.ai/v1"  # Optional: for Grok/other providers
```

### Model Selection
```bash
# Set default provider and model
export KODELET_PROVIDER="anthropic"  # or "openai"
export KODELET_MODEL="claude-sonnet-4-20250514"  # or "gpt-4o", "grok-3", etc.
```

### Other Settings
```bash
export KODELET_MAX_TOKENS="8192"
export KODELET_LOG_LEVEL="info"
```

## Development

### Project Structure
```
examples/chatbot/
├── app.py                    # Main streamlit app
├── pyproject.toml           # uv project config
├── uv.lock                  # Dependency lock file (commit this!)
├── .gitignore               # Git ignore patterns
├── src/
│   ├── kodelet_client.py    # Kodelet CLI & API client
│   ├── conversation_manager.py  # State management
│   └── ui_components.py     # UI components
├── tests/                   # Test suite
├── Dockerfile               # Container deployment
└── README.md               # This file
```

### Running in Development
```bash
# Install dependencies
uv sync

# Install dev dependencies
uv sync --group dev

# Run the app with auto-reload
uv run streamlit run app.py --server.runOnSave true

# Format code
uv run black .
uv run ruff check .
```

### Version Control
The project includes a comprehensive `.gitignore` that excludes:
- Python bytecode (`__pycache__/`, `*.pyc`)
- Virtual environments (`.venv/`, `venv/`)
- IDE files (`.vscode/`, `.idea/`)
- Test artifacts (`.pytest_cache/`, `.coverage`)
- Temporary files created by the app
- OS-specific files (`.DS_Store`, `Thumbs.db`)
- Streamlit cache files (`.streamlit/`)

**Important**: `uv.lock` is included in version control for reproducible builds.

### Testing
```bash
# Run tests (when implemented)
uv run pytest

# Run with coverage
uv run pytest --cov=src
```

## Deployment

### Local Deployment
```bash
cd examples/chatbot
uv run streamlit run app.py --server.address 0.0.0.0 --server.port 8501
```

### Docker Deployment
```bash
# Build image
docker build -t kodelet-chatbot .

# Run container
docker run -p 8501:8501 \
  -e ANTHROPIC_API_KEY="$ANTHROPIC_API_KEY" \
  -e OPENAI_API_KEY="$OPENAI_API_KEY" \
  kodelet-chatbot
```

### Cloud Deployment
The app can be deployed to:
- **Streamlit Cloud**: Connect your GitHub repo for automatic deployment
- **Heroku**: Use the included `Dockerfile` for container deployment
- **Railway**: Simple Python app deployment with environment variable configuration

## Troubleshooting

### Common Issues

**"API Disconnected" Error**:
- Check that kodelet is installed and in your PATH
- Try clicking "📡 Start API" in the sidebar
- Ensure ports 8080 (or configured port) is available

**"Error: Unknown error" in Chat**:
- Check your API keys are configured correctly
- Verify kodelet works from command line: `kodelet run "hello"`
- Check the browser console and terminal for detailed error messages

**Conversations Not Loading**:
- Ensure kodelet serve is running (check sidebar status)
- Try refreshing the conversation list
- Check that conversations exist: `kodelet conversation list`

**Image Upload Issues**:
- Only PNG, JPG, JPEG, GIF formats are supported
- Ensure images are under 5MB each
- Check browser console for upload errors

### Debug Mode
Run with debug logging to see more details:
```bash
export KODELET_LOG_LEVEL="debug"
uv run streamlit run app.py
```

### Manual Testing
Test kodelet CLI directly to isolate issues:
```bash
# Test basic functionality
kodelet run "What is 2+2?"

# Test conversation resume
kodelet run --resume <conversation-id> "Continue this conversation"

# Test API
kodelet serve --host localhost --port 8080
curl http://localhost:8080/api/conversations
```

## Contributing

Contributions are welcome! Please:

1. Follow the existing code style (black + ruff)
2. Add tests for new features
3. Update documentation as needed
4. Submit a PR with a clear description

## License

This example is part of the kodelet project and follows the same MIT license.

## Support

For issues and questions:
- Check the main [kodelet documentation](../../README.md)
- Review the [development guide](../../docs/DEVELOPMENT.md)
- Open an issue on the main kodelet repository