package extensions

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestUIFrameLineSupportsPlainAndStyledJSON(t *testing.T) {
	var lines []UIFrameLine
	require.NoError(t, json.Unmarshal([]byte(`[
		"plain",
		{"spans":[{"text":"green","style":{"foreground":"#00ff00","bold":true}}]}
	]`), &lines))

	require.Len(t, lines, 2)
	assert.Equal(t, []UIStyledSpan{{Text: "plain"}}, lines[0].Spans)
	assert.Equal(t, "green", lines[1].Spans[0].Text)
	assert.Equal(t, "#00ff00", lines[1].Spans[0].Style.Foreground)
	assert.True(t, lines[1].Spans[0].Style.Bold)

	payload, err := json.Marshal(lines)
	require.NoError(t, err)
	assert.JSONEq(t, `[
		"plain",
		{"spans":[{"text":"green","style":{"foreground":"#00ff00","bold":true}}]}
	]`, string(payload))
}

type recordingExtensionUIHost struct {
	mu            sync.Mutex
	set           []UIWidgetSetRequest
	owners        []UIExtensionOwner
	cleanups      []UIExtensionOwner
	openResponse  UIFrameResponse
	closeResponse UIFrameResponse
}

func (h *recordingExtensionUIHost) SetWidget(_ context.Context, source UIExtensionSource, request UIWidgetSetRequest) (UIFrameResponse, error) {
	h.mu.Lock()
	h.set = append(h.set, request)
	h.owners = append(h.owners, source.ExtensionUIOwner())
	h.mu.Unlock()
	return UIFrameResponse{Accepted: true, LatestSequence: request.Frame.Sequence}, nil
}

func (*recordingExtensionUIHost) UpdateWidget(context.Context, UIExtensionSource, UIWidgetFrameRequest) (UIFrameResponse, error) {
	return UIFrameResponse{}, nil
}

func (*recordingExtensionUIHost) RemoveWidget(context.Context, UIExtensionSource, UIWidgetRemoveRequest) (UIFrameResponse, error) {
	return UIFrameResponse{}, nil
}

func (h *recordingExtensionUIHost) OpenSurface(context.Context, UIExtensionSource, UISurfaceOpenRequest) (UIFrameResponse, error) {
	return h.openResponse, nil
}

func (*recordingExtensionUIHost) UpdateSurface(context.Context, UIExtensionSource, UISurfaceFrameRequest) (UIFrameResponse, error) {
	return UIFrameResponse{}, nil
}

func (h *recordingExtensionUIHost) CloseSurface(context.Context, UIExtensionSource, UISurfaceCloseRequest) (UIFrameResponse, error) {
	return h.closeResponse, nil
}

func (h *recordingExtensionUIHost) CleanupExtensionUI(owner UIExtensionOwner) {
	h.mu.Lock()
	h.cleanups = append(h.cleanups, owner)
	h.mu.Unlock()
}

func TestProcessRoutesPersistentExtensionUIRequestsAndCleansFailedGeneration(t *testing.T) {
	host := &recordingExtensionUIHost{}
	client := newRPCClient(strings.NewReader(""), ioDiscard{})
	process := &Process{
		Extension:  Extension{ID: "game"},
		client:     client,
		generation: 4,
		uiHost:     host,
	}

	result, rpcErr := process.HandleRPCRequest(context.Background(), UIWidgetSetMethod, json.RawMessage(`{
		"id":"status",
		"placement":"aboveComposer",
		"frame":{"sequence":1,"lines":["ready"]}
	}`))
	require.Nil(t, rpcErr)
	response, ok := result.(UIFrameResponse)
	require.True(t, ok)
	assert.True(t, response.Accepted)
	require.Len(t, host.set, 1)
	assert.Equal(t, "ready", host.set[0].Frame.Lines[0].Spans[0].Text)

	process.handleRPCFailure(client)
	assert.True(t, process.closed)
	assert.Equal(t, 1, process.failures)
	assert.Equal(t, []UIExtensionOwner{{ExtensionID: "game", Generation: 4}}, host.cleanups)

	process.handleRPCFailure(client)
	assert.Len(t, host.cleanups, 1)
	require.NoError(t, process.Close())
	assert.Len(t, host.cleanups, 1)
}

