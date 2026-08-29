package extensions

import (
	"bytes"
	"context"
	"encoding/json"
	"math"
	"strconv"
	"strings"

	"github.com/pkg/errors"
)

const (
	UIWidgetPlacementAboveComposer = "aboveComposer"
	UIWidgetPlacementBelowComposer = "belowComposer"

	UISurfaceAnchorTopLeft     = "topLeft"
	UISurfaceAnchorTop         = "top"
	UISurfaceAnchorTopRight    = "topRight"
	UISurfaceAnchorLeft        = "left"
	UISurfaceAnchorCenter      = "center"
	UISurfaceAnchorRight       = "right"
	UISurfaceAnchorBottomLeft  = "bottomLeft"
	UISurfaceAnchorBottom      = "bottom"
	UISurfaceAnchorBottomRight = "bottomRight"

	UISurfaceInputKey   = "key"
	UISurfaceInputMouse = "mouse"
	UISurfaceInputFocus = "focus"
	UISurfaceInputBlur  = "blur"

	UIWidgetSetMethod        = "kodelet.ui.widget.set"
	UIWidgetFrameMethod      = "kodelet.ui.widget.frame"
	UIWidgetRemoveMethod     = "kodelet.ui.widget.remove"
	UITranscriptAppendMethod = "kodelet.ui.transcript.append"
	UISurfaceOpenMethod      = "kodelet.ui.surface.open"
	UISurfaceFrameMethod     = "kodelet.ui.surface.frame"
	UISurfaceCloseMethod     = "kodelet.ui.surface.close"
	UISurfaceInputMethod     = "extension.ui.surface.input"
	UISurfaceResizeMethod    = "extension.ui.surface.resize"
)

// UIExtensionOwner identifies one running generation of an extension process.
// The generation keeps frames from a failed process separate from a restarted
// process whose sequence numbers begin at one again.
type UIExtensionOwner struct {
	ExtensionID string
	Generation  uint64
}

// UIStyle describes terminal-safe styling for one text span.
type UIStyle struct {
	Foreground    string `json:"foreground,omitempty"`
	Background    string `json:"background,omitempty"`
	Bold          bool   `json:"bold,omitempty"`
	Dim           bool   `json:"dim,omitempty"`
	Italic        bool   `json:"italic,omitempty"`
	Underline     bool   `json:"underline,omitempty"`
	Strikethrough bool   `json:"strikethrough,omitempty"`
	Reverse       bool   `json:"reverse,omitempty"`
}

// UIStyledSpan is a styled run of text in a widget or surface line.
type UIStyledSpan struct {
	Text  string  `json:"text"`
	Style UIStyle `json:"style,omitempty"`
}

// UIFrameLine accepts either a plain JSON string or an object containing
// styled spans. Internally both representations use spans.
type UIFrameLine struct {
	Spans []UIStyledSpan `json:"spans"`
}

// UnmarshalJSON implements the mixed string/styled-line wire representation.
func (l *UIFrameLine) UnmarshalJSON(data []byte) error {
	data = bytes.TrimSpace(data)
	if len(data) == 0 {
		return errors.New("widget line is empty JSON")
	}
	if data[0] == '"' {
		var text string
		if err := json.Unmarshal(data, &text); err != nil {
			return err
		}
		l.Spans = []UIStyledSpan{{Text: text}}
		return nil
	}
	var styled struct {
		Spans []UIStyledSpan `json:"spans"`
	}
	if err := json.Unmarshal(data, &styled); err != nil {
		return err
	}
	l.Spans = styled.Spans
	return nil
}

// MarshalJSON preserves the concise string representation for plain lines.
func (l UIFrameLine) MarshalJSON() ([]byte, error) {
	if len(l.Spans) == 1 && l.Spans[0].Style == (UIStyle{}) {
		return json.Marshal(l.Spans[0].Text)
	}
	return json.Marshal(struct {
		Spans []UIStyledSpan `json:"spans"`
	}{Spans: l.Spans})
}

// UIFrame is a complete, replace-in-place presentation snapshot.
type UIFrame struct {
	Sequence uint64        `json:"sequence"`
	Lines    []UIFrameLine `json:"lines"`
}

// UISizeValue is either a fixed terminal-cell count or a percentage.
type UISizeValue struct {
	Cells   int
	Percent float64
	Set     bool
}

