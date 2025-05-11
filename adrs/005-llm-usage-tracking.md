# ADR 005: LLM Usage Tracking

## Status
Proposed

## Context
Currently, Kodelet provides no visibility into token usage from LLM API calls. Users have no easy way to monitor how many tokens are being consumed in their interactions, which can have implications for cost management and API quota planning.

The Anthropic client already returns usage information in API responses:
- `response.Usage.InputTokens`
- `response.Usage.OutputTokens`
- `response.Usage.CacheCreationInputTokens`
- `response.Usage.CacheReadInputTokens`

We need to capture, track, and expose this information to users in a helpful way.

## Decision
We will implement LLM usage tracking with the following approach:

1. Create a `Usage` struct to represent token usage data:
   ```go
   type Usage struct {
      InputTokens              int
      OutputTokens             int
      CacheCreationInputTokens int
      CacheReadInputTokens     int
      TotalTokens              int
   }
   ```

2. Extend the `Thread` interface to include a `GetUsage()` method:
   ```go
   type Thread interface {
      // existing methods...
      GetUsage() Usage
   }
   ```

3. Implement usage tracking in `AnthropicThread`:
   - Store usage information from each API response
   - Accumulate usage across multiple responses in a conversation
   - Return the accumulated usage via the `GetUsage()` method

4. Display usage information:
   - At the end of one-shot commands (`kodelet run`, `kodelet commit`)
   - In the status bar of the TUI (`kodelet chat`)
   - Update usage data in real-time whenever `SendMessage` is called

## Consequences

### Positive
- Users gain visibility into their token consumption
- Helps with budgeting and cost management
- Provides awareness of model efficiency
- Enables future optimization efforts based on usage patterns

### Negative
- Adds complexity to the codebase
- May add minor overhead to track and calculate usage
- UI updates in the TUI require careful implementation to avoid disrupting the user experience

### Neutral
- Requires modifications to key interfaces that may impact downstream consumers

## Implementation Plan

1. Add `Usage` struct and extend the `Thread` interface
2. Update the `AnthropicThread` implementation to track usage
3. Modify one-shot commands to display usage summary at completion
4. Enhance the TUI to show usage data in the status bar
5. Add tests to verify proper usage tracking

## Alternatives Considered

1. **External tracking**: Using middleware or server-side tracking instead of in-application tracking.
   - Rejected because it adds unnecessary complexity and dependencies.

2. **Detailed usage logs**: Creating detailed logs with per-request token usage.
   - May be considered as a future enhancement but not needed for initial implementation.

3. **No tracking**: Continue without usage tracking.
   - Rejected because visibility into token usage is important for users managing API costs.