func TestProcessInitializeAllowsReverseUIRequest(t *testing.T) {
	t.Setenv("KODELET_BASE_PATH", t.TempDir())
	clientReader, serverWriter := io.Pipe()
	serverReader, clientWriter := io.Pipe()
	t.Cleanup(func() {
		_ = clientReader.Close()
		_ = serverWriter.Close()
		_ = serverReader.Close()
		_ = clientWriter.Close()
	})

	client := newRPCClient(clientReader, clientWriter)
	host := &recordingExtensionUIHost{}
	process := &Process{
		Extension:  Extension{ID: "reverse-ui"},
		client:     client,
		generation: 1,
	}
	source := &processExtensionUISource{
		process: process,
		client:  client,
		owner:   UIExtensionOwner{ExtensionID: "reverse-ui", Generation: 1},
	}
	process.uiSource = source
	client.setHostRequestHandler(source)

	type initializeResult struct {
		result *InitializeResult
		err    error
	}
	ctx, cancel := context.WithTimeout(ContextWithExtensionUIHost(context.Background(), host), 2*time.Second)
	t.Cleanup(cancel)
	initializeDone := make(chan initializeResult, 1)
	go func() {
		result, err := process.Initialize(ctx, "/workspace")
		initializeDone <- initializeResult{result: result, err: err}
	}()

	outbound := bufio.NewReader(serverReader)
	initializePayload, err := readFrame(outbound)
	require.NoError(t, err)
	var initializeRequest rpcRequest
	require.NoError(t, json.Unmarshal(initializePayload, &initializeRequest))
	assert.Equal(t, "extension.initialize", initializeRequest.Method)

	reverseRequestPayload, err := json.Marshal(rpcRequest{
		JSONRPC:  "2.0",
		ID:       99,
		ParentID: initializeRequest.ID,
		Method:   UIWidgetSetMethod,
		Params: UIWidgetSetRequest{
			ID:        "status",
			Placement: UIWidgetPlacementAboveComposer,
			Frame:     UIFrame{Sequence: 1, Lines: []UIFrameLine{{Spans: []UIStyledSpan{{Text: "ready"}}}}},
		},
	})
	require.NoError(t, err)
	require.NoError(t, writeFrame(serverWriter, reverseRequestPayload))

	type frameResult struct {
		payload []byte
		err     error
	}
	hostResponseDone := make(chan frameResult, 1)
	go func() {
		payload, err := readFrame(outbound)
		hostResponseDone <- frameResult{payload: payload, err: err}
	}()

	var hostResponsePayload []byte
	select {
	case response := <-hostResponseDone:
		require.NoError(t, response.err)
		hostResponsePayload = response.payload
	case <-time.After(time.Second):
		cancel()
		t.Fatal("initialization reverse UI request deadlocked")
	}
	var hostResponse rpcResponse
	require.NoError(t, json.Unmarshal(hostResponsePayload, &hostResponse))
	assert.Equal(t, int64(99), hostResponse.ID)
	assert.JSONEq(t, `{"accepted":true,"latestSequence":1}`, string(hostResponse.Result))

	resultPayload, err := json.Marshal(InitializeResult{Name: "reverse-ui"})
	require.NoError(t, err)
	initializeResponsePayload, err := json.Marshal(rpcResponse{JSONRPC: "2.0", ID: initializeRequest.ID, Result: resultPayload})
	require.NoError(t, err)
	require.NoError(t, writeFrame(serverWriter, initializeResponsePayload))

	select {
	case call := <-initializeDone:
		require.NoError(t, call.err)
		require.NotNil(t, call.result)
		assert.Equal(t, "reverse-ui", call.result.Name)
	case <-time.After(time.Second):
		t.Fatal("initialization response was not delivered")
	}
	require.Len(t, host.set, 1)
	assert.Equal(t, "status", host.set[0].ID)
}

func TestProcessExtensionUISourceRejectsStaleGeneration(t *testing.T) {
	host := &recordingExtensionUIHost{}
	oldClient := newRPCClient(strings.NewReader(""), ioDiscard{})
	currentClient := newRPCClient(strings.NewReader(""), ioDiscard{})
	process := &Process{
		Extension:  Extension{ID: "game"},
		client:     currentClient,
		generation: 12,
		uiHost:     host,
	}
	oldSource := &processExtensionUISource{
		process: process,
		client:  oldClient,
		owner:   UIExtensionOwner{ExtensionID: "game", Generation: 11},
	}
	currentSource := &processExtensionUISource{
		process: process,
		client:  currentClient,
		owner:   UIExtensionOwner{ExtensionID: "game", Generation: 12},
	}
	process.uiSource = currentSource
	params := json.RawMessage(`{
		"id":"status",
		"placement":"aboveComposer",
		"frame":{"sequence":1,"lines":["ready"]}
	}`)

	_, rpcErr := oldSource.HandleRPCRequest(context.Background(), UIWidgetSetMethod, params)
	require.NotNil(t, rpcErr)
	assert.Contains(t, rpcErr.Message, "no longer active")
	assert.Empty(t, host.set)
	require.Error(t, oldSource.NotifyExtensionUI(context.Background(), UISurfaceInputMethod, map[string]any{"id": "game"}))

	result, rpcErr := currentSource.HandleRPCRequest(context.Background(), UIWidgetSetMethod, params)
	require.Nil(t, rpcErr)
	response, ok := result.(UIFrameResponse)
	require.True(t, ok)
	assert.True(t, response.Accepted)
	require.Len(t, host.set, 1)
	assert.Equal(t, []UIExtensionOwner{currentSource.owner}, host.owners)
}

