package tui

import "time"

const (
	inputHeight                             = 3
	transcriptRefreshDelay                  = 16 * time.Millisecond
	conversationStreamReconcileDelay        = 50 * time.Millisecond
	conversationStreamStableDelay           = 5 * time.Second
	conversationStreamReconnectInitialDelay = 250 * time.Millisecond
	conversationStreamReconnectMaxDelay     = 5 * time.Second
	conversationHistoryRefreshTimeout       = 10 * time.Second
	conversationHistoryRefreshMaxRetries    = 3
)
