package extension

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
)

// UI requests user interactions from the Kodelet host.
type UI struct {
	env     *callEnv
	scopeID string
}

// InputRequest asks the user for text.
type InputRequest struct {
	Title            string `json:"title,omitempty"`
	ID               string `json:"id,omitempty"`
	HelpText         string `json:"helpText,omitempty"`
	Message          string `json:"message,omitempty"`
	Placeholder      string `json:"placeholder,omitempty"`
	DefaultValue     string `json:"defaultValue,omitempty"`
	SubmitButtonText string `json:"submitButtonText,omitempty"`
	CancelButtonText string `json:"cancelButtonText,omitempty"`
	Required         bool   `json:"required,omitempty"`
	Secret           bool   `json:"secret,omitempty"`
}

// ConfirmRequest asks the user to confirm an action.
type ConfirmRequest struct {
	Title             string `json:"title,omitempty"`
	ID                string `json:"id,omitempty"`
	Message           string `json:"message,omitempty"`
	ConfirmButtonText string `json:"confirmButtonText,omitempty"`
	CancelButtonText  string `json:"cancelButtonText,omitempty"`
}

// SelectRequest asks the user to choose one option.
type SelectRequest struct {
	Title            string   `json:"title,omitempty"`
	ID               string   `json:"id,omitempty"`
	Message          string   `json:"message,omitempty"`
	Options          []string `json:"options"`
	SubmitButtonText string   `json:"submitButtonText,omitempty"`
	CancelButtonText string   `json:"cancelButtonText,omitempty"`
}

// NotifyRequest shows a notification.
type NotifyRequest struct {
	Title   string `json:"title,omitempty"`
	Message string `json:"message"`
}

// TranscriptRequest appends a message to the conversation transcript.
type TranscriptRequest struct {
	Title   string `json:"title,omitempty"`
	Message string `json:"message"`
}

type uiResponse struct {
	Status    string  `json:"status"`
	Value     *string `json:"value"`
	Confirmed bool    `json:"confirmed"`
}

// Input asks for text. ok is false if the user dismissed the prompt, it timed
// out, or the host has no interactive UI.
func (u *UI) Input(ctx context.Context, request InputRequest) (value string, ok bool, err error) {
	response, err := u.ask(ctx, "kodelet.ui.input", request)
	if err != nil || response.Status != "submitted" || response.Value == nil {
		return "", false, err
	}
	return *response.Value, true, nil
}

// Confirm asks for confirmation and reports whether the user confirmed.
func (u *UI) Confirm(ctx context.Context, request ConfirmRequest) (bool, error) {
	response, err := u.ask(ctx, "kodelet.ui.confirm", request)
	if err != nil {
		return false, err
	}
	return response.Status == "submitted" && response.Confirmed, nil
}

// Select asks the user to pick an option. ok is false without a selection.
func (u *UI) Select(ctx context.Context, request SelectRequest) (value string, ok bool, err error) {
	response, err := u.ask(ctx, "kodelet.ui.select", request)
	if err != nil || response.Status != "submitted" || response.Value == nil {
		return "", false, err
	}
	return *response.Value, true, nil
}

// Notify shows a notification.
func (u *UI) Notify(ctx context.Context, request NotifyRequest) error {
	_, err := u.env.client.request(ctx, "kodelet.ui.notify", request)
	return err
}

func (u *UI) ask(ctx context.Context, method string, request any) (uiResponse, error) {
	raw, err := u.env.client.request(ctx, method, request)
	if err != nil {
		return uiResponse{}, err
	}
	var response uiResponse
	_ = json.Unmarshal(raw, &response)
	return response, nil
}

// AppendTranscript appends a message to the transcript. It is a no-op when the
// host does not support transcript entries.
func (u *UI) AppendTranscript(ctx context.Context, request TranscriptRequest) error {
	if !u.supported("transcript") {
		return nil
	}
	_, err := u.requestPersistent(ctx, "kodelet.ui.transcript.append", request)
	return err
}

// WidgetPlacement positions a persistent widget.
type WidgetPlacement string

// Widget placements.
const (
	WidgetAboveComposer WidgetPlacement = "aboveComposer"
	WidgetBelowComposer WidgetPlacement = "belowComposer"
)

