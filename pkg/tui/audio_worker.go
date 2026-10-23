package tui

import (
	"context"
	"encoding/binary"
	"math"
	"slices"
	"strings"
	"time"

	"github.com/jingkaihe/kodelet/pkg/audiohost"
	"github.com/jingkaihe/kodelet/pkg/chat"
	transcriptiontypes "github.com/jingkaihe/kodelet/pkg/types/transcription"
	"github.com/pkg/errors"
)

type audioPeer interface {
	Send(audiohost.Command) error
	Events() <-chan audiohost.Event
	Close() error
}

type dictationClient interface {
	TranscriptionCapabilities(context.Context) (transcriptiontypes.Capabilities, error)
	TranscribeAudio(
		context.Context, []byte, string, string, string,
	) (transcriptiontypes.Result, error)
}

type realtimeAudioClient interface {
	RealtimeCapabilities(context.Context) (chat.RealtimeCapabilities, error)
	StartRealtime(context.Context, string, chat.ChatRequest, string) (chat.RealtimeSession, error)
	StreamRealtime(context.Context, string, func(chat.RealtimeEvent)) error
	EndRealtime(context.Context, string) error
}

func startAudioPeer(ctx context.Context) (audioPeer, error) { return audiohost.Start(ctx) }

// Worker deadlines; tests shorten them.
var (
	audioDiscoveryTimeout = 10 * time.Second
	transcriptionTimeout  = transcriptiontypes.RequestTimeout
	realtimeCallLimit     = 30 * time.Minute
)

var (
	errDictationDiscoveryTimeout = errors.New("timed out checking whether dictation is available")
	errRealtimeDiscoveryTimeout  = errors.New(
		"timed out checking whether voice calls are available",
	)
	errTranscriptionTimeout = errors.New("transcription timed out")
	errRealtimeCallLimit    = errors.New("voice call reached the 30-minute limit")
)

// deadlineError reports cause instead of err when ctx's own deadline, created
// with that cause, ended the operation. Cancellation and parent deadlines keep err.
func deadlineError(ctx context.Context, err, cause error) error {
	if err != nil && errors.Is(context.Cause(ctx), cause) {
		return cause
	}
	return err
}

// startSessionPeer keeps the helper alive when ctx is canceled after startup,
// so the caller's Close can release devices gracefully instead of the helper
// being killed. Canceling ctx during the handshake still aborts startup without
// sending any command. Call release after Close.
func startSessionPeer(
	ctx context.Context,
	start func(context.Context) (audioPeer, error),
) (audioPeer, context.CancelFunc, error) {
	helper, kill := context.WithCancel(context.WithoutCancel(ctx))
	abortStartup := context.AfterFunc(ctx, kill)
	peer, err := start(helper)
	if !abortStartup() {
		// Canceled during startup: the helper is already being stopped.
		if err == nil {
			_ = peer.Close()
		}
		kill()
		return nil, nil, ctx.Err()
	}
	if err != nil {
		kill()
		return nil, nil, err
	}
	return peer, kill, nil
}

func runTUIAudio(
	ctx context.Context,
	mode string,
	runner any,
	request chat.ChatRequest,
	controls <-chan audiohost.Command,
	emit func(tuiAudioMsg),
	start func(context.Context) (audioPeer, error),
) (string, error) {
	if mode == "voice" {
		client, ok := runner.(dictationClient)
		if !ok {
			return "", errors.New("this connection does not support dictation")
		}
		return runDictation(ctx, client, controls, emit, start)
	}
	client, ok := runner.(realtimeAudioClient)
	if !ok {
		return "", errors.New("this connection does not support realtime voice")
	}
	return "", runRealtimeAudio(ctx, client, request, controls, emit, start)
}

