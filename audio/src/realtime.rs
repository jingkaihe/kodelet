use std::sync::Arc;
use std::sync::atomic::{AtomicBool, Ordering};
use std::time::Duration;
use tokio::sync::{mpsc, watch};
use webrtc::api::{
    APIBuilder, interceptor_registry::register_default_interceptors, media_engine::MediaEngine,
};
use webrtc::interceptor::registry::Registry;
use webrtc::media::Sample;
use webrtc::peer_connection::{
    RTCPeerConnection, configuration::RTCConfiguration,
    peer_connection_state::RTCPeerConnectionState, sdp::session_description::RTCSessionDescription,
};
use webrtc::rtp_transceiver::rtp_codec::{
    RTCRtpCodecCapability, RTCRtpCodecParameters, RTPCodecType,
};
use webrtc::track::track_local::track_local_static_sample::TrackLocalStaticSample;
use webrtc::util::marshal::Marshal;

pub struct Peer {
    connection: Arc<RTCPeerConnection>,
    track: Arc<TrackLocalStaticSample>,
    pub packets: mpsc::Receiver<Vec<u8>>,
    pub failed: Arc<AtomicBool>,
    ready: watch::Receiver<bool>,
}

impl Peer {
    pub async fn offer() -> Result<(Self, String), &'static str> {
        let mut media = MediaEngine::default();
        let codec = RTCRtpCodecCapability {
            mime_type: "audio/opus".into(),
            clock_rate: 48_000,
            channels: 2,
            sdp_fmtp_line: "minptime=10;useinbandfec=1".into(),
            ..Default::default()
        };
        media
            .register_codec(
                RTCRtpCodecParameters {
                    capability: codec.clone(),
                    payload_type: 111,
                    ..Default::default()
                },
                RTPCodecType::Audio,
            )
            .map_err(|_| "could not register Opus")?;
        let registry = register_default_interceptors(Registry::new(), &mut media)
            .map_err(|_| "could not initialize WebRTC")?;
        let api = APIBuilder::new()
            .with_media_engine(media)
            .with_interceptor_registry(registry)
            .build();
        let connection = Arc::new(
            api.new_peer_connection(RTCConfiguration::default())
                .await
                .map_err(|_| "could not create WebRTC peer")?,
        );
        let track = Arc::new(TrackLocalStaticSample::new(
            codec,
            "microphone".into(),
            "kodelet".into(),
        ));
        let sender = connection
            .add_track(track.clone())
            .await
            .map_err(|_| "could not add microphone track")?;
        tokio::spawn(async move { while sender.read_rtcp().await.is_ok() {} });
        let failed = Arc::new(AtomicBool::new(false));
        let (tx, packets) = mpsc::channel(128);
        let failure = failed.clone();
        connection.on_track(Box::new(move |remote, _, _| {
            let tx = tx.clone();
            let failed = failure.clone();
            Box::pin(async move {
                if !remote
                    .codec()
                    .capability
                    .mime_type
                    .eq_ignore_ascii_case("audio/opus")
                {
                    failed.store(true, Ordering::Release);
                    return;
                }
                tokio::spawn(async move {
                    while let Ok((packet, _)) = remote.read_rtp().await {
                        match packet.marshal() {
                            Ok(bytes) if bytes.len() <= 8192 => {
                                if tx.try_send(bytes.to_vec()).is_err() {
                                    failed.store(true, Ordering::Release);
                                    return;
                                }
                            }
                            _ => {
                                failed.store(true, Ordering::Release);
                                return;
                            }
                        }
                    }
                });
            })
        }));
        let failure = failed.clone();
        connection.on_peer_connection_state_change(Box::new(move |state| {
            let failed = failure.clone();
            Box::pin(async move {
                if matches!(
                    state,
                    RTCPeerConnectionState::Failed
                        | RTCPeerConnectionState::Closed
                        | RTCPeerConnectionState::Disconnected
                ) {
                    failed.store(true, Ordering::Release);
                }
            })
        }));
        let (ready_tx, ready) = watch::channel(false);
        let channel = connection
            .create_data_channel("oai-events", None)
            .await
            .map_err(|_| "could not create WebRTC event channel")?;
        channel.on_open(Box::new(move || {
            Box::pin(async move {
                let _ = ready_tx.send(true);
            })
        }));
        let failure = failed.clone();
        channel.on_close(Box::new(move || {
            let failure = failure.clone();
            Box::pin(async move {
                failure.store(true, Ordering::Release);
            })
        }));
        // Provider JSON is intentionally not interpreted. Authenticated server events own delegation.
        let mut peer = Self {
            connection,
            track,
            packets,
            failed,
            ready,
        };
        let result = tokio::time::timeout(Duration::from_secs(20), async {
            let offer = peer
                .connection
                .create_offer(None)
                .await
                .map_err(|_| "could not create SDP offer")?;
            let mut gathered = peer.connection.gathering_complete_promise().await;
            peer.connection
                .set_local_description(offer)
                .await
                .map_err(|_| "could not set SDP offer")?;
            let _ = gathered.recv().await;
            peer.connection
                .local_description()
                .await
                .map(|sdp| sdp.sdp)
                .ok_or("missing SDP offer")
        })
        .await
        .unwrap_or(Err("WebRTC offer timed out"));
        match result {
            Ok(sdp) => Ok((peer, sdp)),
            Err(error) => {
                peer.close().await;
                Err(error)
            }
        }
    }

    pub async fn answer(&mut self, sdp: String) -> Result<(), &'static str> {
        if sdp.is_empty() || sdp.len() > 64 * 1024 {
            return Err("invalid SDP answer length");
        }
        let answer = RTCSessionDescription::answer(sdp).map_err(|_| "invalid SDP answer")?;
        tokio::time::timeout(Duration::from_secs(20), async {
            self.connection
                .set_remote_description(answer)
                .await
                .map_err(|_| "could not apply SDP answer")?;
            self.ready
                .wait_for(|ready| *ready)
                .await
                .map_err(|_| "WebRTC event channel closed")?;
            Ok(())
        })
        .await
        .unwrap_or(Err("WebRTC negotiation timed out"))
    }

    pub async fn send(&self, bytes: Vec<u8>) -> Result<(), &'static str> {
        tokio::time::timeout(
            Duration::from_millis(100),
            self.track.write_sample(&Sample {
                data: bytes.into(),
                duration: Duration::from_millis(20),
                ..Default::default()
            }),
        )
        .await
        .map_err(|_| "WebRTC audio send timed out")?
        .map_err(|_| "WebRTC audio send failed")
    }

    pub async fn close(&mut self) {
        let _ = tokio::time::timeout(Duration::from_secs(1), self.connection.close()).await;
    }
}
