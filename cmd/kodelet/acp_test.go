package main

import (
	"context"
	"errors"
	"sync"
	"testing"

	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type fakeACPServerLifecycle struct {
	runStarted chan struct{}
	runResult  chan error
	shutdown   chan struct{}
	closeOnce  sync.Once
}

func newFakeACPServerLifecycle() *fakeACPServerLifecycle {
	return &fakeACPServerLifecycle{
		runStarted: make(chan struct{}),
		runResult:  make(chan error, 1),
		shutdown:   make(chan struct{}),
	}
}

func (s *fakeACPServerLifecycle) Run() error {
	close(s.runStarted)
	return <-s.runResult
}

func (s *fakeACPServerLifecycle) Shutdown() {
	s.closeOnce.Do(func() {
		close(s.shutdown)
		select {
		case s.runResult <- nil:
		default:
		}
	})
}

func TestRunACPServerShutsDownWhenServerStops(t *testing.T) {
	wantErr := errors.New("server stopped")
	server := newFakeACPServerLifecycle()
	server.runResult <- wantErr

	err := runACPServer(context.Background(), server)

	require.ErrorIs(t, err, wantErr)
	assertClosed(t, server.shutdown)
}

func TestRunACPServerShutsDownWhenContextIsCancelled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	server := newFakeACPServerLifecycle()
	done := make(chan error, 1)
	go func() {
		done <- runACPServer(ctx, server)
	}()

	<-server.runStarted
	cancel()

	require.NoError(t, <-done)
	assertClosed(t, server.shutdown)
}

func assertClosed(t *testing.T, ch <-chan struct{}) {
	t.Helper()
	select {
	case <-ch:
	default:
		t.Fatal("expected channel to be closed")
	}
}

func TestACPDoesNotInheritLocalDatabaseInitialization(t *testing.T) {
	require.NotNil(t, acpCmd.PersistentPreRunE)
	require.NoError(t, acpCmd.PersistentPreRunE(acpCmd, nil))
}

func TestACPResolvesConfiguredServerWithoutFlag(t *testing.T) {
	setServerConfigForTest(t, " https://kodelet.example/control ")
	t.Setenv(controlPlaneServerEnv, "")
	cmd := &cobra.Command{Use: "acp"}
	cmd.Flags().String("server", defaultRunnerServer, "")

	server, configured := serverFlagOrConfig(cmd)

	assert.Equal(t, "https://kodelet.example/control", server)
	assert.True(t, configured)
}
