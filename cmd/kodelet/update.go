package main

import (
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/jingkaihe/kodelet/pkg/logger"
	"github.com/jingkaihe/kodelet/pkg/version"
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
		return fmt.Errorf("version cannot be empty")
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

		if err := updateKodelet(config); err != nil {
			logger.G(ctx).WithError(err).Error("Failed to update Kodelet")
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

func updateKodelet(config *UpdateConfig) error {
	// Get current version info
	currentVersion := version.Get()
	fmt.Printf("Current version: %s\n", currentVersion.Version)

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
		return fmt.Errorf("unsupported architecture: %s", arch)
	}

	// Check for supported OS
	switch osType {
	case "linux", "darwin":
		// These are supported
	default:
		return fmt.Errorf("unsupported operating system: %s", osType)
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
	fmt.Printf("Downloading latest version from: %s\n", downloadURL)

	// Find the current executable path
	execPath, err := os.Executable()
	if err != nil {
		return fmt.Errorf("failed to determine current executable path: %w", err)
	}
	execPath, err = filepath.EvalSymlinks(execPath)
	if err != nil {
		return fmt.Errorf("failed to resolve symlinks for executable path: %w", err)
	}

	fmt.Printf("Current executable: %s\n", execPath)

	// Create a temporary file for downloading
	tempFile, err := os.CreateTemp("", "kodelet-update-*")
	if err != nil {
		return fmt.Errorf("failed to create temporary file: %w", err)
	}
	tempFilePath := tempFile.Name()
	defer os.Remove(tempFilePath) // Clean up temp file on exit

	// Download the new binary
	resp, err := http.Get(downloadURL)
	if err != nil {
		return fmt.Errorf("failed to download new version: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("failed to download new version: HTTP %d", resp.StatusCode)
	}

	// Write the downloaded binary to the temporary file
	_, err = io.Copy(tempFile, resp.Body)
	if err != nil {
		return fmt.Errorf("failed to write downloaded binary: %w", err)
	}
	tempFile.Close()

	// Make the temporary file executable
	if err := os.Chmod(tempFilePath, 0755); err != nil {
		return fmt.Errorf("failed to make downloaded binary executable: %w", err)
	}

	// Check if we need sudo to replace the current binary
	needsSudo := false
	if err := os.Rename(tempFilePath, execPath); err != nil {
		if strings.Contains(err.Error(), "permission denied") {
			needsSudo = true
		} else {
			return fmt.Errorf("failed to replace current binary: %w", err)
		}
	}

	// If we need sudo, try to use it
	if needsSudo {
		fmt.Println("Elevated permissions required to update. You may be prompted for your password.")
		cmd := exec.Command("sudo", "mv", tempFilePath, execPath)
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		cmd.Stdin = os.Stdin

		if err := cmd.Run(); err != nil {
			return fmt.Errorf("failed to replace current binary with sudo: %w", err)
		}
	}

	fmt.Println("Update completed successfully!")
	fmt.Println("Please run 'kodelet version' to verify the new version.")

	return nil
}
