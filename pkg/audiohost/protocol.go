// Package audiohost supervises the local audio helper without native dependencies.
package audiohost

import (
	"encoding/binary"
	"encoding/json"
	"io"

	"github.com/pkg/errors"
)

const (
	protocolVersion = 1
	maxFrameBytes   = 128 << 10 // Includes the one-byte frame kind, not the length prefix.
	maxSDPBytes     = 64 << 10
	jsonFrame       = 1
	pcmFrame        = 2
)

// Command is a JSON control message sent to the helper. Muted is always encoded,
// so unmuting is explicit. Audio never travels on the command pipe.
type Command struct {
	Type               string `json:"type"`
	Protocol           int    `json:"protocol,omitempty"`
	SDP                string `json:"sdp,omitempty"`
	Muted              bool   `json:"muted"`
	MaxAudioBytes      int64  `json:"maxAudioBytes,omitempty"`
	MaxDurationSeconds int    `json:"maxDurationSeconds,omitempty"`
}

// Event is a helper notification. Audio contains owned, raw 16 kHz mono signed
// 16-bit little-endian PCM, only for binary frames whose Type is "audio".
// Terminal transport/process failures are reported as an "error" event.
type Event struct {
	Type     string  `json:"type"`
	Protocol int     `json:"protocol,omitempty"`
	SDP      string  `json:"sdp,omitempty"`
	Message  string  `json:"message,omitempty"`
	Level    float64 `json:"level,omitempty"`
	Muted    bool    `json:"muted"`
	Audio    []byte  `json:"-"`
}

func encodeCommand(command Command) ([]byte, error) {
	if len(command.SDP) > maxSDPBytes {
		return nil, errors.New("audio SDP exceeds the 64 KiB limit")
	}
	data, err := json.Marshal(command)
	if err != nil {
		return nil, errors.Wrap(err, "encode audio command")
	}
	if len(data)+1 > maxFrameBytes {
		return nil, errors.New("audio command exceeds the frame size limit")
	}
	frame := make([]byte, 5+len(data))
	binary.BigEndian.PutUint32(frame, uint32(len(data)+1))
	frame[4] = jsonFrame
	copy(frame[5:], data)
	return frame, nil
}

func readEvent(reader io.Reader) (Event, error) {
	var event Event
	var header [4]byte
	if _, err := io.ReadFull(reader, header[:]); err != nil {
		return event, errors.Wrap(err, "read audio frame length")
	}
	size := binary.BigEndian.Uint32(header[:])
	if size < 1 || size > maxFrameBytes {
		return event, errors.New("invalid audio frame size")
	}
	frame := make([]byte, int(size))
	if _, err := io.ReadFull(reader, frame); err != nil {
		return event, errors.Wrap(err, "read audio frame payload")
	}
	switch frame[0] {
	case jsonFrame:
		if err := json.Unmarshal(frame[1:], &event); err != nil {
			// Do not echo malformed helper output into diagnostics.
			return Event{}, errors.New("invalid audio event JSON")
		}
		if event.Type == "" || event.Type == "audio" {
			return Event{}, errors.New("invalid audio event type")
		}
		if len(event.SDP) > maxSDPBytes {
			return Event{}, errors.New("audio SDP exceeds the 64 KiB limit")
		}
	case pcmFrame:
		if len(frame) == 1 || (len(frame)-1)%2 != 0 {
			return Event{}, errors.New("invalid audio PCM sample length")
		}
		event.Type, event.Audio = "audio", frame[1:]
	default:
		return Event{}, errors.New("unknown audio frame kind")
	}
	return event, nil
}
