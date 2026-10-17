import { useCallback, useEffect, useEffectEvent, useLayoutEffect, useRef, useState } from 'react';
import apiService from '../../services/api';
import type { ChatRequest, RealtimeCapabilities, RealtimeEvent } from '../../types';

interface Call {
  abort: AbortController;
  peer?: RTCPeerConnection;
  stream?: MediaStream;
  audio?: HTMLAudioElement;
  id?: string;
  timer?: ReturnType<typeof setTimeout>;
}

function release(call: Call) {
  clearTimeout(call.timer);
  call.abort.abort();
  call.stream?.getTracks().forEach((track) => {
    track.stop();
  });
  call.peer?.close();
  if (call.audio) {
    call.audio.pause();
    call.audio.srcObject = null;
    call.audio.remove();
  }
  if (call.id) void apiService.endRealtime(call.id).catch(() => {});
}

function waitForICE(peer: RTCPeerConnection, signal: AbortSignal): Promise<void> {
  if (signal.aborted) return Promise.reject(signal.reason);
  if (peer.iceGatheringState === 'complete') return Promise.resolve();
  return new Promise((resolve, reject) => {
    const cleanup = () => {
      peer.removeEventListener('icegatheringstatechange', check);
      signal.removeEventListener('abort', abort);
    };
    const check = () => {
      if (peer.iceGatheringState !== 'complete') return;
      cleanup();
      resolve();
    };
    const abort = () => {
      cleanup();
      reject(signal.reason);
    };
    peer.addEventListener('icegatheringstatechange', check);
    signal.addEventListener('abort', abort, { once: true });
  });
}