func TestProcessExtensionUISourceOrdersHostEventsBySequence(t *testing.T) {
	reader, writer := io.Pipe()
	t.Cleanup(func() {
		_ = reader.Close()
		_ = writer.Close()
	})
	client := newRPCClient(strings.NewReader(""), writer)
	process := &Process{Extension: Extension{ID: "game"}, client: client, generation: 1}
	source := &processExtensionUISource{
		process: process,
		client:  client,
		owner:   UIExtensionOwner{ExtensionID: "game", Generation: 1},
	}
	process.uiSource = source
	frames := make(chan []byte, 2)
	go func() {
		buffered := bufio.NewReader(reader)
		for range 2 {
			payload, err := readFrame(buffered)
			if err != nil {
				return
			}
			frames <- payload
		}
	}()

	require.NoError(t, source.NotifyExtensionUI(context.Background(), UISurfaceInputMethod, UISurfaceInputNotification{
		ID: "surface", Sequence: 2, Kind: UISurfaceInputKey, Key: "b",
	}))
	select {
	case <-frames:
		t.Fatal("sequence 2 was sent before sequence 1 arrived")
	case <-time.After(25 * time.Millisecond):
	}
	require.NoError(t, source.NotifyExtensionUI(context.Background(), UISurfaceResizeMethod, UISurfaceResizeNotification{
		ID: "surface", Sequence: 1, Width: 80, Height: 24,
	}))

	sequences := make([]uint64, 0, 2)
	methods := make([]string, 0, 2)
	for range 2 {
		select {
		case payload := <-frames:
			var notification struct {
				Method string `json:"method"`
				Params struct {
					Sequence uint64 `json:"sequence"`
				} `json:"params"`
			}
			require.NoError(t, json.Unmarshal(payload, &notification))
			methods = append(methods, notification.Method)
			sequences = append(sequences, notification.Params.Sequence)
		case <-time.After(time.Second):
			t.Fatal("timed out waiting for ordered host notification")
		}
	}
	assert.Equal(t, []string{UISurfaceResizeMethod, UISurfaceInputMethod}, methods)
	assert.Equal(t, []uint64{1, 2}, sequences)
}

func TestProcessResetsSurfaceEventSequenceOnlyAfterAcceptedLifecycleRequest(t *testing.T) {
	tests := []struct {
		name   string
		method string
		params json.RawMessage
	}{
		{
			name:   "open",
			method: UISurfaceOpenMethod,
			params: json.RawMessage(`{"id":"surface","frame":{"sequence":3,"lines":[]}}`),
		},
		{
			name:   "close",
			method: UISurfaceCloseMethod,
			params: json.RawMessage(`{"id":"surface","sequence":3}`),
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			for _, accepted := range []bool{false, true} {
				t.Run(map[bool]string{false: "rejected", true: "accepted"}[accepted], func(t *testing.T) {
					host := &recordingExtensionUIHost{
						openResponse:  UIFrameResponse{Accepted: accepted, LatestSequence: 2},
						closeResponse: UIFrameResponse{Accepted: accepted, LatestSequence: 2},
					}
					process := &Process{uiHost: host}
					source := &processExtensionUISource{
						notify: map[string]*orderedExtensionUINotifications{
							"surface": {next: 3, pending: map[uint64]extensionUINotification{}},
						},
					}

					result, rpcErr := process.handleRPCRequest(context.Background(), source, test.method, test.params)

					require.Nil(t, rpcErr)
					response, ok := result.(UIFrameResponse)
					require.True(t, ok)
					assert.Equal(t, accepted, response.Accepted)
					_, sequenceStateExists := source.notify["surface"]
					assert.Equal(t, !accepted, sequenceStateExists)
				})
			}
		})
	}
}

func TestUISizeValueSupportsCellsAndPercentages(t *testing.T) {
	var cells UISizeValue
	require.NoError(t, json.Unmarshal([]byte(`42`), &cells))
	assert.Equal(t, UISizeValue{Cells: 42, Set: true}, cells)

	var percentage UISizeValue
	require.NoError(t, json.Unmarshal([]byte(`"75%"`), &percentage))
	assert.Equal(t, UISizeValue{Percent: 75, Set: true}, percentage)

	for _, input := range []string{`0`, `"0%"`, `"101%"`, `"wide"`} {
		var value UISizeValue
		require.Error(t, json.Unmarshal([]byte(input), &value), input)
	}
}

func TestExtensionUINormalizationAndValidation(t *testing.T) {
	placement, err := NormalizeWidgetPlacement("")
	require.NoError(t, err)
	assert.Equal(t, UIWidgetPlacementAboveComposer, placement)
	_, err = NormalizeWidgetPlacement("sidebar")
	require.Error(t, err)

	anchor, err := NormalizeSurfaceAnchor("")
	require.NoError(t, err)
	assert.Equal(t, UISurfaceAnchorCenter, anchor)
	_, err = NormalizeSurfaceAnchor("floating")
	require.Error(t, err)

	require.Error(t, ValidateUIObjectID(""))
	require.ErrorContains(t, ValidateUIObjectID(" doom "), "leading or trailing whitespace")
	require.Error(t, ValidateUISequence(0))
	require.NoError(t, ValidateUIObjectID("doom"))
	require.NoError(t, ValidateUISequence(1))
}
