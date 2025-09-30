# OpenAI Response API - Go SDK Deep Dive

## Table of Contents

1. [Overview](#overview)
2. [Key Differences from Chat Completion API](#key-differences-from-chat-completion-api)
3. [Basic Usage](#basic-usage)
4. [Multi-Turn Conversations](#multi-turn-conversations)
5. [Tool Calling](#tool-calling)
6. [Multimodal Inputs](#multimodal-inputs)
7. [Streaming Responses](#streaming-responses)
8. [Advanced Features](#advanced-features)
9. [Response Structure](#response-structure)
10. [Best Practices](#best-practices)

## Overview

The OpenAI Response API is a unified interface for creating model responses that supports text, images, files, and audio inputs. It provides a more flexible and powerful alternative to the Chat Completion API with built-in support for:

- **Stateful conversations** with automatic context management
- **Multimodal inputs** (text, images, audio, files)
- **Built-in tools** (file search, web search, code interpreter, computer use)
- **Function calling** with strongly-typed arguments
- **MCP (Model Context Protocol) integrations**
- **Background processing** for long-running tasks
- **Streaming responses** with fine-grained events

**API Endpoint**: `POST /responses`

**Go Package**: `github.com/openai/openai-go/v2/responses`

## Key Differences from Chat Completion API

| Feature | Chat Completion API | Response API |
|---------|-------------------|--------------|
| **Input Format** | Messages array | Flexible input items (text, images, files, tool outputs) |
| **Conversation Management** | Manual | Automatic with `conversation` parameter |
| **State Persistence** | None | Built-in with `PreviousResponseID` or `conversation` |
| **Tool Support** | Functions only | Functions + built-in tools + MCP |
| **Multimodal** | Limited | Full support for images, audio, files |
| **Output** | Single message | Structured output items with metadata |
| **Background Processing** | No | Yes, with `background: true` |

## Basic Usage

### Simple Text Response

```go
package main

import (
    "context"
    "fmt"

    "github.com/openai/openai-go/v2"
    "github.com/openai/openai-go/v2/responses"
)

func main() {
    client := openai.NewClient()
    ctx := context.Background()

    resp, err := client.Responses.New(ctx, responses.ResponseNewParams{
        Input: responses.ResponseNewParamsInputUnion{
            OfString: openai.String("Write me a haiku about computers"),
        },
        Model: openai.ChatModelGPT4o,
    })

    if err != nil {
        panic(err)
    }

    // Simple way to get text output
    fmt.Println(resp.OutputText())
}
```

### With System Instructions

```go
resp, err := client.Responses.New(ctx, responses.ResponseNewParams{
    Input: responses.ResponseNewParamsInputUnion{
        OfString: openai.String("Tell me about Go programming"),
    },
    Instructions: openai.String("You are a helpful Go programming expert. Keep responses concise."),
    Model: openai.ChatModelGPT4o,
    Temperature: openai.Float(0.7),
    MaxOutputTokens: openai.Int(1000),
})
```

### Using Structured Input Items

```go
// Create message input items
inputItems := []responses.ResponseInputItemUnionParam{
    responses.ResponseInputItemParamOfMessage(
        "What is the weather like today?",
        responses.EasyInputMessageRoleUser,
    ),
}

resp, err := client.Responses.New(ctx, responses.ResponseNewParams{
    Input: responses.ResponseNewParamsInputUnion{
        OfInputItemList: inputItems,
    },
    Model: openai.ChatModelGPT4o,
})
```

## Multi-Turn Conversations

The Response API provides two approaches for managing multi-turn conversations:

### Approach 1: Using `PreviousResponseID` (Stateless)

This approach manually tracks response IDs to build conversation history:

```go
package main

import (
    "context"
    "fmt"

    "github.com/openai/openai-go/v2"
    "github.com/openai/openai-go/v2/responses"
)

func main() {
    client := openai.NewClient()
    ctx := context.Background()

    // First turn
    resp1, err := client.Responses.New(ctx, responses.ResponseNewParams{
        Input: responses.ResponseNewParamsInputUnion{
            OfString: openai.String("What is the capital of France?"),
        },
        Model: openai.ChatModelGPT4o,
        Store: openai.Bool(true), // Required to retrieve later
    })
    if err != nil {
        panic(err)
    }

    fmt.Println("First response:", resp1.OutputText())
    fmt.Println("Response ID:", resp1.ID)

    // Second turn - reference the previous response
    resp2, err := client.Responses.New(ctx, responses.ResponseNewParams{
        Input: responses.ResponseNewParamsInputUnion{
            OfString: openai.String("What is the population of that city?"),
        },
        PreviousResponseID: openai.String(resp1.ID),
        Model: openai.ChatModelGPT4o,
        Store: openai.Bool(true),
    })
    if err != nil {
        panic(err)
    }

    fmt.Println("Second response:", resp2.OutputText())

    // Third turn
    resp3, err := client.Responses.New(ctx, responses.ResponseNewParams{
        Input: responses.ResponseNewParamsInputUnion{
            OfString: openai.String("What are some famous landmarks there?"),
        },
        PreviousResponseID: openai.String(resp2.ID),
        Model: openai.ChatModelGPT4o,
    })
    if err != nil {
        panic(err)
    }

    fmt.Println("Third response:", resp3.OutputText())
}
```

**Note**: When using `PreviousResponseID`:
- System instructions from the previous response are **not** carried over
- Use `Instructions` parameter to swap system messages between turns
- Set `Store: true` if you need to retrieve responses later via API

### Approach 2: Using `Conversation` (Stateful)

This approach uses a persistent conversation object that automatically manages history:

```go
package main

import (
    "context"
    "fmt"

    "github.com/openai/openai-go/v2"
    "github.com/openai/openai-go/v2/responses"
)

func main() {
    client := openai.NewClient()
    ctx := context.Background()

    // Create or use existing conversation ID
    conversationID := "conv_abc123" // Or get from previous response

    // First turn
    resp1, err := client.Responses.New(ctx, responses.ResponseNewParams{
        Input: responses.ResponseNewParamsInputUnion{
            OfString: openai.String("Tell me about neural networks"),
        },
        Conversation: responses.ResponseNewParamsConversationUnion{
            OfString: openai.String(conversationID),
        },
        Model: openai.ChatModelGPT4o,
    })
    if err != nil {
        panic(err)
    }

    fmt.Println("Response 1:", resp1.OutputText())

    // Subsequent turns - items are automatically added to the conversation
    resp2, err := client.Responses.New(ctx, responses.ResponseNewParams{
        Input: responses.ResponseNewParamsInputUnion{
            OfString: openai.String("What are some common architectures?"),
        },
        Conversation: responses.ResponseNewParamsConversationUnion{
            OfString: openai.String(conversationID),
        },
        Model: openai.ChatModelGPT4o,
    })
    if err != nil {
        panic(err)
    }

    fmt.Println("Response 2:", resp2.OutputText())

    // List conversation items
    items, err := client.Responses.InputItems.List(ctx, resp2.ID, 
        responses.InputItemListParams{
            Limit: openai.Int(100),
        },
    )
    if err != nil {
        panic(err)
    }

    fmt.Printf("Conversation has %d items\n", len(items.Data))
}
```

**Benefits of Conversation approach**:
- Automatic context management
- Input and output items automatically added to conversation
- Can retrieve conversation history via `InputItems.List()`
- Cannot be used with `PreviousResponseID` (mutually exclusive)

### Truncation Strategy

When conversation history exceeds the model's context window:

```go
resp, err := client.Responses.New(ctx, responses.ResponseNewParams{
    Input: responses.ResponseNewParamsInputUnion{
        OfString: openai.String("Continue our discussion"),
    },
    PreviousResponseID: openai.String(previousID),
    Truncation: responses.ResponseNewParamsTruncationAuto, // Drop old items
    Model: openai.ChatModelGPT4o,
})
```

Options:
- `ResponseNewParamsTruncationAuto`: Automatically drops items from the beginning
- `ResponseNewParamsTruncationDisabled`: Fails with 400 error (default)

## Tool Calling

The Response API supports three categories of tools:

1. **Built-in tools**: File search, web search, code interpreter, computer use
2. **Function calls (custom tools)**: Your own functions with typed arguments
3. **MCP Tools**: Third-party integrations via Model Context Protocol

### Function Calling

```go
package main

import (
    "context"
    "encoding/json"
    "fmt"

    "github.com/openai/openai-go/v2"
    "github.com/openai/openai-go/v2/responses"
    "github.com/openai/openai-go/v2/shared/constant"
)

// Define tool
func getWeatherTool() responses.ToolUnionParam {
    return responses.ToolUnionParam{
        OfFunction: &responses.FunctionToolParam{
            Name: "get_weather",
            Description: openai.String("Get the current weather in a given location"),
            Parameters: map[string]any{
                "type": "object",
                "properties": map[string]any{
                    "location": map[string]any{
                        "type":        "string",
                        "description": "The city and state, e.g. San Francisco, CA",
                    },
                    "unit": map[string]any{
                        "type": "string",
                        "enum": []string{"celsius", "fahrenheit"},
                    },
                },
                "required": []string{"location"},
            },
            Strict: openai.Bool(true), // Enable strict parameter validation
        },
    }
}

// Simulate weather API
func getWeather(location, unit string) string {
    return fmt.Sprintf(`{"location": "%s", "temperature": "22", "unit": "%s", "condition": "sunny"}`, 
        location, unit)
}

func main() {
    client := openai.NewClient()
    ctx := context.Background()

    // Initial request with tool
    resp, err := client.Responses.New(ctx, responses.ResponseNewParams{
        Input: responses.ResponseNewParamsInputUnion{
            OfString: openai.String("What's the weather like in San Francisco?"),
        },
        Model: openai.ChatModelGPT4o,
        Tools: []responses.ToolUnionParam{getWeatherTool()},
    })
    if err != nil {
        panic(err)
    }

    // Process output items
    for _, item := range resp.Output {
        // Check for function call
        if item.Type == "function_call" {
            functionCall := item.AsFunctionToolCall()
            
            fmt.Printf("Function call: %s\n", functionCall.Name)
            fmt.Printf("Arguments: %s\n", functionCall.Arguments)

            // Parse arguments
            var args map[string]string
            json.Unmarshal([]byte(functionCall.Arguments), &args)

            // Execute function
            result := getWeather(args["location"], args["unit"])
            
            // Continue conversation with function result
            inputItems := []responses.ResponseInputItemUnionParam{
                // Add original assistant response
                responses.ResponseInputItemParamOfOutputMessage(
                    item.Content,
                    item.ID,
                    item.Status,
                ),
                // Add function call output
                responses.ResponseInputItemParamOfFunctionCallOutput(
                    functionCall.CallID,
                    result,
                ),
            }

            resp2, err := client.Responses.New(ctx, responses.ResponseNewParams{
                Input: responses.ResponseNewParamsInputUnion{
                    OfInputItemList: inputItems,
                },
                PreviousResponseID: openai.String(resp.ID),
                Model: openai.ChatModelGPT4o,
            })
            if err != nil {
                panic(err)
            }

            fmt.Println("Final response:", resp2.OutputText())
        }
    }
}
```

### Parallel Tool Calls

Enable the model to call multiple tools simultaneously:

```go
resp, err := client.Responses.New(ctx, responses.ResponseNewParams{
    Input: responses.ResponseNewParamsInputUnion{
        OfString: openai.String("What's the weather in SF and NYC?"),
    },
    Tools: []responses.ToolUnionParam{getWeatherTool()},
    ParallelToolCalls: openai.Bool(true), // Allow parallel execution
    Model: openai.ChatModelGPT4o,
})
```

### Tool Choice

Control which tools the model can use:

```go
// Auto mode (default) - model decides
resp, err := client.Responses.New(ctx, responses.ResponseNewParams{
    Input: /* ... */,
    Tools: []responses.ToolUnionParam{tool1, tool2},
    ToolChoice: responses.ResponseNewParamsToolChoiceUnion{
        OfToolChoiceMode: openai.Opt(responses.ToolChoiceOptionsAuto),
    },
    Model: openai.ChatModelGPT4o,
})

// Required mode - force tool use
resp, err := client.Responses.New(ctx, responses.ResponseNewParams{
    Input: /* ... */,
    Tools: []responses.ToolUnionParam{tool1},
    ToolChoice: responses.ResponseNewParamsToolChoiceUnion{
        OfToolChoiceMode: openai.Opt(responses.ToolChoiceOptionsRequired),
    },
    Model: openai.ChatModelGPT4o,
})

// None mode - disable all tools
resp, err := client.Responses.New(ctx, responses.ResponseNewParams{
    Input: /* ... */,
    Tools: []responses.ToolUnionParam{tool1},
    ToolChoice: responses.ResponseNewParamsToolChoiceUnion{
        OfToolChoiceMode: openai.Opt(responses.ToolChoiceOptionsNone),
    },
    Model: openai.ChatModelGPT4o,
})

// Force specific function
resp, err := client.Responses.New(ctx, responses.ResponseNewParams{
    Input: /* ... */,
    Tools: []responses.ToolUnionParam{tool1, tool2},
    ToolChoice: responses.ResponseNewParamsToolChoiceUnion{
        OfFunctionTool: &responses.ToolChoiceFunctionParam{
            Name: "get_weather",
            Type: constant.Function,
        },
    },
    Model: openai.ChatModelGPT4o,
})
```

### Built-in Tools

#### File Search Tool

```go
resp, err := client.Responses.New(ctx, responses.ResponseNewParams{
    Input: responses.ResponseNewParamsInputUnion{
        OfString: openai.String("What does the quarterly report say about revenue?"),
    },
    Tools: []responses.ToolUnionParam{{
        OfFileSearch: &responses.FileSearchToolParam{
            VectorStoreIDs: []string{"vs_abc123"}, // Your vector store IDs
            MaxNumResults: openai.Int(5),
            RankingOptions: responses.FileSearchToolRankingOptionsParam{
                ScoreThreshold: openai.Float(0.7),
                Ranker: "default-2024-11-15",
            },
        },
    }},
    Model: openai.ChatModelGPT4o,
})
```

#### Web Search Tool

```go
resp, err := client.Responses.New(ctx, responses.ResponseNewParams{
    Input: responses.ResponseNewParamsInputUnion{
        OfString: openai.String("What are the latest developments in quantum computing?"),
    },
    Tools: []responses.ToolUnionParam{{
        OfWebSearch: &responses.WebSearchToolParam{
            Filters: responses.WebSearchToolFiltersParam{
                AllowedDomains: []string{"arxiv.org", "nature.com"},
            },
            UserLocation: responses.WebSearchToolUserLocationParam{
                Country: openai.String("US"),
                City: openai.String("San Francisco"),
            },
        },
    }},
    Model: openai.ChatModelGPT4o,
})
```

#### Code Interpreter

```go
resp, err := client.Responses.New(ctx, responses.ResponseNewParams{
    Input: responses.ResponseNewParamsInputUnion{
        OfString: openai.String("Calculate the fibonacci sequence for n=10"),
    },
    Tools: []responses.ToolUnionParam{{
        OfCodeInterpreter: &responses.ToolCodeInterpreterParam{},
    }},
    Include: []responses.ResponseIncludable{
        responses.ResponseIncludableCodeInterpreterCallOutputs, // Include code outputs
    },
    Model: openai.ChatModelGPT4o,
})
```

#### Computer Use Tool

```go
resp, err := client.Responses.New(ctx, responses.ResponseNewParams{
    Input: responses.ResponseNewParamsInputUnion{
        OfString: openai.String("Click the submit button"),
    },
    Tools: []responses.ToolUnionParam{{
        OfComputer: &responses.ComputerToolParam{
            DisplayWidth: 1920,
            DisplayHeight: 1080,
            Environment: responses.ComputerToolEnvironmentBrowser,
        },
    }},
    Model: openai.ChatModelGPT4o,
})
```

### Custom Tools

Define tools with custom input formats:

```go
customTool := responses.ToolUnionParam{
    OfCustom: &responses.CustomToolParam{
        Name: "process_data",
        Description: openai.String("Process data with custom format"),
        Format: shared.CustomToolInputFormatUnionParam{
            OfJSONSchema: &shared.CustomToolInputFormatJSONSchemaParam{
                JSONSchema: map[string]any{
                    "type": "object",
                    "properties": map[string]any{
                        "data": map[string]any{"type": "string"},
                    },
                },
            },
        },
    },
}
```

## Multimodal Inputs

The Response API provides comprehensive support for images, audio, and files.

### Image Inputs

```go
package main

import (
    "context"
    "fmt"

    "github.com/openai/openai-go/v2"
    "github.com/openai/openai-go/v2/responses"
)

func main() {
    client := openai.NewClient()
    ctx := context.Background()

    // Create input with image
    inputItems := []responses.ResponseInputItemUnionParam{
        responses.ResponseInputItemParamOfInputMessage(
            responses.ResponseInputMessageContentListParam{
                {
                    OfInputText: &responses.ResponseInputTextParam{
                        Text: "What's in this image?",
                    },
                },
                {
                    OfInputImage: &responses.ResponseInputImageParam{
                        InputImage: responses.ResponseInputImageInputImageParam{
                            // Option 1: URL
                            ImageURL: openai.String("https://example.com/image.jpg"),
                            
                            // Option 2: Base64 encoded image
                            // Data: openai.String("data:image/jpeg;base64,/9j/4AAQSkZJRg..."),
                            
                            // Option 3: File ID from uploaded file
                            // FileID: openai.String("file-abc123"),
                        },
                        Detail: responses.ResponseInputImageDetailHigh, // or Low, Auto
                    },
                },
            },
            "user",
        ),
    }

    resp, err := client.Responses.New(ctx, responses.ResponseNewParams{
        Input: responses.ResponseNewParamsInputUnion{
            OfInputItemList: inputItems,
        },
        Model: openai.ChatModelGPT4o,
        Include: []responses.ResponseIncludable{
            responses.ResponseIncludableMessageInputImageImageURL, // Include image URLs in response
        },
    })
    if err != nil {
        panic(err)
    }

    fmt.Println(resp.OutputText())
}
```

**Image Detail Levels**:
- `ResponseInputImageDetailLow`: Faster, less detailed analysis (512x512 representation)
- `ResponseInputImageDetailHigh`: More detailed analysis, higher cost
- `ResponseInputImageDetailAuto`: Model decides based on image

### Multiple Images

```go
inputItems := []responses.ResponseInputItemUnionParam{
    responses.ResponseInputItemParamOfInputMessage(
        responses.ResponseInputMessageContentListParam{
            {
                OfInputText: &responses.ResponseInputTextParam{
                    Text: "Compare these two images",
                },
            },
            {
                OfInputImage: &responses.ResponseInputImageParam{
                    InputImage: responses.ResponseInputImageInputImageParam{
                        ImageURL: openai.String("https://example.com/image1.jpg"),
                    },
                },
            },
            {
                OfInputImage: &responses.ResponseInputImageParam{
                    InputImage: responses.ResponseInputImageInputImageParam{
                        ImageURL: openai.String("https://example.com/image2.jpg"),
                    },
                },
            },
        },
        "user",
    ),
}
```

### Audio Inputs

```go
inputItems := []responses.ResponseInputItemUnionParam{
    responses.ResponseInputItemParamOfInputMessage(
        responses.ResponseInputMessageContentListParam{
            {
                OfInputText: &responses.ResponseInputTextParam{
                    Text: "Transcribe and summarize this audio",
                },
            },
            {
                OfInputAudio: &responses.ResponseInputAudioParam{
                    InputAudio: responses.ResponseInputAudioInputAudioParam{
                        // Base64 encoded audio data
                        Data: "base64_encoded_audio_data",
                        Format: responses.ResponseInputAudioInputAudioParamFormatMp3, // or Wav
                    },
                },
            },
        },
        "user",
    ),
}

resp, err := client.Responses.New(ctx, responses.ResponseNewParams{
    Input: responses.ResponseNewParamsInputUnion{
        OfInputItemList: inputItems,
    },
    Model: openai.ChatModelGPT4o,
})
```

### File Inputs (PDF, Documents)

```go
inputItems := []responses.ResponseInputItemUnionParam{
    responses.ResponseInputItemParamOfInputMessage(
        responses.ResponseInputMessageContentListParam{
            {
                OfInputText: &responses.ResponseInputTextParam{
                    Text: "Summarize this document",
                },
            },
            {
                OfInputFile: &responses.ResponseInputFileParam{
                    // File ID from upload
                    FileID: "file-abc123",
                    
                    // Or inline file content
                    // Content: openai.String("base64_encoded_content"),
                    // Filename: openai.String("document.pdf"),
                },
            },
        },
        "user",
    ),
}

resp, err := client.Responses.New(ctx, responses.ResponseNewParams{
    Input: responses.ResponseNewParamsInputUnion{
        OfInputItemList: inputItems,
    },
    Model: openai.ChatModelGPT4o,
})
```

### Mixed Multimodal Input

```go
inputItems := []responses.ResponseInputItemUnionParam{
    responses.ResponseInputItemParamOfInputMessage(
        responses.ResponseInputMessageContentListParam{
            {
                OfInputText: &responses.ResponseInputTextParam{
                    Text: "Analyze this data from multiple sources",
                },
            },
            {
                OfInputImage: &responses.ResponseInputImageParam{
                    InputImage: responses.ResponseInputImageInputImageParam{
                        ImageURL: openai.String("https://example.com/chart.png"),
                    },
                },
            },
            {
                OfInputFile: &responses.ResponseInputFileParam{
                    FileID: "file-spreadsheet123",
                },
            },
            {
                OfInputAudio: &responses.ResponseInputAudioParam{
                    InputAudio: responses.ResponseInputAudioInputAudioParam{
                        Data: "base64_audio_data",
                        Format: responses.ResponseInputAudioInputAudioParamFormatMp3,
                    },
                },
            },
        },
        "user",
    ),
}
```

## Streaming Responses

Streaming provides real-time access to response generation with fine-grained events.

### Basic Streaming

```go
package main

import (
    "context"
    "fmt"

    "github.com/openai/openai-go/v2"
    "github.com/openai/openai-go/v2/responses"
)

func main() {
    client := openai.NewClient()
    ctx := context.Background()

    stream := client.Responses.NewStreaming(ctx, responses.ResponseNewParams{
        Input: responses.ResponseNewParamsInputUnion{
            OfString: openai.String("Tell me about quantum computing"),
        },
        Model: openai.ChatModelGPT4o,
    })

    var completeText string

    for stream.Next() {
        event := stream.Current()

        // Print delta as it arrives
        if event.Delta != "" {
            fmt.Print(event.Delta)
        }

        // Check for completion
        if event.JSON.Text.Valid() {
            fmt.Println("\n\nFinished Content")
            completeText = event.Text
            break
        }
    }

    if err := stream.Err(); err != nil {
        panic(err)
    }

    fmt.Printf("\nComplete text: %s\n", completeText)
}
```

### Handling Stream Events

The Response API provides detailed streaming events for different stages:

```go
stream := client.Responses.NewStreaming(ctx, responses.ResponseNewParams{
    Input: responses.ResponseNewParamsInputUnion{
        OfString: openai.String("Write a story"),
    },
    Model: openai.ChatModelGPT4o,
})

for stream.Next() {
    event := stream.Current()

    // Use AsAny() to switch on event type
    switch e := event.AsAny().(type) {
    case responses.ResponseCreatedEvent:
        fmt.Printf("Response created: %s\n", e.Response.ID)

    case responses.ResponseInProgressEvent:
        fmt.Println("Response generation in progress...")

    case responses.ResponseTextDeltaEvent:
        fmt.Print(e.Delta) // Print text as it arrives

    case responses.ResponseTextDoneEvent:
        fmt.Printf("\nText complete: %s\n", e.Text)

    case responses.ResponseCompletedEvent:
        fmt.Printf("Response completed. Status: %s\n", e.Response.Status)
        fmt.Printf("Usage: %+v\n", e.Response.Usage)

    case responses.ResponseErrorEvent:
        fmt.Printf("Error: %+v\n", e.Error)

    case responses.ResponseFailedEvent:
        fmt.Printf("Response failed: %+v\n", e.Response.Error)
    }
}
```

### Stream Event Types

#### Core Events
- `ResponseCreatedEvent`: Response object created
- `ResponseInProgressEvent`: Generation started
- `ResponseCompletedEvent`: Generation finished successfully
- `ResponseFailedEvent`: Generation failed
- `ResponseErrorEvent`: Error occurred
- `ResponseIncompleteEvent`: Response incomplete (e.g., max tokens reached)

#### Content Events
- `ResponseTextDeltaEvent`: Partial text content
- `ResponseTextDoneEvent`: Text content complete
- `ResponseRefusalDeltaEvent`: Partial refusal message
- `ResponseRefusalDoneEvent`: Refusal complete
- `ResponseContentPartAddedEvent`: New content part started
- `ResponseContentPartDoneEvent`: Content part finished

#### Tool Events
- `ResponseFunctionCallArgumentsDeltaEvent`: Function arguments being streamed
- `ResponseFunctionCallArgumentsDoneEvent`: Function arguments complete
- `ResponseFileSearchCallInProgressEvent`: File search in progress
- `ResponseFileSearchCallSearchingEvent`: Actively searching files
- `ResponseFileSearchCallCompletedEvent`: File search complete
- `ResponseWebSearchCallInProgressEvent`: Web search in progress
- `ResponseWebSearchCallCompletedEvent`: Web search complete
- `ResponseCodeInterpreterCallCodeDeltaEvent`: Code being generated
- `ResponseCodeInterpreterCallCodeDoneEvent`: Code generation complete
- `ResponseCodeInterpreterCallInterpretingEvent`: Code executing
- `ResponseCodeInterpreterCallCompletedEvent`: Execution complete

#### Reasoning Events (for o-series models)
- `ResponseReasoningTextDeltaEvent`: Reasoning tokens being generated
- `ResponseReasoningTextDoneEvent`: Reasoning complete
- `ResponseReasoningSummaryPartAddedEvent`: Reasoning summary part added
- `ResponseReasoningSummaryTextDeltaEvent`: Summary text delta
- `ResponseReasoningSummaryTextDoneEvent`: Summary complete
- `ResponseReasoningSummaryPartDoneEvent`: Summary part finished

#### Output Events
- `ResponseOutputItemAddedEvent`: New output item added
- `ResponseOutputItemDoneEvent`: Output item complete
- `ResponseAudioDeltaEvent`: Audio response delta
- `ResponseAudioDoneEvent`: Audio response complete
- `ResponseAudioTranscriptDeltaEvent`: Audio transcript delta
- `ResponseAudioTranscriptDoneEvent`: Audio transcript complete

### Streaming with Tools

```go
stream := client.Responses.NewStreaming(ctx, responses.ResponseNewParams{
    Input: responses.ResponseNewParamsInputUnion{
        OfString: openai.String("What's the weather in SF?"),
    },
    Tools: []responses.ToolUnionParam{getWeatherTool()},
    Model: openai.ChatModelGPT4o,
})

var functionName string
var arguments string

for stream.Next() {
    event := stream.Current()

    switch e := event.AsAny().(type) {
    case responses.ResponseFunctionCallArgumentsDeltaEvent:
        // Arguments being streamed
        arguments += e.Delta
        fmt.Printf("Args delta: %s\n", e.Delta)

    case responses.ResponseFunctionCallArgumentsDoneEvent:
        // Arguments complete
        functionName = e.Name
        arguments = e.Arguments
        fmt.Printf("\nFunction: %s\nArgs: %s\n", functionName, arguments)
        // Execute function and continue conversation...
    }
}
```

### Retrieving Streaming Response Later

```go
// Get a streaming response that was previously generated
stream := client.Responses.GetStreaming(ctx, responseID, 
    responses.ResponseGetParams{
        StartingAfter: openai.Int(0), // Start from beginning
        Include: []responses.ResponseIncludable{
            responses.ResponseIncludableMessageOutputTextLogprobs,
        },
    },
)

for stream.Next() {
    event := stream.Current()
    // Process events...
}
```

### Stream Obfuscation

For enhanced security, enable stream obfuscation to normalize payload sizes:

```go
resp, err := client.Responses.New(ctx, responses.ResponseNewParams{
    Input: /* ... */,
    Model: openai.ChatModelGPT4o,
    StreamOptions: responses.ResponseNewParamsStreamOptions{
        IncludeObfuscation: openai.Bool(true), // Enable obfuscation
    },
})
```

## Advanced Features

### Background Processing

For long-running tasks, run responses in the background:

```go
resp, err := client.Responses.New(ctx, responses.ResponseNewParams{
    Input: responses.ResponseNewParamsInputUnion{
        OfString: openai.String("Analyze this large dataset..."),
    },
    Background: openai.Bool(true), // Run in background
    Model: openai.ChatModelGPT4o,
})
if err != nil {
    panic(err)
}

fmt.Printf("Response ID: %s\n", resp.ID)
fmt.Printf("Status: %s\n", resp.Status) // Will be "queued" or "in_progress"

// Poll for completion
for {
    time.Sleep(5 * time.Second)
    
    status, err := client.Responses.Get(ctx, resp.ID, responses.ResponseGetParams{})
    if err != nil {
        panic(err)
    }

    fmt.Printf("Status: %s\n", status.Status)
    
    if status.Status == "completed" {
        fmt.Println("Result:", status.OutputText())
        break
    } else if status.Status == "failed" {
        fmt.Printf("Error: %+v\n", status.Error)
        break
    }
}

// Cancel a background response
cancelled, err := client.Responses.Cancel(ctx, resp.ID)
```

### Structured Outputs (JSON Schema)

Force the model to produce JSON conforming to a schema:

```go
type WeatherReport struct {
    Location    string   `json:"location"`
    Temperature int      `json:"temperature"`
    Conditions  []string `json:"conditions"`
    Forecast    string   `json:"forecast"`
}

// Generate JSON schema (using github.com/invopop/jsonschema)
reflector := jsonschema.Reflector{
    AllowAdditionalProperties: false,
    DoNotReference:            true,
}
schema := reflector.Reflect(WeatherReport{})

resp, err := client.Responses.New(ctx, responses.ResponseNewParams{
    Input: responses.ResponseNewParamsInputUnion{
        OfString: openai.String("Give me a weather report for Paris"),
    },
    Text: responses.ResponseTextConfigParam{
        Format: responses.ResponseFormatTextConfigUnionParam{
            OfJSONSchema: &responses.ResponseFormatTextJSONSchemaConfigParam{
                JSONSchema: responses.ResponseFormatTextJSONSchemaParam{
                    Name:        "weather_report",
                    Description: openai.String("A structured weather report"),
                    Schema:      schema,
                    Strict:      openai.Bool(true), // Strict schema adherence
                },
            },
        },
    },
    Model: openai.ChatModelGPT4o,
})

// Parse the JSON output
var report WeatherReport
json.Unmarshal([]byte(resp.OutputText()), &report)
```

### Prompt Caching

Optimize cache hit rates for similar requests:

```go
resp, err := client.Responses.New(ctx, responses.ResponseNewParams{
    Input: responses.ResponseNewParamsInputUnion{
        OfString: openai.String("Analyze this document"),
    },
    Instructions: openai.String("You are a document analyzer..."),
    PromptCacheKey: openai.String("doc-analyzer-v1"), // Cache key
    Model: openai.ChatModelGPT4o,
})
```

### Reasoning Models (o-series)

Configure reasoning effort for o-series models:

```go
resp, err := client.Responses.New(ctx, responses.ResponseNewParams{
    Input: responses.ResponseNewParamsInputUnion{
        OfString: openai.String("Solve this complex math problem..."),
    },
    Model: shared.ResponsesModelO1,
    Reasoning: shared.ReasoningParam{
        Effort: shared.ReasoningEffortHigh, // minimal, low, medium, high
        GenerateSummary: shared.ReasoningGenerateSummaryAuto,
        Summary: shared.ReasoningSummaryAuto,
    },
    Include: []responses.ResponseIncludable{
        responses.ResponseIncludableReasoningEncryptedContent, // Include encrypted reasoning
    },
})
```

### Service Tiers

Control processing priority and pricing:

```go
resp, err := client.Responses.New(ctx, responses.ResponseNewParams{
    Input: /* ... */,
    ServiceTier: responses.ResponseNewParamsServiceTierFlex, // auto, default, flex, scale, priority
    Model: openai.ChatModelGPT4o,
})

// Check actual service tier used
fmt.Printf("Service tier: %s\n", resp.ServiceTier)
```

Options:
- `auto`: Use project default
- `default`: Standard pricing/performance
- `flex`: Lower cost, best-effort
- `scale`: Higher throughput
- `priority`: Guaranteed capacity, premium pricing

### Metadata and Safety

```go
resp, err := client.Responses.New(ctx, responses.ResponseNewParams{
    Input: /* ... */,
    Model: openai.ChatModelGPT4o,
    
    // Custom metadata
    Metadata: shared.Metadata{
        "user_id": "user123",
        "session": "sess_abc",
        "environment": "production",
    },
    
    // Safety identifiers
    SafetyIdentifier: openai.String("hashed_user_id_12345"),
    
    // Store response for later retrieval
    Store: openai.Bool(true),
})

// Access metadata in response
fmt.Printf("Metadata: %+v\n", resp.Metadata)
```

### Response Management

```go
// Delete a response
err := client.Responses.Delete(ctx, responseID)

// List input items
items, err := client.Responses.InputItems.List(ctx, responseID, 
    responses.InputItemListParams{
        Limit: openai.Int(20),
        Order: responses.InputItemListParamsOrderDesc, // asc or desc
    },
)

// Auto-pagination
iter := client.Responses.InputItems.ListAutoPaging(ctx, responseID, 
    responses.InputItemListParams{})
for iter.Next() {
    item := iter.Current()
    // Process item...
}
```

### Include Additional Data

Control what additional data is included in responses:

```go
resp, err := client.Responses.New(ctx, responses.ResponseNewParams{
    Input: /* ... */,
    Model: openai.ChatModelGPT4o,
    Include: []responses.ResponseIncludable{
        responses.ResponseIncludableWebSearchCallActionSources,      // Web search sources
        responses.ResponseIncludableCodeInterpreterCallOutputs,      // Code execution outputs
        responses.ResponseIncludableComputerCallOutputOutputImageURL, // Computer tool screenshots
        responses.ResponseIncludableFileSearchCallResults,           // File search results
        responses.ResponseIncludableMessageInputImageImageURL,       // Input image URLs
        responses.ResponseIncludableMessageOutputTextLogprobs,       // Output token logprobs
        responses.ResponseIncludableReasoningEncryptedContent,       // Encrypted reasoning tokens
    },
})
```

## Response Structure

### Response Object

```go
type Response struct {
    ID                 string                   // Unique response ID
    CreatedAt          float64                  // Unix timestamp
    Model              shared.ResponsesModel    // Model used
    Status             ResponseStatus           // completed, failed, in_progress, etc.
    Object             constant.Response        // Always "response"
    
    // Input
    Instructions       ResponseInstructionsUnion // System instructions
    PreviousResponseID string                   // For multi-turn
    Conversation       ResponseConversation     // Conversation ID if used
    
    // Output
    Output             []ResponseOutputItemUnion // Generated output items
    IncompleteDetails  ResponseIncompleteDetails // If incomplete
    Error              ResponseError            // If failed
    
    // Configuration
    Temperature        float64
    TopP               float64
    MaxOutputTokens    int64
    MaxToolCalls       int64
    ParallelToolCalls  bool
    Tools              []ToolUnion
    ToolChoice         ResponseToolChoiceUnion
    Text               ResponseTextConfig
    Reasoning          shared.Reasoning
    
    // Metadata
    Metadata           shared.Metadata
    Usage              ResponseUsage
    ServiceTier        ResponseServiceTier
    Truncation         ResponseTruncation
    
    // Advanced
    Background         bool
    Prompt             ResponsePrompt
    PromptCacheKey     string
    SafetyIdentifier   string
    TopLogprobs        int64
}
```

### Output Items

Output items can be various types:

```go
for _, item := range resp.Output {
    switch item.Type {
    case "message":
        // Assistant message
        msg := item.AsOutputMessage()
        for _, content := range msg.Content {
            if content.Type == "output_text" {
                fmt.Println("Text:", content.Text)
            }
        }
        
    case "function_call":
        // Function call
        call := item.AsFunctionToolCall()
        fmt.Printf("Function: %s(%s)\n", call.Name, call.Arguments)
        
    case "file_search_call":
        // File search
        search := item.AsFileSearchToolCall()
        fmt.Printf("Queries: %v\n", search.Queries)
        
    case "web_search_call":
        // Web search
        search := item.AsWebSearchCall()
        fmt.Printf("Action: %+v\n", search.Action)
        
    case "code_interpreter_call":
        // Code interpreter
        code := item.AsCodeInterpreterToolCall()
        fmt.Printf("Code: %s\n", code.Code)
        fmt.Printf("Outputs: %+v\n", code.Outputs)
        
    case "reasoning":
        // Reasoning (o-series models)
        reasoning := item.AsReasoningItem()
        fmt.Printf("Reasoning content: %+v\n", reasoning.Content)
    }
}
```

### Usage Statistics

```go
fmt.Printf("Input tokens: %d\n", resp.Usage.InputTokens)
fmt.Printf("Output tokens: %d\n", resp.Usage.OutputTokens)
fmt.Printf("Total tokens: %d\n", resp.Usage.TotalTokens)

// Detailed breakdown
if resp.Usage.OutputTokensDetails != nil {
    fmt.Printf("Reasoning tokens: %d\n", resp.Usage.OutputTokensDetails.ReasoningTokens)
    fmt.Printf("Text tokens: %d\n", resp.Usage.OutputTokensDetails.TextTokens)
}

// Input token breakdown
if resp.Usage.InputTokensDetails != nil {
    fmt.Printf("Cached tokens: %d\n", resp.Usage.InputTokensDetails.CachedTokens)
    fmt.Printf("Text tokens: %d\n", resp.Usage.InputTokensDetails.TextTokens)
    fmt.Printf("Audio tokens: %d\n", resp.Usage.InputTokensDetails.AudioTokens)
}
```

### Helper Method

```go
// Simple way to extract all text output
text := resp.OutputText()
fmt.Println(text)

// This internally does:
var outputText strings.Builder
for _, item := range resp.Output {
    for _, content := range item.Content {
        if content.Type == "output_text" {
            outputText.WriteString(content.Text)
        }
    }
}
```

## Best Practices

### 1. Choose the Right Conversation Management Approach

**Use `PreviousResponseID` when:**
- You need fine-grained control over conversation history
- You want to swap system instructions between turns
- Working with stateless architecture
- Need to branch conversations

**Use `Conversation` when:**
- You want automatic history management
- Building multi-session conversations
- Need to retrieve conversation history
- Want simpler code

### 2. Enable Response Storage Selectively

```go
// Only store when needed
Store: openai.Bool(needsRetrieval || needsHistory)
```

Storage incurs costs, so only enable when:
- Using `PreviousResponseID` (required for chaining)
- Need to retrieve response later
- Building conversation history

### 3. Optimize Token Usage

```go
resp, err := client.Responses.New(ctx, responses.ResponseNewParams{
    Input: /* ... */,
    Model: openai.ChatModelGPT4o,
    
    // Set token limits
    MaxOutputTokens: openai.Int(500),
    
    // Use truncation for long histories
    Truncation: responses.ResponseNewParamsTruncationAuto,
    
    // Use prompt caching
    PromptCacheKey: openai.String("stable-prompt-v1"),
})
```

### 4. Handle Errors Gracefully

```go
resp, err := client.Responses.New(ctx, params)
if err != nil {
    var apiErr *openai.Error
    if errors.As(err, &apiErr) {
        fmt.Printf("Status: %d\n", apiErr.StatusCode)
        fmt.Printf("Type: %s\n", apiErr.Type)
        fmt.Printf("Message: %s\n", apiErr.Message)
    }
    return err
}

// Check response status
if resp.Status == "failed" {
    fmt.Printf("Response failed: %+v\n", resp.Error)
    return fmt.Errorf("response generation failed")
}

if resp.Status == "incomplete" {
    fmt.Printf("Incomplete: %s\n", resp.IncompleteDetails.Reason)
    // Handle incomplete response (e.g., max_output_tokens, content_filter)
}
```

### 5. Use Streaming for Better UX

```go
// Use streaming for long responses
stream := client.Responses.NewStreaming(ctx, params)
for stream.Next() {
    event := stream.Current()
    // Update UI with delta as it arrives
    updateUI(event.Delta)
}
```

### 6. Optimize Tool Configuration

```go
// Only include necessary tools
Tools: []responses.ToolUnionParam{
    essentialTool1,
    essentialTool2,
},

// Control tool behavior
ParallelToolCalls: openai.Bool(allowParallel),
MaxToolCalls: openai.Int(5), // Prevent excessive tool use

// Force specific tool when needed
ToolChoice: responses.ResponseNewParamsToolChoiceUnion{
    OfFunctionTool: &responses.ToolChoiceFunctionParam{
        Name: "specific_tool",
    },
},
```

### 7. Handle Multimodal Inputs Efficiently

```go
// Use appropriate detail levels for images
{
    OfInputImage: &responses.ResponseInputImageParam{
        InputImage: responses.ResponseInputImageInputImageParam{
            ImageURL: openai.String(url),
        },
        Detail: responses.ResponseInputImageDetailLow, // Use low for thumbnails
    },
}

// Upload files once, reuse file IDs
{
    OfInputFile: &responses.ResponseInputFileParam{
        FileID: "file-abc123", // Cheaper than re-uploading
    },
}
```

### 8. Monitor Usage and Costs

```go
fmt.Printf("Tokens used: %d\n", resp.Usage.TotalTokens)
fmt.Printf("Service tier: %s\n", resp.ServiceTier)

// Track per-request
if resp.Usage.OutputTokensDetails != nil {
    reasoningCost := resp.Usage.OutputTokensDetails.ReasoningTokens * reasoningRate
    textCost := resp.Usage.OutputTokensDetails.TextTokens * textRate
    totalCost := reasoningCost + textCost
    fmt.Printf("Estimated cost: $%.4f\n", totalCost)
}
```

### 9. Use Include Selectively

```go
// Only request additional data you need
Include: []responses.ResponseIncludable{
    responses.ResponseIncludableMessageOutputTextLogprobs, // If analyzing probabilities
    responses.ResponseIncludableCodeInterpreterCallOutputs, // If using code interpreter
},
```

Including unnecessary data increases response size and costs.

### 10. Test Background Processing

```go
if isLongRunning {
    params.Background = openai.Bool(true)
    
    resp, err := client.Responses.New(ctx, params)
    // Store response.ID for later retrieval
    // Implement polling or webhook handler
}
```

## Comparison Table: Response API vs Chat Completion API

| Feature | Response API | Chat Completion API |
|---------|-------------|-------------------|
| **State Management** | Built-in (`conversation`, `PreviousResponseID`) | Manual (client-side) |
| **Multimodal** | Native support (images, audio, files) | Limited to images |
| **Tools** | Built-in tools + functions + MCP | Functions only |
| **Background Jobs** | Yes (`background: true`) | No |
| **Streaming Events** | Fine-grained (30+ event types) | Basic (content + function call) |
| **Input Format** | Flexible (string or items array) | Messages array |
| **Output Format** | Structured items | Message object |
| **Token Usage** | Detailed breakdown | Basic count |
| **Retrieval** | Can retrieve by ID | No built-in retrieval |
| **Cancellation** | Yes (for background) | No |
| **Truncation** | Auto or disabled | Not available |

## Additional Resources

- [OpenAI Response API Documentation](https://platform.openai.com/docs/guides/text)
- [Conversation State Guide](https://platform.openai.com/docs/guides/conversation-state)
- [Function Calling Guide](https://platform.openai.com/docs/guides/function-calling)
- [Built-in Tools Guide](https://platform.openai.com/docs/guides/tools)
- [Structured Outputs Guide](https://platform.openai.com/docs/guides/structured-outputs)
- [Go SDK Repository](https://github.com/openai/openai-go)
- [Go SDK API Reference](https://pkg.go.dev/github.com/openai/openai-go/v2)

## Conclusion

The OpenAI Response API provides a powerful, flexible interface for building conversational AI applications with:

- **Simplified conversation management** through `PreviousResponseID` or `Conversation`
- **Rich multimodal support** for images, audio, and files
- **Comprehensive tool ecosystem** with built-in tools, functions, and MCP integrations
- **Fine-grained streaming** with detailed event types for real-time updates
- **Production-ready features** like background processing, prompt caching, and structured outputs

The Go SDK provides excellent type safety and ergonomic APIs that make working with the Response API straightforward while maintaining flexibility for advanced use cases.
