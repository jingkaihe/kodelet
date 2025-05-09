package version

import (
	"encoding/json"
	"fmt"
)

var (
	// Version is the current version of Kodelet
	// This will be set during the build process from VERSION.txt
	Version = "dev"

	// GitCommit is the git commit SHA that was built
	// This will be set during the build process
	GitCommit = "unknown"
)

// Info represents version information
type Info struct {
	Version   string `json:"version"`
	GitCommit string `json:"gitCommit"`
}

// Get returns the version information
func Get() Info {
	return Info{
		Version:   Version,
		GitCommit: GitCommit,
	}
}

// String returns the string representation of version info
func (i Info) String() string {
	return fmt.Sprintf("Version: %s, GitCommit: %s", i.Version, i.GitCommit)
}

// JSON returns the JSON representation of version info
func (i Info) JSON() (string, error) {
	bytes, err := json.MarshalIndent(i, "", "  ")
	if err != nil {
		return "", err
	}
	return string(bytes), nil
}
