package responses

import (
	"bytes"
	"encoding/json"

	"github.com/openai/openai-go/v3/packages/param"
	openairesponses "github.com/openai/openai-go/v3/responses"
)

// responsesWebSocketContinuation tracks the connection-local response state
// that may be used for a lower-latency incremental response.create request.
// The state is valid only for the WebSocket generation that produced it.
type responsesWebSocketContinuation struct {
	connectionGeneration uint64
	responseID           string
	requestProperties    []byte
	requestInput         []openairesponses.ResponseInputItemUnionParam
	serverKnownItems     []openairesponses.ResponseInputItemUnionParam
}

func (c *responsesWebSocketContinuation) prepare(
	params openairesponses.ResponseNewParams,
	connectionGeneration uint64,
) openairesponses.ResponseNewParams {
	if c == nil || c.connectionGeneration != connectionGeneration || c.responseID == "" {
		return params
	}

	properties, err := responsesWebSocketRequestProperties(params)
	if err != nil || !bytes.Equal(properties, c.requestProperties) {
		return params
	}

	input := params.Input.OfInputItemList
	incremental, ok := incrementalResponsesWebSocketInput(input, c.requestInput, c.serverKnownItems)
	if !ok {
		return params
	}

	params.PreviousResponseID = param.NewOpt(c.responseID)
	params.Input = openairesponses.ResponseNewParamsInputUnion{
		OfInputItemList: incremental,
	}
	return params
}

func (c *responsesWebSocketContinuation) commit(
	connectionGeneration uint64,
	params openairesponses.ResponseNewParams,
	result processStreamResult,
) {
	if c == nil {
		return
	}
	if connectionGeneration == 0 || !result.responseCompleted || result.responseID == "" {
		c.reset()
		return
	}

	properties, err := responsesWebSocketRequestProperties(params)
	if err != nil {
		c.reset()
		return
	}

	c.connectionGeneration = connectionGeneration
	c.responseID = result.responseID
	c.requestProperties = properties
	c.requestInput = cloneResponsesInputItems(params.Input.OfInputItemList)
	c.serverKnownItems = cloneResponsesInputItems(result.serverKnownItems)
}

func (c *responsesWebSocketContinuation) reset() {
	if c == nil {
		return
	}
	*c = responsesWebSocketContinuation{}
}

func responsesWebSocketRequestProperties(params openairesponses.ResponseNewParams) ([]byte, error) {
	body, err := params.MarshalJSON()
	if err != nil {
		return nil, err
	}

	var properties map[string]json.RawMessage
	if err := json.Unmarshal(body, &properties); err != nil {
		return nil, err
	}
	delete(properties, "input")
	delete(properties, "previous_response_id")
	delete(properties, "background")
	delete(properties, "stream")
	delete(properties, "stream_options")
	return json.Marshal(properties)
}

func incrementalResponsesWebSocketInput(
	current []openairesponses.ResponseInputItemUnionParam,
	previousRequest []openairesponses.ResponseInputItemUnionParam,
	serverKnown []openairesponses.ResponseInputItemUnionParam,
) ([]openairesponses.ResponseInputItemUnionParam, bool) {
	if len(current) < len(previousRequest) {
		return nil, false
	}
	for i := range previousRequest {
		if !responsesInputItemEqual(current[i], previousRequest[i]) {
			return nil, false
		}
	}

	remaining := current[len(previousRequest):]
	incremental := make([]openairesponses.ResponseInputItemUnionParam, 0, len(remaining))
	knownIndex := 0
	for _, item := range remaining {
		if knownIndex < len(serverKnown) && responsesInputItemEqual(item, serverKnown[knownIndex]) {
			knownIndex++
			continue
		}
		incremental = append(incremental, item)
	}
	if knownIndex != len(serverKnown) {
		return nil, false
	}
	return cloneResponsesInputItems(incremental), true
}

func responsesInputItemEqual(a, b openairesponses.ResponseInputItemUnionParam) bool {
	aJSON, aErr := json.Marshal(a)
	bJSON, bErr := json.Marshal(b)
	return aErr == nil && bErr == nil && bytes.Equal(aJSON, bJSON)
}

func cloneResponsesInputItems(items []openairesponses.ResponseInputItemUnionParam) []openairesponses.ResponseInputItemUnionParam {
	if items == nil {
		return nil
	}
	cloned := make([]openairesponses.ResponseInputItemUnionParam, len(items))
	copy(cloned, items)
	return cloned
}
