# Kodelet Chatbot Web UI

## Overview

This document outlines the plan for implementing a chatbot web UI powered by kodelet. The chatbot will provide a streamlit-based interface that leverages kodelet's capabilities for multi-turn conversations and tool execution.

## Architecture

### Option 1: Shell Out to Kodelet CLI (Recommended)
- Use `subprocess` to call `kodelet run` and `kodelet run --resume` commands
- Parse conversation IDs from kodelet output
- Simple to implement and maintains feature parity
- Handles all complex LLM interactions through kodelet

### Option 2: API Integration with Kodelet Serve
- Start `kodelet serve` in background
- Use REST API to read conversation history
- Still need to shell out for new interactions since serve is read-only
- More complex but provides richer conversation browsing

### Hybrid Approach (Selected)
- Shell out to kodelet for new messages and interactions
- Use kodelet serve API for conversation history and management
- Best of both worlds: simple interactions + rich history

## Technical Stack

- **Python**: Core implementation language
- **uv**: Dependency management (fast, reliable Python package installer)
- **Streamlit**: Web UI framework (simple, pythonic, great for prototyping)
- **subprocess**: Shell out to kodelet commands
- **requests**: HTTP client for kodelet serve API
- **json**: Parse kodelet outputs and API responses

## Implementation Plan

### Phase 1: Basic Chat Interface ✅ (REAL-TIME STREAMING v2.1)
1. Create basic streamlit app with chat interface ✅
2. Implement generator-based streaming with subprocess.Popen ✅
3. API polling for real-time message streaming ✅
4. Enable multi-turn conversations with conversation ID discovery ✅
5. Handle user input and display responses incrementally ✅
6. **v2.0**: Real-time UI updates with `st.empty()` containers ✅
7. **v2.0**: Live progress indicators and message streaming ✅
8. **v2.1**: FIXED streaming message location - now appears in main chat thread ✅

### Phase 2: Conversation Management ✅
1. Start kodelet serve in background thread
2. Integration with `/api/conversations` endpoints
3. Conversation list sidebar
4. Load/resume existing conversations
5. Delete conversations functionality

### Phase 3: Enhanced Features
1. Tool result visualization (rich display of tool outputs)
2. Image upload support (`kodelet run --image`)
3. Fragment/recipe support (`kodelet run -r recipe`)
4. Configuration management (model selection, etc.)

### Phase 4: Production Ready
1. Error handling and user feedback
2. Conversation export/import
3. Settings persistence
4. Docker deployment configuration

## API Integration Details

### Kodelet Serve API Endpoints

Based on analysis of the codebase:

```
GET /api/conversations
- Query params: search, sortBy, sortOrder, limit, offset, startDate, endDate
- Returns: {conversations: [], total: number, limit: number, offset: number, hasMore: boolean}

GET /api/conversations/{id}
- Returns: {id, createdAt, updatedAt, provider, summary?, usage, messages[], toolResults{}, messageCount}

GET /api/conversations/{id}/tools/{toolCallId}
- Returns: {toolCallId, result: StructuredToolResult}

DELETE /api/conversations/{id}
- Returns: 204 No Content
```

### Message Structure

Kodelet converts provider-specific messages to unified format:
```typescript
interface WebMessage {
  role: 'user' | 'assistant',
  content: string,
  toolCalls?: ToolCall[],
  thinkingText?: string  // Claude-specific
}
```

### Shell Command Integration

```python
# New conversation
result = subprocess.run(['kodelet', 'run', query], capture_output=True, text=True)

# Resume conversation  
result = subprocess.run(['kodelet', 'run', '--resume', conv_id, query], capture_output=True, text=True)

# Parse conversation ID from output
# Format: "ID: {conversation_id}"
# Format: "To resume this conversation: kodelet run --resume {conversation_id}"
```

## UI Design

### Layout
- **Main Area**: Chat interface with message history
- **Sidebar**: Conversation list, settings, model selection
- **Bottom**: Input area with send button, file upload, options

### Features
- **Chat Interface**: WhatsApp/iMessage style bubbles
- **Conversation List**: Recent conversations with titles/summaries
- **Settings Panel**: Model selection, API keys, advanced options
- **Tool Visualization**: Rich display of tool outputs (code, files, etc.)
- **Message Actions**: Copy, export, regenerate responses

### Streamlit Components