export function useRealtime({
  targetKey,
  chat,
  isDraft = false,
  blockedReason,
  isCaptureBusy,
  onStarted,
  onEvent,
}: {
  targetKey: string;
  chat: ChatRequest;
  isDraft?: boolean;
  blockedReason?: string;
  isCaptureBusy: () => boolean;
  onStarted?: (conversationId: string, signal: AbortSignal) => Promise<void>;
  onEvent: (event: RealtimeEvent) => void;
}) {
  const [capabilities, setCapabilities] = useState<RealtimeCapabilities | null>(null);
  const [phase, setPhase] = useState<'idle' | 'connecting' | 'connected'>('idle');
  const [muted, setMuted] = useState(false);
  const [selectedVoice, setSelectedVoice] = useState('');
  const [error, setError] = useState<string | null>(null);
  const callRef = useRef<Call | null>(null);
  const target = useRef({ key: targetKey, id: chat.conversationId, isDraft });
  const handleEvent = useEffectEvent(onEvent);
  const handleStarted = useEffectEvent(async (id: string, signal: AbortSignal) =>
    onStarted?.(id, signal)
  );

  useEffect(() => {
    const controller = new AbortController();
    void apiService
      .getRealtimeCapabilities(controller.signal)
      .then((value) => {
        if (!controller.signal.aborted) setCapabilities(value);
      })
      .catch(() => {});
    return () => controller.abort();
  }, []);

  const end = useCallback(() => {
    const call = callRef.current;
    callRef.current = null;
    if (call) release(call);
    setPhase('idle');
    setMuted(false);
  }, []);

  // Runner generations deliberately do not belong in the target key: reconnects
  // must not discard a call, but changing its conversation/workspace must.
  useLayoutEffect(() => {
    const previous = target.current;
    target.current = { key: targetKey, id: chat.conversationId, isDraft };
    // Saving this draft may canonicalize its workspace path. That is adoption,
    // not navigation to another call target.
    const adopted = previous.isDraft && !isDraft && previous.id === chat.conversationId;
    if (previous.key !== targetKey && !adopted) {
      end();
      setError(null);
    }
    if (previous.id !== chat.conversationId) setSelectedVoice('');
  }, [targetKey, chat.conversationId, isDraft, end]);
  useLayoutEffect(() => end, [end]);

  const start = async (): Promise<boolean> => {
    if (callRef.current) return false;
    if (blockedReason || isCaptureBusy()) {
      setError(blockedReason || 'Finish dictation before starting a call.');
      return false;
    }
    if (
      !globalThis.isSecureContext ||
      !navigator.mediaDevices?.getUserMedia ||
      typeof RTCPeerConnection === 'undefined'
    ) {
      setError(
        'Calls require HTTPS or localhost and a browser with microphone and WebRTC support.'
      );
      return false;
    }
    const call: Call = { abort: new AbortController() };
    callRef.current = call;
    const current = () => callRef.current === call;
    const fail = (message: string) => {
      if (!current()) return;
      end();
      setError(message);
    };
    setError(null);
    setMuted(false);
    setPhase('connecting');
    call.timer = setTimeout(() => fail('The call could not connect. Try again.'), 40_000);
    try {
      // Use a media element for WebRTC playout: a WebAudio-only receiver can
      // remain silent in Chromium. Create it in the initiating user gesture.
      call.audio = document.createElement('audio');
      call.audio.autoplay = true;
      call.audio.setAttribute('playsinline', '');
      call.audio.hidden = true;
      document.body.append(call.audio);
      const available = await apiService.getRealtimeCapabilities(call.abort.signal);
      if (!current()) return false;
      setCapabilities(available);
      if (!available.enabled || !available.available) {
        throw new Error(available.reason || 'Calls are not available.');
      }
      const stream = await navigator.mediaDevices.getUserMedia({
        audio: { echoCancellation: true, noiseSuppression: true },
      });
      if (!current()) {
        stream.getTracks().forEach((track) => {
          track.stop();
        });
        return false;
      }
      call.stream = stream;
      const peer = new RTCPeerConnection();
      call.peer = peer;
      for (const track of stream.getAudioTracks()) {
        track.addEventListener('ended', () => fail('The microphone disconnected.'), { once: true });
        peer.addTrack(track, stream);
      }
      peer.ontrack = async (event) => {
        if (!current() || !call.audio) return;
        try {
          call.audio.srcObject = event.streams[0] || new MediaStream([event.track]);
          await call.audio.play();
        } catch {
          fail('Could not play the call audio. Check browser audio permissions and try again.');
        }
      };
      peer.onconnectionstatechange = () => {
        if (!current()) return;
        if (peer.connectionState === 'connected') {
          clearTimeout(call.timer);
          setPhase('connected');
        } else if (peer.connectionState === 'disconnected') {
          clearTimeout(call.timer);
          call.timer = setTimeout(() => fail('The call disconnected. Try again.'), 10_000);
        } else if (peer.connectionState === 'failed' || peer.connectionState === 'closed') {
          fail('The call disconnected. Try again.');
        }
      };
      // Negotiate the provider's data channel, but consume only the authenticated
      // server stream. Delegation and provider credentials stay on the server.
      peer.createDataChannel('oai-events');
      await peer.setLocalDescription(await peer.createOffer());
      await waitForICE(peer, call.abort.signal);
      if (!current()) return false;
      const offer = peer.localDescription?.sdp;
      if (!offer) throw new Error('Could not prepare the call.');
      // Do not abort an allocation on local cancellation: its late response must
      // be closed explicitly, rather than losing the allocated provider call ID.
      const session = await apiService.startRealtime(offer, chat, {
        signal: AbortSignal.timeout(45_000),
        voice: selectedVoice || undefined,
      });
      if (!current()) {
        void apiService.endRealtime(session.id).catch(() => {});
        return false;
      }
      call.id = session.id;
      if (session.conversationId !== chat.conversationId)
        throw new Error('The call changed conversations unexpectedly.');
      await handleStarted(session.conversationId, call.abort.signal);
      if (!current()) return false;
      await peer.setRemoteDescription({ type: 'answer', sdp: session.answer });
      if (!current()) return false;
      void apiService
        .streamRealtime(session.id, {
          signal: call.abort.signal,
          onEvent: (event) => {
            if (!current()) return;
            if (event.kind === 'closed') {
              end();
              return;
            }
            if (event.kind === 'error') {
              fail(event.text || 'The call failed. Try again.');
              return;
            }
            handleEvent(event);
          },
        })
        .then(() => {
          if (current()) fail('The call disconnected. Try again.');
        })
        .catch((cause: unknown) => {
          if (current()) fail(cause instanceof Error ? cause.message : 'The call disconnected.');
        });
      return true;
    } catch (cause) {
      if (current())
        fail(
          cause instanceof DOMException && cause.name === 'NotAllowedError'
            ? 'Microphone access was denied. Allow microphone access and try again.'
            : cause instanceof Error
              ? cause.message
              : 'Could not start the call.'
        );
      return false;
    }
  };

  const toggleMute = () => {
    const tracks = callRef.current?.stream?.getAudioTracks();
    if (!tracks?.length) return;
    const enabled = !tracks[0].enabled;
    tracks.forEach((track) => {
      track.enabled = enabled;
    });
    setMuted(!enabled);
  };

  return {
    enabled: capabilities?.enabled === true,
    blockedReason,
    reason: blockedReason || capabilities?.reason,
    voice: selectedVoice || capabilities?.defaultVoice || '',
    defaultVoice: capabilities?.defaultVoice,
    voices: capabilities?.voices || [],
    setVoice: setSelectedVoice,
    phase,
    busy: phase !== 'idle',
    isBusy: () => callRef.current !== null,
    muted,
    error,
    dismissError: () => setError(null),
    start,
    end,
    toggleMute,
  };
}
