// Package prompts provides common prompt templates for LLM interactions
// including context compacting prompts and other reusable prompt text
// used throughout kodelet's LLM communication.
package prompts

// CompactPrompt is the prompt used for comprehensive context compacting
const CompactPrompt = `Create a comprehensive summary of the conversation history that preserves all essential context for continued development work.

Please create a conversation summary following the steps below:

1. Review the entire conversation history thoroughly to understand:
   - The user's primary objectives and detailed requirements
   - All technical decisions and implementations discussed
   - Problems encountered and solutions applied
   - Current state of work and what remains to be done

2. To understand the full context of the conversation, analyze:
   - Explicit user requests and intentions behind each request
   - Technical concepts, frameworks, and tools mentioned or used
   - Files created, modified, or discussed with their purposes
   - Error messages encountered and fixes that were applied
   - Debugging steps taken and troubleshooting approaches used

3. Gather your thoughts in the <analysis>...</analysis> block, and double check the completeness and accuracy of the analysis.

4. Thoroughly review and organize your analysis into the following sections:
   - Explicit Request and Intention: What the user is trying to achieve
   - Key Technical Concepts: Languages, frameworks, patterns, APIs, domain specific concepts, etc.
   - Files and Code Snippets Examined.
   - Errors and Fixes Applied: Chronological list of issues and their resolutions
   - Solved Problems and Ongoing Troubleshooting: Completed and current issues
   - Non-Tool Use User Messages: Clarifications, feedback, preferences, questions
   - Pending Tasks: Immediate next steps, backlog, improvements, testing, documentation
   - Current Work in Progress: Active development, partial implementations, blocked work

5. Create the summary with the following structure:

<summary>

## Objective
Create a detailed summary of the conversation history that preserves all essential context for continued development work. This summary should enable seamless continuation of the project without losing critical technical information or user intent.

### 1. Explicit Request and Intention
- **Primary goal**: What is the user trying to achieve? What is the main objective?
- **Detailed description**: Comprehensive explanation of the user's vision, requirements, and expectations

### 2. Key Technical Concepts
[concept 1]
[concept 2]
...

### 3. Files and Code Snippets Examined
- [File 1]
   - [Purpose of the file]
   - [Summarise the changes made to the file]
   - [Important code snippets]
   - [Dependencies]
- [File 2]
...

### 4. Errors and Fixes Applied
Chronological list of issues encountered and their resolutions:
- [Description of the error]
   - [How it was fixed]
- [Description of the error 2]
   - [How it was fixed]
...

### 5. Solved Problems and Ongoing Troubleshooting
Problems that have been fully resolved:
- [Solved problem 1]
- [Solved problem 2]
...

Ongoing troubleshooting:
- [Ongoing troubleshooting 1]
- [Ongoing troubleshooting 2]
...

### 6. Non-Tool Use User Messages
- [User message 1]
- [User message 2]
...

### 7. Pending Tasks
- [Pending task 1]
- [Pending task 2]
...

### 8. Current Work in Progress
[Precisely describe the current work in progress]
</summary>

IMPORTANT:
- Use the exact 8-section structure above in numerical order
- Include markdown formatting for technical concepts (section 2)
- Use code blocks for code snippets with appropriate language tags
- Be specific with file paths and function names
- Include exact error messages when listing errors and fixes
- Use bullet points within each section for clarity
- The summary should be complete, actionable, technical, chronological, and precise
- This summary replaces the entire conversation history - every detail needed for seamless project continuation must be captured in these 8 sections`

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