```python
# Chat interface
st.chat_message("user").write(user_input)
st.chat_message("assistant").write(assistant_response)

# Conversation management
selected_conv = st.sidebar.selectbox("Conversations", conversation_list)

# Settings
model = st.sidebar.selectbox("Model", ["claude-sonnet-4", "gpt-4o", "claude-haiku"])

# Input
user_input = st.chat_input("Type your message here...")
uploaded_file = st.file_uploader("Upload image", type=['png', 'jpg', 'jpeg'])
```

## File Structure

```
examples/chatbot/
├── CHATBOT_UI.md                 # This documentation
├── pyproject.toml               # uv project configuration  
├── uv.lock                      # Dependency lock file
├── app.py                       # Main streamlit application
├── src/
│   ├── __init__.py
│   ├── kodelet_client.py        # Kodelet CLI and API client
│   ├── conversation_manager.py  # Conversation state management
│   ├── ui_components.py         # Reusable UI components
│   └── utils.py                 # Utility functions
├── config/
│   └── settings.py              # Configuration management
├── static/                      # Static assets (CSS, images)
├── tests/                       # Test suite
├── README.md                    # Setup and usage instructions
└── Dockerfile                   # Container deployment
```

## Data Flow

1. **User Input**: User types message in streamlit chat input
2. **Message Processing**: App determines if new conversation or continuing existing
3. **Kodelet Execution**: Shell out to appropriate kodelet command
4. **Response Parsing**: Parse kodelet output for response and conversation ID
5. **UI Update**: Display response in chat interface
6. **State Management**: Update conversation list via API

## Error Handling

- **Kodelet CLI Errors**: Parse stderr and display user-friendly messages
- **API Errors**: Handle connection failures gracefully
- **Malformed Responses**: Validate and sanitize outputs
- **Rate Limiting**: Implement request throttling if needed

## Security Considerations

- **Input Validation**: Sanitize user inputs before passing to kodelet
- **Command Injection**: Use subprocess safely with argument lists
- **API Security**: Run kodelet serve on localhost only
- **File Handling**: Secure handling of uploaded files

## Testing Strategy

- **Unit Tests**: Test individual components (kodelet_client, conversation_manager)
- **Integration Tests**: Test end-to-end conversation flows
- **UI Tests**: Test streamlit components and user interactions
- **Error Handling**: Test various failure scenarios

## Deployment Options

### Local Development
```bash
cd examples/chatbot
uv run streamlit run app.py
```

### Docker Deployment
```dockerfile
FROM python:3.11-slim
COPY . /app
WORKDIR /app
RUN pip install uv && uv sync
CMD ["uv", "run", "streamlit", "run", "app.py", "--server.address", "0.0.0.0"]
```

### Cloud Deployment
- Streamlit Cloud: Direct deployment from GitHub
- Heroku: Container deployment
- Railway: Simple Python app deployment

## Future Enhancements

1. **Real-time Streaming**: Stream responses as they're generated
2. **Voice Input**: Speech-to-text integration
3. **Collaborative Features**: Multiple users, shared conversations
4. **Plugin System**: Custom tool integration
5. **Advanced Visualization**: Charts, graphs, diagrams for tool outputs
6. **Mobile Responsive**: Touch-friendly interface
7. **Dark Mode**: Theme customization
8. **Conversation Analytics**: Usage statistics, token usage tracking

## Implementation Priority

**Phase 1** (MVP): Basic chat with kodelet integration
**Phase 2** (Core): Conversation management via API
**Phase 3** (Enhanced): Tool visualization and advanced features
**Phase 4** (Production): Polish, deployment, documentation

## Implementation Improvements (v2.0)

### Real-time Streaming Architecture

The chatbot has been upgraded from simple CLI output parsing to a sophisticated real-time streaming system:

#### Original Approach (v1.0)
```python
# Old: Brittle text parsing
response, conv_id = client.run_query(query, conversation_id)
```

#### New Approach (v2.0) 
```python
# New: Generator-based streaming with API polling
for message in client.run_query(query, conversation_id):
    # Messages arrive incrementally as kodelet generates them
    display_message(message)
```

### Technical Improvements

1. **Robust Conversation ID Discovery**
   - New conversations: Uses `kodelet conversation list --limit 1 --json`
   - Resumed conversations: Uses existing ID with message offset
   - No more regex parsing of CLI output

2. **Real-time Message Streaming**
   - Polls `/api/conversations/{id}` every second
   - Yields messages as they appear in the conversation
   - Handles thinking text, tool calls, and multi-part responses