// SetWidget shows or replaces a persistent widget that outlives the current
// call. An empty placement means WidgetAboveComposer. It is a no-op when the
// host does not support widgets.
func (u *UI) SetWidget(ctx context.Context, id string, lines []FrameLine, placement WidgetPlacement) error {
	if !u.supported("widgets") {
		return nil
	}
	objectID, err := validateUIObjectID(id)
	if err != nil {
		return err
	}
	if placement == "" {
		placement = WidgetAboveComposer
	}
	if lines == nil {
		lines = []FrameLine{}
	}
	sequence := u.env.rt.nextSequence("widget", uiObjectKey(u.scopeID, objectID))
	_, err = u.requestPersistent(ctx, "kodelet.ui.widget.set", map[string]any{
		"id":        objectID,
		"placement": placement,
		"frame":     map[string]any{"sequence": sequence, "lines": lines},
	})
	return err
}

// RemoveWidget removes a persistent widget.
func (u *UI) RemoveWidget(ctx context.Context, id string) error {
	if !u.supported("widgets") {
		return nil
	}
	objectID, err := validateUIObjectID(id)
	if err != nil {
		return err
	}
	sequence := u.env.rt.nextSequence("widget", uiObjectKey(u.scopeID, objectID))
	_, err = u.requestPersistent(ctx, "kodelet.ui.widget.remove", map[string]any{"id": objectID, "sequence": sequence})
	return err
}

// OpenSurface opens an interactive surface that outlives the current call
// until it is closed by the extension or the host.
func (u *UI) OpenSurface(ctx context.Context, options SurfaceOptions) (*Surface, error) {
	if !u.supported("surfaces") {
		return nil, errors.New("interactive extension surfaces are not available in this host")
	}
	id, err := validateUIObjectID(options.ID)
	if err != nil {
		return nil, err
	}
	rt := u.env.rt
	key := uiObjectKey(u.scopeID, id)
	surface := newSurface(id, u.scopeID, rt)
	if !rt.registerSurface(key, surface) {
		return nil, fmt.Errorf("interactive surface %q is already open, opening, or closing; close it before reusing the ID", id)
	}
	lines := options.InitialLines
	if lines == nil {
		lines = []FrameLine{}
	}
	surface.mu.Lock()
	sequence := surface.nextSequenceLocked()
	surface.mu.Unlock()
	raw, err := u.requestPersistent(ctx, "kodelet.ui.surface.open", map[string]any{
		"id":      id,
		"options": options.wire(),
		"frame":   map[string]any{"sequence": sequence, "lines": lines},
	})
	if err == nil {
		err = rejection(raw, "the host rejected the interactive surface")
	}
	if err != nil {
		rt.unregisterSurface(key, surface)
		return nil, err
	}
	if err := surface.activate(); err != nil {
		return nil, err
	}
	return surface, nil
}

func (u *UI) supported(feature string) bool {
	capabilities := u.env.rt.uiCapabilities()
	if capabilities == nil {
		capabilities, _ = capabilityValue(u.env.capabilities(), "ui").(map[string]any)
	}
	value, _ := capabilities[feature].(bool)
	return value
}

func (u *UI) requestPersistent(ctx context.Context, method string, params any) (json.RawMessage, error) {
	scoped, err := withUIScope(params, u.scopeID)
	if err != nil {
		return nil, err
	}
	return u.env.client.requestPersistent(ctx, method, scoped)
}

func withUIScope(params any, scopeID string) (map[string]any, error) {
	record, err := cloneJSON(params)
	if err != nil {
		return nil, err
	}
	scoped, ok := record.(map[string]any)
	if !ok {
		return nil, errors.New("persistent UI parameters must be an object")
	}
	scoped["scopeId"] = scopeID
	return scoped, nil
}

func rejection(raw json.RawMessage, fallback string) error {
	var response struct {
		Accepted *bool   `json:"accepted"`
		Reason   *string `json:"reason"`
	}
	if json.Unmarshal(raw, &response) != nil || response.Accepted == nil || *response.Accepted {
		return nil
	}
	if response.Reason != nil {
		return errors.New(*response.Reason)
	}
	return errors.New(fallback)
}

func uiObjectKey(scopeID, id string) string {
	return scopeID + "\x00" + id
}

func validateUIObjectID(id string) (string, error) {
	if strings.TrimSpace(id) == "" {
		return "", errors.New("extension UI id is required")
	}
	if id != strings.TrimSpace(id) {
		return "", errors.New("extension UI id must not have leading or trailing whitespace")
	}
	if len(id) > 128 {
		return "", errors.New("extension UI id is too long")
	}
	return id, nil
}

// FrameLine is one rendered line: plain text or styled spans.
type FrameLine struct {
	Text  string
	Spans []StyledSpan
}