func runDictation(
	ctx context.Context,
	client dictationClient,
	controls <-chan audiohost.Command,
	emit func(tuiAudioMsg),
	start func(context.Context) (audioPeer, error),
) (string, error) {
	discovery, cancel := context.WithTimeoutCause(
		ctx, audioDiscoveryTimeout, errDictationDiscoveryTimeout,
	)
	caps, err := client.TranscriptionCapabilities(discovery)
	err = deadlineError(discovery, err, errDictationDiscoveryTimeout)
	cancel()
	if err != nil {
		return "", err
	}
	if !caps.Enabled || !caps.Available {
		return "", errors.New("dictation unavailable: " + caps.Reason)
	}
	if !slices.Contains(caps.MimeTypes, "audio/wav") ||
		caps.MaxAudioBytes <= 44 ||
		caps.MaxDurationSeconds <= 0 {
		return "", errors.New("the server does not advertise usable WAV recording limits")
	}
	peer, release, err := startSessionPeer(ctx, start)
	if err != nil {
		return "", err
	}
	defer release()
	defer peer.Close()
	maxBytes := min(caps.MaxAudioBytes, transcriptiontypes.MaxAudioBytes) - 44
	maxSeconds := min(caps.MaxDurationSeconds, transcriptiontypes.MaxDurationSeconds)
	if err := peer.Send(audiohost.Command{
		Type: "record", MaxAudioBytes: maxBytes, MaxDurationSeconds: maxSeconds,
	}); err != nil {
		return "", err
	}
	// The helper enforces sample limits; the parent also bounds startup, stalled
	// devices, and the final flush independently of device callbacks.
	deadline := time.NewTimer(30 * time.Second)
	defer deadline.Stop()
	var pcm []byte
	var transcript string
	var uploadErr error
	var recording, finishing, recorded bool
	events := peer.Events()
	detector := dictationSpeechDetector{noiseFloor: 0.003}
	var processed, pending int
	var uploading, snapshotted bool
	type uploadResult struct {
		text string
		err  error
	}
	results := make(chan uploadResult, 1)
	ctx, cancelUploads := context.WithCancel(ctx)
	defer cancelUploads()
	uploadNext := func() {
		if uploading || pending == 0 || ctx.Err() != nil {
			return
		}
		// Copy before starting the request: capture keeps appending to pcm. Only
		// the newest waiting prefix is needed, not a queue of overlapping WAVs.
		wav := dictationWAV(pcm[:pending])
		pending = 0
		uploading = true
		upload, cancel := context.WithTimeoutCause(
			ctx, transcriptionTimeout, errTranscriptionTimeout,
		)
		go func() {
			defer cancel()
			result, err := client.TranscribeAudio(
				upload, wav, "dictation.wav", "audio/wav", "",
			)
			err = deadlineError(upload, err, errTranscriptionTimeout)
			if err == nil && strings.TrimSpace(result.Text) == "" {
				err = errors.New("no speech detected")
			}
			results <- uploadResult{text: result.Text, err: err}
		}()
	}
	queueSnapshot := func(size int) {
		pending = size
		snapshotted = true
		detector.dirty = false
		uploadNext()
	}
	for {
		if err := ctx.Err(); err != nil {
			return transcript, err
		}
		if recorded && !uploading && pending == 0 {
			return transcript, uploadErr
		}
		select {
		case <-ctx.Done():
			return transcript, ctx.Err()
		case result := <-results:
			uploading = false
			uploadErr = result.err
			if result.err == nil {
				transcript = result.text
			}
			if !recorded {
				emit(tuiAudioMsg{
					event: audiohost.Event{Type: "preview"},
					text:  result.text,
					err:   result.err,
				})
			}
			uploadNext()
		case <-deadline.C:
			if !recording || finishing {
				return transcript, errors.New("audio recording timed out")
			}
			finishing = true
			if err := peer.Send(audiohost.Command{Type: "finish"}); err != nil {
				return transcript, err
			}
			deadline.Reset(10 * time.Second)
		case control, ok := <-controls:
			if !ok {
				controls = nil
				continue
			}
			if control.Type != "finish" || finishing {
				continue
			}
			finishing = true
			if err := peer.Send(control); err != nil {
				return transcript, err
			}
			deadline.Reset(10 * time.Second)
		case event, ok := <-events:
			if !ok {
				return transcript, errors.New("audio helper exited before finishing the recording")
			}
			switch event.Type {
			case "recording":
				if !recording {
					recording = true
					deadline.Reset(time.Duration(maxSeconds) * time.Second)
				}
			case "audio":
				if len(event.Audio)%2 != 0 || int64(len(pcm)+len(event.Audio)) > maxBytes {
					return transcript, errors.New(
						"audio helper exceeded the recording limit or returned invalid PCM",
					)
				}
				pcm = append(pcm, event.Audio...)
				// Analyze fixed 20 ms frames regardless of helper IPC boundaries.
				for len(pcm)-processed >= 640 {
					frame := pcm[processed : processed+640]
					processed += len(frame)
					if detector.push(frame) {
						queueSnapshot(processed)
					}
				}
				continue
			case "error":
				return transcript, errors.New(event.Message)
			case "recorded":
				recorded = true
				deadline.Stop()
				events = nil
				controls = nil
				if err := peer.Close(); err != nil {
					return transcript, err
				}
				if len(pcm) == 0 {
					return "", errors.New("no audio was recorded")
				}
				emit(tuiAudioMsg{event: audiohost.Event{Type: "transcribing"}})
				if processed < len(pcm) {
					detector.push(pcm[processed:]) // Include even a tiny final syllable.
				}
				// Silence alone does not need another upload. A failed latest request
				// is retried on finish; outstanding requests already cover that speech.
				retryFailedUpload := !uploading && pending == 0 && uploadErr != nil
				if detector.dirty || !snapshotted || retryFailedUpload {
					queueSnapshot(len(pcm))
				}
				continue
			}
			emit(tuiAudioMsg{event: event})
		}
	}
}