3. **Better Error Handling** 
   - Graceful handling of API failures
   - Timeout protection (30 seconds without changes)
   - Process lifecycle management

4. **Enhanced User Experience**
   - Messages appear as kodelet generates them ✅
   - Better status indicators ("🤖 Kodelet is thinking...") ✅  
   - Improved error feedback and recovery ✅
   - **FIXED**: Real-time UI updates using `st.empty()` containers ✅
   - **NEW**: Live message accumulation and progress tracking ✅

### Critical Fix: Real-time UI Updates

**Problem Identified**: The original v2.0 implementation collected all streaming messages internally before updating the UI, which defeated the purpose of real-time streaming.

**Solution Implemented**:
```python
# Before: Messages collected invisibly, UI updated once at end
for message in generator:
    process_message(message)  # Internal processing only
# st.rerun()  # UI updates only at the end

# After: Messages streamed to UI in real-time
accumulated_content = ""
for message in generator:
    process_message(message)  # Add to session state
    accumulated_content += message.content
    # Update streaming container immediately
    with streaming_container.container():
        with st.chat_message("assistant"):
            st.markdown(accumulated_content)  # Live update!
    status_container.success(f"📝 Receiving... ({count} parts)")
```

**Key Technical Improvements**:
- Moved streaming handling from conversation manager to UI layer
- Used `st.empty()` containers for real-time content updates  
- Separated session state management from UI rendering
- Added live progress indicators and message counters
- Eliminated blocking behavior that prevented real-time updates

**Critical Fix: Streaming Message Location**

**Problem Identified**: Streaming messages appeared below the input box instead of integrating into the main conversation thread, causing poor UX where messages would "jump up" after completion.

**Solution Implemented**:
```python
# BEFORE: Streaming in input area (wrong location)
def render_input_area():
    user_input = st.chat_input()
    if user_input:
        # Streaming containers created here (below input)
        streaming_container = st.empty()
        for message in generator:
            # Messages appear below input box ❌
            streaming_container.update(content)

# AFTER: Streaming in main chat interface (correct location)  
def render_chat_interface():
    # Display existing messages
    for message in session_state.messages:
        render_message(message)
    
    # Handle streaming in main chat area
    if session_state.streaming_active:
        render_streaming_response()  # ✅ Appears in conversation thread

def render_input_area():
    user_input = st.chat_input()
    if user_input:
        # Just setup streaming state, don't do UI updates
        session_state.message_generator = get_generator()
        session_state.streaming_active = True
        st.rerun()  # Let main interface handle streaming
```

**Key Architectural Changes**:
- **Separated Concerns**: Input handling vs. streaming display
- **Session State**: Streaming state managed via `st.session_state`
- **Correct Layout**: Streaming happens in main chat area, not input area
- **Proper Flow**: Input → State Setup → Main Interface Streaming

**Result**: Streaming messages now appear seamlessly in the main conversation thread where users expect them! 🎯

## Summary of Implementation Evolution

### v1.0 (Initial Implementation)
- ❌ Brittle CLI output parsing with regex
- ❌ Blocking subprocess calls
- ❌ Messages only appeared after completion
- ❌ Poor error handling and recovery

### v2.0 (Real-time Streaming) 
- ✅ Generator-based streaming architecture
- ✅ API polling for robust conversation discovery
- ✅ Real-time message streaming with live updates
- ❌ Messages appeared below input box (poor UX)

### v2.1 (Fixed Layout + Polish)
- ✅ All v2.0 improvements retained
- ✅ **FIXED**: Streaming messages appear in main conversation thread
- ✅ Proper UI architecture with separated concerns
- ✅ Session state management for streaming flow
- ✅ Clean input handling and file management
- ✅ Professional chat experience with proper message flow

### Technical Architecture (Final)
```
User Input (render_input_area)
    ↓
Set Session State (streaming_active=True)
    ↓
st.rerun() → Main Chat Interface (render_chat_interface)
    ↓
Streaming Response (render_streaming_response)
    ↓
Real-time Updates in Main Chat Thread
    ↓
Finalize & Clean State
```

This provides the most intuitive and responsive chat experience, with messages appearing exactly where users expect them in the conversation flow! 🚀

### Performance Benefits

- **Lower Latency**: Messages appear immediately when generated
- **More Reliable**: Uses structured API instead of text parsing  
- **Better Resource Usage**: Background process management
- **Scalable**: Can handle long-running conversations efficiently

This approach provides a much more professional and responsive chat experience while maintaining full compatibility with kodelet's features.