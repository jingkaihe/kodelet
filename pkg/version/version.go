// Package version provides version information for kodelet including
// semantic version, git commit SHA, build time, and build information
// that are set during the build process.
package version

import (
	"encoding/json"
	"fmt"
	"runtime"
)

var (
	// Version is the current version of Kodelet
	// This will be set during the build process from VERSION.txt
	Version = "dev"

	// GitCommit is the git commit SHA that was built
	// This will be set during the build process
	GitCommit = "unknown"

	// BuildTime is the time when the binary was built
	// This will be set during the build process
	BuildTime = "unknown"
)

// Info represents version information
type Info struct {
	Version   string `json:"version"`
	GitCommit string `json:"gitCommit"`
	BuildTime string `json:"buildTime"`
	GoVersion string `json:"goVersion"`
}

// Get returns the version information
func Get() Info {
	return Info{
		Version:   Version,
		GitCommit: GitCommit,
		BuildTime: BuildTime,
		GoVersion: runtime.Version(),
	}
}

// String returns the string representation of version info
func (i Info) String() string {
	return fmt.Sprintf("Version: %s, GitCommit: %s, BuildTime: %s, GoVersion: %s", i.Version, i.GitCommit, i.BuildTime, i.GoVersion)
}

// JSON returns the JSON representation of version info
func (i Info) JSON() (string, error) {
	bytes, err := json.MarshalIndent(i, "", "  ")
	if err != nil {
		return "", err
	}
	return string(bytes), nil
}
