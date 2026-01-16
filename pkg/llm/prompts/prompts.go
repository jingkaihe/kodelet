// Package prompts provides common prompt templates for LLM interactions
// including context compacting prompts and other reusable prompt text
// used throughout kodelet's LLM communication.
package prompts

// ShortSummaryPrompt is the prompt used for generating short conversation summaries
const ShortSummaryPrompt = `Summarise the conversation in one sentence, less or equal than 12 words. Keep it short and concise.

## Process
1. Analyze the multi-turn conversation to understand the context and main topics
2. Internally form your analysis
3. Write a short and concise summary as your only response

## Tone and Style
* Use active, descriptive language without first-person pronouns
* Focus on the main topic or task discussed
* Keep it professional and direct
* Avoid unnecessary words like "help with" or "assistance for"

## Examples
<example>
<conversation>
USER: Can you help me debug this Python script?
ASSISTANT: [helps with debugging Python script]
</conversation>
<summary>Debugging Python script with proper error handling implementation.</summary>
</example>

<example>
<conversation>
USER: I need to set up a Docker container for my app
ASSISTANT: [provides Docker setup instructions]
</conversation>
<summary>Optimizing Docker container with multi-stage builds for production.</summary>
</example>

<example>
<conversation>
USER: What's the best way to optimize SQL queries?
ASSISTANT: [explains SQL optimization techniques]
</conversation>
<summary>Resolving SQL performance issues through indexing and optimization.</summary>
</example>
`
