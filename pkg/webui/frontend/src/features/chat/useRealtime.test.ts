import { act, renderHook, waitFor } from '@testing-library/react';
import { afterEach, assert, beforeEach, describe, expect, it, vi } from 'vitest';
import apiService from '../../services/api';
import type { RealtimeEvent, RealtimeSession } from '../../types';
import { useRealtime } from './useRealtime';

class Track extends EventTarget {
  enabled = true;
  stop = vi.fn();
}

class Peer extends EventTarget {
  connectionState = 'new';
  iceGatheringState = 'complete';
  localDescription = { type: 'offer', sdp: 'browser-offer' };
  onconnectionstatechange = () => {};
  ontrack = (_event: RTCTrackEvent) => {};
  addTrack = vi.fn();
  createDataChannel = vi.fn();
  createOffer = vi.fn().mockResolvedValue(this.localDescription);
  setLocalDescription = vi.fn().mockResolvedValue(undefined);
  setRemoteDescription = vi.fn().mockImplementation(async () => {
    this.connectionState = 'connected';
    this.onconnectionstatechange();
  });
  close = vi.fn();
}

const getUserMedia = vi.fn();
const onEvent = vi.fn();
let track: Track;
let peer: Peer;
let stream: MediaStream;
let emit: (event: RealtimeEvent) => void;
const session: RealtimeSession = { id: 'call-one', conversationId: 'saved-chat', answer: 'answer' };

function deferred<T>() {
  let resolve!: (value: T) => void;
  const promise = new Promise<T>((fulfill) => {
    resolve = fulfill;
  });
  return { promise, resolve };
}

function renderCall(conversationId = 'saved-chat', captureBusy = false) {
  return renderHook(
    ({ targetKey, onEvent }) =>
      useRealtime({
        targetKey,
        chat: { message: '', conversationId },
        isCaptureBusy: () => captureBusy,
        onEvent,
      }),
    { initialProps: { targetKey: conversationId, onEvent } }
  );
}

beforeEach(() => {
  track = new Track();
  peer = new Peer();
  stream = { getTracks: () => [track], getAudioTracks: () => [track] } as unknown as MediaStream;
  getUserMedia.mockReset().mockResolvedValue(stream);
  vi.spyOn(HTMLMediaElement.prototype, 'play').mockResolvedValue(undefined);
  vi.spyOn(HTMLMediaElement.prototype, 'pause').mockImplementation(() => {});
  onEvent.mockReset();
  vi.stubGlobal('isSecureContext', true);
  vi.stubGlobal('navigator', { mediaDevices: { getUserMedia } });
  vi.stubGlobal(
    'RTCPeerConnection',
    class extends Peer {
      constructor() {
        super();
        peer = this;
      }
    }
  );
  vi.spyOn(apiService, 'getRealtimeCapabilities').mockResolvedValue({
    enabled: true,
    available: true,
  });
  vi.spyOn(apiService, 'startRealtime').mockResolvedValue(session);
  vi.spyOn(apiService, 'endRealtime').mockResolvedValue(undefined);
  vi.spyOn(apiService, 'streamRealtime').mockImplementation((_id, options) => {
    emit = options.onEvent;
    return new Promise((resolve) =>
      options.signal.addEventListener('abort', () => resolve(), { once: true })
    );
  });
  vi.spyOn(apiService, 'stopConversation').mockResolvedValue({
    success: true,
    conversation_id: 'saved-chat',
    stopped: true,
  });
  vi.spyOn(apiService, 'streamChat').mockResolvedValue(undefined);
});

afterEach(() => {
  vi.restoreAllMocks();
  vi.unstubAllGlobals();
});