// UnmarshalJSON accepts positive integer cell counts and strings such as "75%".
func (v *UISizeValue) UnmarshalJSON(data []byte) error {
	data = bytes.TrimSpace(data)
	if len(data) == 0 || bytes.Equal(data, []byte("null")) {
		*v = UISizeValue{}
		return nil
	}
	if data[0] == '"' {
		var value string
		if err := json.Unmarshal(data, &value); err != nil {
			return err
		}
		value = strings.TrimSpace(value)
		percent, ok := strings.CutSuffix(value, "%")
		if !ok {
			return errors.Errorf("invalid surface size %q", value)
		}
		parsed, err := strconv.ParseFloat(strings.TrimSpace(percent), 64)
		if err != nil || math.IsNaN(parsed) || math.IsInf(parsed, 0) || parsed <= 0 || parsed > 100 {
			return errors.Errorf("invalid surface percentage %q", value)
		}
		*v = UISizeValue{Percent: parsed, Set: true}
		return nil
	}
	var cells int
	if err := json.Unmarshal(data, &cells); err != nil || cells <= 0 {
		return errors.New("surface cell size must be a positive integer")
	}
	*v = UISizeValue{Cells: cells, Set: true}
	return nil
}

// MarshalJSON emits the same number-or-percentage representation accepted by
// UnmarshalJSON.
func (v UISizeValue) MarshalJSON() ([]byte, error) {
	if !v.Set {
		return []byte("null"), nil
	}
	if v.Cells > 0 {
		return json.Marshal(v.Cells)
	}
	return json.Marshal(strconv.FormatFloat(v.Percent, 'f', -1, 64) + "%")
}

// UIMargin constrains an overlay to the usable terminal rectangle.
type UIMargin struct {
	Top    int `json:"top,omitempty"`
	Right  int `json:"right,omitempty"`
	Bottom int `json:"bottom,omitempty"`
	Left   int `json:"left,omitempty"`
}

// UISurfaceOptions controls overlay allocation and focus behavior.
type UISurfaceOptions struct {
	Width        UISizeValue `json:"width,omitempty"`
	Height       UISizeValue `json:"height,omitempty"`
	MaxWidth     UISizeValue `json:"maxWidth,omitempty"`
	MaxHeight    UISizeValue `json:"maxHeight,omitempty"`
	Anchor       string      `json:"anchor,omitempty"`
	OffsetX      int         `json:"offsetX,omitempty"`
	OffsetY      int         `json:"offsetY,omitempty"`
	Margin       UIMargin    `json:"margin,omitempty"`
	NonCapturing bool        `json:"nonCapturing,omitempty"`
}

// UIWidgetSetRequest creates or replaces a passive widget frame.
type UIWidgetSetRequest struct {
	ScopeID   string  `json:"scopeId,omitempty"`
	ID        string  `json:"id"`
	Placement string  `json:"placement"`
	Frame     UIFrame `json:"frame"`
}

// UIWidgetFrameRequest updates an existing passive widget.
type UIWidgetFrameRequest struct {
	ScopeID string  `json:"scopeId,omitempty"`
	ID      string  `json:"id"`
	Frame   UIFrame `json:"frame"`
}

// UIWidgetRemoveRequest removes a passive widget.
type UIWidgetRemoveRequest struct {
	ScopeID  string `json:"scopeId,omitempty"`
	ID       string `json:"id"`
	Sequence uint64 `json:"sequence"`
}

// UITranscriptAppendRequest appends a persistent informational TUI transcript entry.
type UITranscriptAppendRequest struct {
	ScopeID string `json:"scopeId,omitempty"`
	Title   string `json:"title,omitempty"`
	Message string `json:"message"`
}

// UITranscriptAppendResponse reports whether an informational entry was accepted.
type UITranscriptAppendResponse struct {
	Accepted bool   `json:"accepted"`
	Reason   string `json:"reason,omitempty"`
}

// UISurfaceOpenRequest creates or replaces an interactive overlay surface.
type UISurfaceOpenRequest struct {
	ScopeID string           `json:"scopeId,omitempty"`
	ID      string           `json:"id"`
	Options UISurfaceOptions `json:"options,omitempty"`
	Frame   UIFrame          `json:"frame"`
}

// UISurfaceFrameRequest replaces a surface's current presentation frame.
type UISurfaceFrameRequest struct {
	ScopeID string  `json:"scopeId,omitempty"`
	ID      string  `json:"id"`
	Frame   UIFrame `json:"frame"`
}

// UISurfaceCloseRequest closes an interactive surface.
type UISurfaceCloseRequest struct {
	ScopeID  string `json:"scopeId,omitempty"`
	ID       string `json:"id"`
	Sequence uint64 `json:"sequence"`
}

// UIFrameResponse acknowledges the latest accepted sequence for an object.
type UIFrameResponse struct {
	Accepted       bool   `json:"accepted"`
	LatestSequence uint64 `json:"latestSequence"`
	Reason         string `json:"reason,omitempty"`
}

