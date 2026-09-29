package controlplane

import (
	"time"

	"github.com/jingkaihe/kodelet/pkg/logger"
)

// startSearchIndexer backfills and maintains the conversation search index in
// the background while the server runs. Searches still refresh recent changes
// on demand, so the loop only keeps that catch-up small.
func (s *Server) startSearchIndexer() {
	if s.searchIndexer == nil || s.searchIndexDone != nil || s.runCtx == nil {
		return
	}
	done := make(chan struct{})
	s.searchIndexDone = done
	go func() {
		defer close(done)
		s.searchIndexer.Run(s.runCtx)
	}()
}

// waitForSearchIndexer lets an in-flight indexing write finish before the
// conversation store closes. Stop cancels the run context the loop uses.
func (s *Server) waitForSearchIndexer() {
	if s.searchIndexDone == nil {
		return
	}
	select {
	case <-s.searchIndexDone:
	case <-time.After(s.httpShutdownTimeout()):
		logger.G(s.runCtx).Warn("the conversation search indexer did not stop before the shutdown timeout")
	}
}
