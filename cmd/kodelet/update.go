package main

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/jingkaihe/kodelet/pkg/logger"
	"github.com/jingkaihe/kodelet/pkg/presenter"
	"github.com/jingkaihe/kodelet/pkg/version"
	"github.com/pkg/errors"
	"github.com/spf13/cobra"
)

const (
	GitHubRepoURL = "github.com/jingkaihe/kodelet"
)

// UpdateConfig holds configuration for the update command
type UpdateConfig struct {
	Version string
}

// NewUpdateConfig creates a new UpdateConfig with default values
func NewUpdateConfig() *UpdateConfig {
	return &UpdateConfig{
		Version: "latest",
	}
}

// Validate validates the UpdateConfig and returns an error if invalid
func (c *UpdateConfig) Validate() error {
	if c.Version == "" {
		return errors.New("version cannot be empty")
	}

	return nil
}

var updateCmd = &cobra.Command{
	Use:   "update",
	Short: "Update Kodelet to the latest version",
	Long:  `Download and install the latest version of Kodelet or a specified version.`,
	Run: func(cmd *cobra.Command, args []string) {
		ctx := cmd.Context()
		// Get update config from flags
		config := getUpdateConfigFromFlags(cmd)

		if err := updateKodelet(ctx, config); err != nil {
			presenter.Error(err, "Failed to update Kodelet")
			os.Exit(1)
		}
	},
}

func init() {
	defaults := NewUpdateConfig()
	updateCmd.Flags().String("version", defaults.Version, "Specific version to install (e.g., v0.1.0)")
}

// getUpdateConfigFromFlags extracts update configuration from command flags
func getUpdateConfigFromFlags(cmd *cobra.Command) *UpdateConfig {
	config := NewUpdateConfig()

	if version, err := cmd.Flags().GetString("version"); err == nil {
		config.Version = version
	}

	return config
}

func updateKodelet(ctx context.Context, config *UpdateConfig) error {
	// Get current version info
	currentVersion := version.Get()
	presenter.Info(fmt.Sprintf("Current version: %s", currentVersion.Version))

	// Detect OS and architecture
	osType := runtime.GOOS
	arch := runtime.GOARCH

	// Map architecture values to match those used in the install script
	switch arch {
	case "amd64":
		// amd64 is already correct
	case "arm64":
		// arm64 is already correct
	default:
		return errors.New(fmt.Sprintf("unsupported architecture: %s", arch))
	}

	// Check for supported OS
	switch osType {
	case "linux", "darwin":
		// These are supported
	default:
		return errors.New(fmt.Sprintf("unsupported operating system: %s", osType))
	}

	// Construct download URL based on version
	var downloadURL string
	if config.Version == "latest" {
		downloadURL = fmt.Sprintf("https://%s/releases/latest/download/kodelet-%s-%s", GitHubRepoURL, osType, arch)
	} else {
		// If version doesn't start with 'v', add it
		version := config.Version
		if !strings.HasPrefix(version, "v") {
			version = "v" + version
		}
		downloadURL = fmt.Sprintf("https://%s/releases/download/%s/kodelet-%s-%s", GitHubRepoURL, version, osType, arch)
	}

	presenter.Info(fmt.Sprintf("Downloading from: %s", downloadURL))

	// Find the current executable path
	execPath, err := os.Executable()
	if err != nil {
		return errors.Wrap(err, "failed to determine current executable path")
	}
	execPath, err = filepath.EvalSymlinks(execPath)
	if err != nil {
		return errors.Wrap(err, "failed to resolve symlinks for executable path")
	}

	logger.G(ctx).WithField("executable_path", execPath).Debug("Resolved current executable path")

	// Create a temporary file for downloading
	tempFile, err := os.CreateTemp("", "kodelet-update-*")
	if err != nil {
		return errors.Wrap(err, "failed to create temporary file")
	}
	tempFilePath := tempFile.Name()
	defer os.Remove(tempFilePath) // Clean up temp file on exit

	// Download the new binary
	resp, err := http.Get(downloadURL)
	if err != nil {
		return errors.Wrap(err, "failed to download new version")
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return errors.New(fmt.Sprintf("failed to download new version: HTTP %d", resp.StatusCode))
	}

	// Write the downloaded binary to the temporary file
	_, err = io.Copy(tempFile, resp.Body)
	if err != nil {
		return errors.Wrap(err, "failed to write downloaded binary")
	}
	tempFile.Close()

	// Make the temporary file executable
	if err := os.Chmod(tempFilePath, 0755); err != nil {
		return errors.Wrap(err, "failed to make downloaded binary executable")
	}

	// Check if we need sudo to replace the current binary
	needsSudo := false
	if err := os.Rename(tempFilePath, execPath); err != nil {
		if strings.Contains(err.Error(), "permission denied") {
			needsSudo = true
		} else {
			return errors.Wrap(err, "failed to replace current binary")
		}
	}

	// If we need sudo, try to use it
	if needsSudo {
		presenter.Warning("Elevated permissions required to update. You may be prompted for your password.")
		cmd := exec.Command("sudo", "mv", tempFilePath, execPath)
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		cmd.Stdin = os.Stdin

		if err := cmd.Run(); err != nil {
			return errors.Wrap(err, "failed to replace current binary with sudo")
		}
	}

	presenter.Success("Update completed successfully!")
	presenter.Info("Please run 'kodelet version' to verify the new version.")

	return nil
}
