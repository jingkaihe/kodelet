package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"

	"github.com/pkg/errors"
	"github.com/spf13/cobra"
)

func init() {
	server := &cobra.Command{
		Use: "server", Short: "Manage the local background server",
		Long: "Manage this user's local background server. These commands never operate on a remote --server endpoint. Use 'kodelet serve' for foreground operation.",
	}
	start := &cobra.Command{Use: "start", Short: "Start or reuse the local background server", Args: cobra.NoArgs, RunE: startLocalServerCommand}
	status := &cobra.Command{Use: "status", Short: "Show local server status", Args: cobra.NoArgs, RunE: localServerStatusCommand}
	stop := &cobra.Command{Use: "stop", Short: "Stop the local background server", Args: cobra.NoArgs, RunE: stopLocalServerCommand}
	restart := &cobra.Command{Use: "restart", Short: "Restart the local server to apply configuration changes", Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, args []string) error {
		if err := stopLocalServerCommand(cmd, args); err != nil {
			return err
		}
		return startLocalServerCommand(cmd, args)
	}}
	for _, command := range []*cobra.Command{stop, restart} {
		command.Flags().Bool("force", false, "Cancel active work before stopping the server")
	}
	logs := &cobra.Command{Use: "logs", Short: "Show recent background server logs", Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, _ []string) error {
		directory, err := localServerDirectory()
		if err != nil {
			return err
		}
		file, err := os.Open(filepath.Join(directory, "server.log"))
		if err != nil {
			return errors.Wrap(err, "no background server log is available")
		}
		defer file.Close()
		info, err := file.Stat()
		if err != nil {
			return err
		}
		if info.Size() > 64*1024 {
			if _, err := file.Seek(-64*1024, io.SeekEnd); err != nil {
				return err
			}
		}
		_, err = io.Copy(cmd.OutOrStdout(), file)
		return err
	}}
	server.AddCommand(start, status, stop, restart, logs)
	rootCmd.AddCommand(server)
}

func startLocalServerCommand(cmd *cobra.Command, _ []string) error {
	connection, err := ensureLocalServer(cmd.Context(), cmd.ErrOrStderr())
	if err != nil {
		return err
	}
	fmt.Fprintf(cmd.OutOrStdout(), "Local server ready at %s (PID %d)\n", connection.URL, connection.PID)
	return nil
}

func localServerStatusCommand(cmd *cobra.Command, _ []string) error {
	directory, err := localServerDirectory()
	if err != nil {
		return err
	}
	connection, err := readLocalServerConnection(directory)
	if errors.Is(err, os.ErrNotExist) {
		fmt.Fprintln(cmd.OutOrStdout(), "No local server connection is available. Run 'kodelet chat' or 'kodelet server start'.")
		return nil
	}
	if err != nil {
		return err
	}
	token, err := os.ReadFile(filepath.Join(directory, "client-token"))
	if err != nil {
		return err
	}
	status, err := probeLocalServer(cmd.Context(), connection, string(token))
	if err != nil {
		return err
	}
	fmt.Fprintf(cmd.OutOrStdout(), "Server: %s\nPID: %d\nVersion: %s\nManaged: %t\nAPI ready: %t\nRunner ready: %t\nActive runs: %d\nLog: %s\n", connection.URL, connection.PID, status.Version, connection.Managed, status.APIReady, status.EmbeddedRunner.Ready, status.ActiveRuns, filepath.Join(directory, "server.log"))
	if status.EmbeddedRunner.Error != "" {
		fmt.Fprintf(cmd.OutOrStdout(), "Runner error: %s\n", status.EmbeddedRunner.Error)
	}
	return nil
}

func stopLocalServerCommand(cmd *cobra.Command, _ []string) error {
	ctx, cancel := context.WithTimeout(cmd.Context(), localServerTimeout)
	defer cancel()
	directory, err := localServerDirectory()
	if err != nil {
		return err
	}
	startup, err := waitLocalServerLock(ctx, directory)
	if err != nil {
		return err
	}
	defer startup.Close()
	lock, err := tryLocalServerLock(directory, "server.lock")
	if err != nil {
		return err
	}
	if lock != nil {
		_ = lock.Close()
		fmt.Fprintln(cmd.OutOrStdout(), "Local server is not running.")
		return nil
	}
	connection, err := readLocalServerConnection(directory)
	if err != nil {
		return errors.Wrap(err, "server connection is unavailable; stop the foreground server with its process supervisor or Ctrl+C")
	}
	if !connection.Managed {
		return errors.New("the local server is foreground/operator-managed; stop it with its process supervisor or Ctrl+C")
	}
	token, err := os.ReadFile(filepath.Join(directory, "client-token"))
	if err != nil {
		return err
	}
	if _, err := probeLocalServer(ctx, connection, string(token)); err != nil {
		return err
	}
	force, _ := cmd.Flags().GetBool("force")
	body, err := json.Marshal(map[string]any{"instanceId": connection.InstanceID, "force": force})
	if err != nil {
		return err
	}
	response, err := localServerRequest(ctx, connection, string(token), http.MethodPost, "/api/server/stop", bytes.NewReader(body))
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(response.Body, 4096))
		return errors.Errorf("could not stop local server (HTTP %d): %s", response.StatusCode, body)
	}
	for {
		lock, err := tryLocalServerLock(directory, "server.lock")
		if err != nil {
			return err
		}
		if lock != nil {
			_ = lock.Close()
			fmt.Fprintln(cmd.OutOrStdout(), "Local server stopped.")
			return nil
		}
		if err := waitLocalServerPoll(ctx); err != nil {
			return errors.Wrap(err, "server is still stopping; inspect 'kodelet server status' before retrying")
		}
	}
}