// UISurfaceMouseEvent describes a mouse event relative to the surface origin.
type UISurfaceMouseEvent struct {
	X      int    `json:"x"`
	Y      int    `json:"y"`
	Button string `json:"button"`
	Action string `json:"action"`
	Shift  bool   `json:"shift,omitempty"`
	Alt    bool   `json:"alt,omitempty"`
	Ctrl   bool   `json:"ctrl,omitempty"`
}

// UISurfaceInputNotification routes terminal input and focus changes to an extension.
type UISurfaceInputNotification struct {
	ScopeID  string               `json:"scopeId,omitempty"`
	ID       string               `json:"id"`
	Sequence uint64               `json:"sequence"`
	Kind     string               `json:"kind"`
	Key      string               `json:"key,omitempty"`
	Text     string               `json:"text,omitempty"`
	Alt      bool                 `json:"alt,omitempty"`
	Shift    bool                 `json:"shift,omitempty"`
	Ctrl     bool                 `json:"ctrl,omitempty"`
	Mouse    *UISurfaceMouseEvent `json:"mouse,omitempty"`
}

// UISurfaceResizeNotification reports the current allocated overlay size.
type UISurfaceResizeNotification struct {
	ScopeID  string `json:"scopeId,omitempty"`
	ID       string `json:"id"`
	Sequence uint64 `json:"sequence"`
	Width    int    `json:"width"`
	Height   int    `json:"height"`
}

// UIExtensionSource is a running process capable of receiving host UI events.
type UIExtensionSource interface {
	ExtensionUIOwner() UIExtensionOwner
	NotifyExtensionUI(ctx context.Context, method string, params any) error
}

// PrepareUISurfaceEventLifecycle resets ordered event delivery after a surface
// lifecycle request has been accepted and before the host publishes its UI changes.
func PrepareUISurfaceEventLifecycle(source UIExtensionSource, scopeID, id string, lifecycle uint64) {
	if source == nil || lifecycle == 0 {
		return
	}
	if lifecycleSource, ok := source.(interface {
		PrepareUISurfaceEventLifecycle(string, string, uint64)
	}); ok {
		lifecycleSource.PrepareUISurfaceEventLifecycle(scopeID, id, lifecycle)
	}
}

// NotifyUISurfaceEvent delivers an event within a specific surface lifecycle.
// Sources without lifecycle-aware routing retain the base UIExtensionSource behavior.
func NotifyUISurfaceEvent(ctx context.Context, source UIExtensionSource, lifecycle uint64, method string, params any) error {
	if source == nil {
		return errors.New("extension UI source is required")
	}
	if lifecycleSource, ok := source.(interface {
		NotifyExtensionUISurfaceEvent(context.Context, uint64, string, any) error
	}); ok {
		return lifecycleSource.NotifyExtensionUISurfaceEvent(ctx, lifecycle, method, params)
	}
	return source.NotifyExtensionUI(ctx, method, params)
}

// ExtensionUIHost owns extension widgets and interactive surfaces for an
// interactive frontend.
type ExtensionUIHost interface {
	SetWidget(ctx context.Context, source UIExtensionSource, request UIWidgetSetRequest) (UIFrameResponse, error)
	UpdateWidget(ctx context.Context, source UIExtensionSource, request UIWidgetFrameRequest) (UIFrameResponse, error)
	RemoveWidget(ctx context.Context, source UIExtensionSource, request UIWidgetRemoveRequest) (UIFrameResponse, error)
	OpenSurface(ctx context.Context, source UIExtensionSource, request UISurfaceOpenRequest) (UIFrameResponse, error)
	UpdateSurface(ctx context.Context, source UIExtensionSource, request UISurfaceFrameRequest) (UIFrameResponse, error)
	CloseSurface(ctx context.Context, source UIExtensionSource, request UISurfaceCloseRequest) (UIFrameResponse, error)
	CleanupExtensionUI(owner UIExtensionOwner)
}

// ExtensionUIHostCapabilities describes the persistent UI features implemented
// by a host. Hosts that do not implement ExtensionUIHostCapabilityProvider are
// treated as supporting both widgets and interactive surfaces.
type ExtensionUIHostCapabilities struct {
	Widgets    bool
	Surfaces   bool
	Transcript bool
}

// ExtensionUIHostCapabilityProvider lets a host advertise persistent UI
// features independently. This is useful for frontends that support passive
// widgets without implementing interactive overlay surfaces.
type ExtensionUIHostCapabilityProvider interface {
	ExtensionUIHostCapabilities(ctx context.Context) ExtensionUIHostCapabilities
}