describe('useRealtime', () => {
  it('negotiates, mutes and ends media without submitting captions or stopping coding work', async () => {
    const hook = renderCall();
    await act(async () => {
      expect(await hook.result.current.start()).toBe(true);
    });
    expect(hook.result.current.phase).toBe('connected');
    expect(getUserMedia).toHaveBeenCalledWith({
      audio: { echoCancellation: true, noiseSuppression: true },
    });
    expect(apiService.startRealtime).toHaveBeenCalledWith(
      'browser-offer',
      { message: '', conversationId: 'saved-chat' },
      { signal: expect.any(AbortSignal), voice: undefined }
    );
    expect(peer.setRemoteDescription).toHaveBeenCalledWith({ type: 'answer', sdp: 'answer' });
    const audio = document.querySelector('audio');
    assert.isNotNull(audio);
    expect(audio.autoplay).toBe(true);
    expect(audio).toHaveAttribute('playsinline');
    await act(async () => peer.ontrack({ streams: [stream] } as unknown as RTCTrackEvent));
    expect(audio.srcObject).toBe(stream);
    expect(audio.play).toHaveBeenCalledOnce();
    const updatedOnEvent = vi.fn();
    hook.rerender({ targetKey: 'saved-chat', onEvent: updatedOnEvent });
    act(() =>
      emit({ kind: 'transcript', id: 'utterance', role: 'user', text: 'Fix it', final: true })
    );
    expect(updatedOnEvent).toHaveBeenCalledOnce();
    expect(onEvent).not.toHaveBeenCalled();
    expect(apiService.streamChat).not.toHaveBeenCalled();
    act(() => hook.result.current.toggleMute());
    expect(track.enabled).toBe(false);
    expect(hook.result.current.muted).toBe(true);
    act(() => hook.result.current.toggleMute());
    expect(track.enabled).toBe(true);
    act(() => hook.result.current.end());
    expect(track.stop).toHaveBeenCalledOnce();
    expect(peer.close).toHaveBeenCalledOnce();
    expect(audio.pause).toHaveBeenCalledOnce();
    expect(audio.srcObject).toBeNull();
    expect(audio.isConnected).toBe(false);
    expect(apiService.endRealtime).toHaveBeenCalledWith('call-one');
    expect(apiService.stopConversation).not.toHaveBeenCalled();
    act(() => emit({ kind: 'error', text: 'stale error' }));
    expect(hook.result.current.error).toBeNull();
  });

  it('stops a microphone granted after cancellation and does not allocate a call', async () => {
    const pending = deferred<MediaStream>();
    getUserMedia.mockReturnValueOnce(pending.promise);
    const hook = renderCall();
    let starting!: Promise<boolean>;
    act(() => {
      starting = hook.result.current.start();
    });
    await waitFor(() => expect(getUserMedia).toHaveBeenCalledOnce());
    act(() => hook.result.current.end());
    await act(async () => {
      pending.resolve(stream);
      await starting;
    });
    expect(track.stop).toHaveBeenCalledOnce();
    expect(apiService.startRealtime).not.toHaveBeenCalled();
  });

  it('closes a late server allocation when navigation cancels negotiation', async () => {
    const pending = deferred<RealtimeSession>();
    vi.mocked(apiService.startRealtime).mockReturnValueOnce(pending.promise);
    const hook = renderCall();
    let starting!: Promise<boolean>;
    act(() => {
      starting = hook.result.current.start();
    });
    await waitFor(() => expect(apiService.startRealtime).toHaveBeenCalledOnce());
    hook.rerender({ targetKey: 'another-conversation', onEvent });
    await act(async () => {
      pending.resolve(session);
      await starting;
    });
    expect(hook.result.current.busy).toBe(false);
    expect(apiService.endRealtime).toHaveBeenCalledOnce();
    expect(peer.setRemoteDescription).not.toHaveBeenCalled();
    expect(track.stop).toHaveBeenCalledOnce();
  });

  it('cleans up on microphone failure and unmount', async () => {
    const hook = renderCall();
    await act(async () => {
      await hook.result.current.start();
    });
    act(() => track.dispatchEvent(new Event('ended')));
    expect(hook.result.current.error).toBe('The microphone disconnected.');
    expect(hook.result.current.busy).toBe(false);
    expect(apiService.endRealtime).toHaveBeenCalledOnce();
    await act(async () => {
      await hook.result.current.start();
    });
    hook.unmount();
    expect(apiService.endRealtime).toHaveBeenCalledTimes(2);
    expect(HTMLMediaElement.prototype.pause).toHaveBeenCalledTimes(2);
  });

  it('rechecks availability at start and prevents concurrent starts', async () => {
    vi.mocked(apiService.getRealtimeCapabilities).mockResolvedValueOnce({
      enabled: true,
      available: false,
      reason: 'Sign in first.',
    });
    const hook = renderCall();
    await waitFor(() => expect(hook.result.current.reason).toBe('Sign in first.'));
    await act(async () => {
      const first = hook.result.current.start();
      expect(await hook.result.current.start()).toBe(false);
      await first;
    });
    expect(hook.result.current.phase).toBe('connected');
    expect(apiService.startRealtime).toHaveBeenCalledOnce();
    hook.unmount();
  });

  it('refuses a second capture during dictation', async () => {
    const dictating = renderCall('saved-chat', true);
    await act(async () => {
      await dictating.result.current.start();
    });
    expect(dictating.result.current.error).toContain('Finish dictation');
    expect(getUserMedia).not.toHaveBeenCalled();
  });

  it('releases media when negotiation fails', async () => {
    vi.mocked(apiService.startRealtime).mockRejectedValueOnce(new Error('Provider unavailable'));
    const hook = renderCall();
    await act(async () => {
      await hook.result.current.start();
    });
    expect(hook.result.current.error).toBe('Provider unavailable');
    expect(track.stop).toHaveBeenCalledOnce();
    expect(peer.close).toHaveBeenCalledOnce();
    expect(document.querySelector('audio')).toBeNull();
    expect(hook.result.current.busy).toBe(false);
  });

  it('reports rejected playback instead of staying silently connected', async () => {
    vi.mocked(HTMLMediaElement.prototype.play).mockRejectedValueOnce(
      new DOMException('Playback blocked', 'NotAllowedError')
    );
    const hook = renderCall();
    await act(async () => {
      await hook.result.current.start();
      await peer.ontrack({ streams: [stream] } as unknown as RTCTrackEvent);
    });
    expect(hook.result.current.error).toContain('audio');
    expect(hook.result.current.error).not.toContain('Microphone');
    expect(hook.result.current.busy).toBe(false);
    expect(document.querySelector('audio')).toBeNull();
    expect(track.stop).toHaveBeenCalledOnce();
  });
});