// This mirrors the browser's energy detector. Samples always remain in the WAV;
// speech detection only decides when to refresh the cumulative transcription.
type dictationSpeechDetector struct {
	noiseFloor     float64
	voicedSamples  int
	silenceSamples int
	hasSpeech      bool
	speaking       bool
	dirty          bool
}

func (d *dictationSpeechDetector) push(pcm []byte) bool {
	samples := len(pcm) / 2
	var energy float64
	for offset := 0; offset < len(pcm); offset += 2 {
		sample := float64(int16(binary.LittleEndian.Uint16(pcm[offset:]))) / 32768
		energy += sample * sample
	}
	rms := math.Sqrt(energy / float64(samples))
	threshold := max(0.01, d.noiseFloor*3)
	if d.hasSpeech {
		threshold = max(0.008, d.noiseFloor*1.8)
	}
	voiced := rms > threshold
	if !voiced && !d.speaking {
		d.noiseFloor += (rms - d.noiseFloor) * 0.05
	}
	if voiced {
		d.voicedSamples += samples
		d.silenceSamples = 0
		if d.voicedSamples >= 2880 { // 180 ms of initial speech at 16 kHz.
			d.hasSpeech = true
		}
		if d.hasSpeech {
			d.dirty = true
			d.speaking = true
		}
	} else {
		d.silenceSamples += samples
		if !d.hasSpeech && d.silenceSamples >= 1920 { // Reset isolated clicks after 120 ms.
			d.voicedSamples = 0
		}
		if d.silenceSamples >= 2880 {
			d.speaking = false
		}
	}
	return d.dirty && d.silenceSamples >= 40000 // 2.5 seconds of silence.
}

func dictationWAV(pcm []byte) []byte {
	wav := make([]byte, 44+len(pcm))
	copy(wav, "RIFF")
	binary.LittleEndian.PutUint32(wav[4:], uint32(36+len(pcm)))
	copy(wav[8:], "WAVEfmt ")
	binary.LittleEndian.PutUint32(wav[16:], 16)
	binary.LittleEndian.PutUint16(wav[20:], 1)
	binary.LittleEndian.PutUint16(wav[22:], 1)
	binary.LittleEndian.PutUint32(wav[24:], 16000)
	binary.LittleEndian.PutUint32(wav[28:], 32000)
	binary.LittleEndian.PutUint16(wav[32:], 2)
	binary.LittleEndian.PutUint16(wav[34:], 16)
	copy(wav[36:], "data")
	binary.LittleEndian.PutUint32(wav[40:], uint32(len(pcm)))
	copy(wav[44:], pcm)
	return wav
}

// Call setup limits; tests shorten them.
var (
	// The helper's first offer can include a cold GStreamer plugin-registry scan.
	realtimeOfferTimeout = 3 * time.Minute
	// Server allocation, the helper's answer, and ICE/DTLS setup after the offer.
	realtimeConnectTimeout = 90 * time.Second
)

