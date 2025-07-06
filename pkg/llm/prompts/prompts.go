package prompts

// ShortSummaryPrompt is the prompt used for generating short conversation summaries
const ShortSummaryPrompt = `Summarise the conversation in one sentence, less or equal than 12 words. Keep it short and concise.

## Process
1. Analyze the multi-turn conversation to understand the context and main topics
2. Internally form your analysis in an <analysis>...</analysis> block, DO NOT write it down
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
<analysis>
User asked me to debug a Python script that was throwing errors.
I identified several issues including missing imports and incorrect variable names.
User then asked about best practices for error handling. We discussed try-catch blocks and logging. Finally reached a working solution with proper exception handling.
</analysis>
<summary>Debugging Python script with proper error handling implementation.</summary>
</example>

<example>
<conversation>
USER: I need to set up a Docker container for my app
ASSISTANT: [provides Docker setup instructions]
</conversation>
<analysis>
User wanted to containerize their Node.js application.
I helped create a Dockerfile with proper base image selection.
User then asked about multi-stage builds for optimization.
We explored build strategies and settled on a production-ready configuration with security best practices and minimal image size.
</analysis>
<summary>Optimizing Docker container with multi-stage builds for production.</summary>
</example>

<example>
<conversation>
USER: What's the best way to optimize SQL queries?
ASSISTANT: [explains SQL optimization techniques]
</conversation>
<analysis>
User inquired about SQL performance issues with slow queries.
I explained indexing strategies and query analysis tools.
User then shared specific slow queries from their application.
We identified missing indexes and inefficient joins,
resulting in query optimization recommendations that improved performance significantly.
</analysis>
<summary>Resolving SQL performance issues through indexing and optimization.</summary>
</example>
`