// Line returns a plain-text frame line.
func Line(text string) FrameLine {
	return FrameLine{Text: text}
}

// Styled returns a frame line made of styled spans.
func Styled(spans ...StyledSpan) FrameLine {
	if spans == nil {
		spans = []StyledSpan{}
	}
	return FrameLine{Spans: spans}
}

// MarshalJSON encodes plain lines as strings and styled lines as {"spans": [...]}.
func (l FrameLine) MarshalJSON() ([]byte, error) {
	if l.Spans != nil {
		return json.Marshal(struct {
			Spans []StyledSpan `json:"spans"`
		}{l.Spans})
	}
	return json.Marshal(l.Text)
}

// UnmarshalJSON decodes either frame line form.
func (l *FrameLine) UnmarshalJSON(data []byte) error {
	var text string
	if err := json.Unmarshal(data, &text); err == nil {
		*l = FrameLine{Text: text}
		return nil
	}
	var styled struct {
		Spans []StyledSpan `json:"spans"`
	}
	if err := json.Unmarshal(data, &styled); err != nil {
		return err
	}
	if styled.Spans == nil {
		styled.Spans = []StyledSpan{}
	}
	*l = FrameLine{Spans: styled.Spans}
	return nil
}

// StyledSpan is styled text within a line.
type StyledSpan struct {
	Text  string `json:"text"`
	Style *Style `json:"style,omitempty"`
}

// Style is a terminal text style.
type Style struct {
	Foreground    string `json:"foreground,omitempty"`
	Background    string `json:"background,omitempty"`
	Bold          bool   `json:"bold,omitempty"`
	Dim           bool   `json:"dim,omitempty"`
	Italic        bool   `json:"italic,omitempty"`
	Underline     bool   `json:"underline,omitempty"`
	Strikethrough bool   `json:"strikethrough,omitempty"`
	Reverse       bool   `json:"reverse,omitempty"`
}

// SurfaceAnchor positions a surface.
type SurfaceAnchor string

// Surface anchors.
const (
	AnchorTopLeft     SurfaceAnchor = "topLeft"
	AnchorTop         SurfaceAnchor = "top"
	AnchorTopRight    SurfaceAnchor = "topRight"
	AnchorLeft        SurfaceAnchor = "left"
	AnchorCenter      SurfaceAnchor = "center"
	AnchorRight       SurfaceAnchor = "right"
	AnchorBottomLeft  SurfaceAnchor = "bottomLeft"
	AnchorBottom      SurfaceAnchor = "bottom"
	AnchorBottomRight SurfaceAnchor = "bottomRight"
)

// Size is a surface dimension in cells or as a percentage.
type Size struct {
	value   float64
	percent bool
}

// Cells returns an absolute size.
func Cells(n int) *Size {
	return &Size{value: float64(n)}
}

// Percent returns a size relative to the available area.
func Percent(p float64) *Size {
	return &Size{value: p, percent: true}
}

// MarshalJSON encodes cells as a number and percentages as "N%".
func (s Size) MarshalJSON() ([]byte, error) {
	if s.percent {
		return json.Marshal(strconv.FormatFloat(s.value, 'f', -1, 64) + "%")
	}
	return json.Marshal(s.value)
}

// Margin surrounds a surface.
type Margin struct {
	Top    int `json:"top,omitempty"`
	Right  int `json:"right,omitempty"`
	Bottom int `json:"bottom,omitempty"`
	Left   int `json:"left,omitempty"`
}

// SurfaceOptions configures OpenSurface.
type SurfaceOptions struct {
	ID           string
	InitialLines []FrameLine
	Width        *Size
	Height       *Size
	MaxWidth     *Size
	MaxHeight    *Size
	Anchor       SurfaceAnchor
	OffsetX      int
	OffsetY      int
	Margin       *Margin
	NonCapturing bool
}

func (o SurfaceOptions) wire() map[string]any {
	options := map[string]any{}
	set := func(key string, value *Size) {
		if value != nil {
			options[key] = value
		}
	}
	set("width", o.Width)
	set("height", o.Height)
	set("maxWidth", o.MaxWidth)
	set("maxHeight", o.MaxHeight)
	if o.Anchor != "" {
		options["anchor"] = o.Anchor
	}
	if o.OffsetX != 0 {
		options["offsetX"] = o.OffsetX
	}
	if o.OffsetY != 0 {
		options["offsetY"] = o.OffsetY
	}
	if o.Margin != nil {
		options["margin"] = o.Margin
	}
	if o.NonCapturing {
		options["nonCapturing"] = true
	}
	return options
}