func runRealtimeAudio(
	ctx context.Context,
	client realtimeAudioClient,
	request chat.ChatRequest,
	controls <-chan audiohost.Command,
	emit func(tuiAudioMsg),
	start func(context.Context) (audioPeer, error),
) (err error) {
	ctx, cancel := context.WithTimeoutCause(ctx, realtimeCallLimit, errRealtimeCallLimit)
	defer func() {
		// Whatever failed after the call limit expired failed because of it.
		err = deadlineError(ctx, err, errRealtimeCallLimit)
		cancel()
	}()
	discovery, stop := context.WithTimeoutCause(
		ctx, audioDiscoveryTimeout, errRealtimeDiscoveryTimeout,
	)
	caps, err := client.RealtimeCapabilities(discovery)
	err = deadlineError(discovery, err, errRealtimeDiscoveryTimeout)
	stop()
	if err != nil {
		return err
	}
	if !caps.Enabled || !caps.Available {
		return errors.New("realtime voice unavailable: " + caps.Reason)
	}
	peer, release, err := startSessionPeer(ctx, start)
	if err != nil {
		return err
	}
	defer release()
	if err := peer.Send(audiohost.Command{Type: "createOffer"}); err != nil {
		_ = peer.Close()
		return err
	}
	callID := ""
	defer func() {
		cancel()
		// End the server call while the helper releases the microphone, so both
		// bounded cleanups fit within the TUI's exit wait.
		ended := make(chan struct{})
		go func(id string) {
			defer close(ended)
			if id != "" {
				closeRealtimeCall(client, id)
			}
		}(callID)
		_ = peer.Close()
		<-ended
	}()
	type allocation struct {
		session chat.RealtimeSession
		err     error
	}
	allocated := make(chan allocation)
	type streamUpdate struct {
		event chat.RealtimeEvent
		err   error
		done  bool
	}
	events := make(chan streamUpdate, 32)
	// Slow first-use setup happens before the offer, so the connection deadline
	// starts only once the offer exists.
	connecting := time.NewTimer(realtimeOfferTimeout)
	defer connecting.Stop()
	offered := false
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-connecting.C:
			if !offered {
				return errors.New("audio helper did not prepare the voice call in time")
			}
			return errors.New("voice call connection timed out")
		case control := <-controls:
			if err := peer.Send(control); err != nil {
				return err
			}
		case result := <-allocated:
			callID = result.session.ID
			if result.session.CWD != "" {
				// Sync even after a failure, which can follow saving the conversation.
				emit(tuiAudioMsg{cwd: result.session.CWD})
			}
			if result.err != nil {
				return result.err
			}
			if result.session.ConversationID != request.ConversationID {
				return errors.New("voice call changed conversations unexpectedly")
			}
			emit(tuiAudioMsg{conversationReady: true})
			go func(id string) {
				err := client.StreamRealtime(ctx, id, func(event chat.RealtimeEvent) {
					select {
					case events <- streamUpdate{event: event}:
					case <-ctx.Done():
					}
				})
				select {
				case events <- streamUpdate{err: err, done: true}:
				case <-ctx.Done():
				}
			}(callID)
			if err := peer.Send(audiohost.Command{
				Type: "answer", SDP: result.session.Answer,
			}); err != nil {
				return err
			}
		case update := <-events:
			if update.done {
				return update.err
			}
			event := update.event
			switch event.Kind {
			case "closed":
				return nil
			case "error":
				return errors.New(event.Text)
			}
			emit(tuiAudioMsg{caption: &event})
		case event, ok := <-peer.Events():
			if !ok {
				return errors.New("audio helper disconnected")
			}
			switch event.Type {
			case "offer":
				if offered {
					return errors.New("audio helper returned a duplicate offer")
				}
				offered = true
				connecting.Reset(realtimeConnectTimeout)
				go func(offer string) {
					// Do not lose an allocated call on cancellation. Close late responses.
					setup, stop := context.WithTimeout(context.WithoutCancel(ctx), 45*time.Second)
					defer stop()
					session, err := client.StartRealtime(setup, offer, request, "")
					select {
					case allocated <- allocation{session, err}:
					case <-ctx.Done():
						if session.ID != "" {
							closeRealtimeCall(client, session.ID)
						}
					}
				}(event.SDP)
			case "connected":
				connecting.Stop()
			case "error":
				return errors.New(event.Message)
			}
			emit(tuiAudioMsg{event: event})
		}
	}
}

func closeRealtimeCall(client realtimeAudioClient, id string) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = client.EndRealtime(ctx, id)
}