// ExtensionUITranscriptHost optionally accepts persistent informational transcript entries.
type ExtensionUITranscriptHost interface {
	AppendTranscript(ctx context.Context, source UIExtensionSource, request UITranscriptAppendRequest) (UITranscriptAppendResponse, error)
}

type extensionUIHostContextKey struct{}

type extensionUIScopeContextKey struct{}

type extensionUIImplicitScopeContextKey struct{}

// ContextWithExtensionUIHost attaches persistent widget/surface support to an
// extension runtime initialization context.
func ContextWithExtensionUIHost(ctx context.Context, host ExtensionUIHost) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	if host == nil {
		return ctx
	}
	return context.WithValue(ctx, extensionUIHostContextKey{}, host)
}

// ExtensionUIHostFromContext returns the interactive extension UI host, if any.
func ExtensionUIHostFromContext(ctx context.Context) (ExtensionUIHost, bool) {
	if ctx == nil {
		return nil, false
	}
	host, ok := ctx.Value(extensionUIHostContextKey{}).(ExtensionUIHost)
	return host, ok && host != nil
}

// ContextWithExtensionUIScope attaches an opaque host UI scope to extension
// calls. Persistent UI requests carry this scope explicitly so they remain
// associated with their originating conversation after a handler returns.
func ContextWithExtensionUIScope(ctx context.Context, scopeID string) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	scopeID = strings.TrimSpace(scopeID)
	if scopeID == "" {
		return ctx
	}
	return context.WithValue(ctx, extensionUIScopeContextKey{}, scopeID)
}

// ExtensionUIScopeFromContext returns the opaque persistent UI scope, if any.
func ExtensionUIScopeFromContext(ctx context.Context) string {
	if ctx == nil {
		return ""
	}
	scopeID, _ := ctx.Value(extensionUIScopeContextKey{}).(string)
	return strings.TrimSpace(scopeID)
}

// ContextWithExtensionUIImplicitScope marks a persistent UI request whose
// scopeId field was omitted by an older SDK. Hosts may use the request context
// or an existing uniquely matching object to recover its conversation scope.
func ContextWithExtensionUIImplicitScope(ctx context.Context) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	return context.WithValue(ctx, extensionUIImplicitScopeContextKey{}, true)
}

// ExtensionUIHasImplicitScope reports whether a persistent UI request omitted
// scopeId and therefore requires legacy scope recovery.
func ExtensionUIHasImplicitScope(ctx context.Context) bool {
	if ctx == nil {
		return false
	}
	implicit, _ := ctx.Value(extensionUIImplicitScopeContextKey{}).(bool)
	return implicit
}

func hasExplicitExtensionUIScope(params json.RawMessage) bool {
	var request map[string]json.RawMessage
	if err := json.Unmarshal(params, &request); err != nil {
		return false
	}
	_, ok := request["scopeId"]
	return ok
}

// ValidateUIObjectID validates an extension-scoped widget or surface ID.
func ValidateUIObjectID(id string) error {
	if strings.TrimSpace(id) == "" {
		return errors.New("extension UI id is required")
	}
	if id != strings.TrimSpace(id) {
		return errors.New("extension UI id must not have leading or trailing whitespace")
	}
	if len(id) > 128 {
		return errors.New("extension UI id is too long")
	}
	return nil
}

// ValidateUISequence validates a monotonically increasing presentation sequence.
func ValidateUISequence(sequence uint64) error {
	if sequence == 0 {
		return errors.New("extension UI sequence must be greater than zero")
	}
	return nil
}

// NormalizeWidgetPlacement applies the default passive-widget placement.
func NormalizeWidgetPlacement(placement string) (string, error) {
	placement = strings.TrimSpace(placement)
	if placement == "" {
		return UIWidgetPlacementAboveComposer, nil
	}
	switch placement {
	case UIWidgetPlacementAboveComposer, UIWidgetPlacementBelowComposer:
		return placement, nil
	default:
		return "", errors.Errorf("unsupported widget placement %q", placement)
	}
}

// NormalizeSurfaceAnchor applies the default overlay anchor.
func NormalizeSurfaceAnchor(anchor string) (string, error) {
	anchor = strings.TrimSpace(anchor)
	if anchor == "" {
		return UISurfaceAnchorCenter, nil
	}
	switch anchor {
	case UISurfaceAnchorTopLeft, UISurfaceAnchorTop, UISurfaceAnchorTopRight,
		UISurfaceAnchorLeft, UISurfaceAnchorCenter, UISurfaceAnchorRight,
		UISurfaceAnchorBottomLeft, UISurfaceAnchorBottom, UISurfaceAnchorBottomRight:
		return anchor, nil
	default:
		return "", errors.Errorf("unsupported surface anchor %q", anchor)
	}
}
