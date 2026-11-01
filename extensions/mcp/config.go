package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/pkg/errors"
)

type oauthConfig struct {
	Interactive           any      `json:"interactive"`
	OpenBrowser           *bool    `json:"open_browser"`
	CallbackTimeout       any      `json:"callback_timeout"`
	ClientID              string   `json:"client_id"`
	ClientSecret          string   `json:"client_secret"`
	Flow                  string   `json:"flow"`
	Scopes                []string `json:"scopes"`
	RedirectURI           string   `json:"redirect_uri"`
	AuthServerMetadataURL string   `json:"auth_server_metadata_url"`
	DeviceAuthEndpoint    string   `json:"device_auth_endpoint"`
}

type serverConfig struct {
	Type          string            `json:"type"`
	ServerType    string            `json:"server_type"`
	Command       string            `json:"command"`
	Args          []string          `json:"args"`
	Env           map[string]string `json:"env"`
	Envs          map[string]string `json:"envs"`
	URL           string            `json:"url"`
	Headers       map[string]string `json:"headers"`
	OAuth         json.RawMessage   `json:"oauth"`
	ToolWhiteList []string          `json:"tool_white_list"`
}

type mcpConfig struct {
	OAuth   oauthConfig             `json:"oauth"`
	Servers map[string]serverConfig `json:"mcpServers"`
}

func workspaceCWD() (string, error) {
	if cwd := os.Getenv("KODELET_EXTENSION_WORKSPACE_CWD"); cwd != "" {
		return cwd, nil
	}
	return os.Getwd()
}

func loadConfig(cwd string) (mcpConfig, error) {
	var config mcpConfig
	home, err := os.UserHomeDir()
	if err != nil {
		return config, errors.Wrap(err, "resolve home directory")
	}
	for _, path := range []string{filepath.Join(home, ".kodelet", "mcp.json"), filepath.Join(cwd, "mcp.json")} {
		data, err := os.ReadFile(path)
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			return config, errors.Wrapf(err, "read MCP config %s", path)
		}
		// JSON decoding merges OAuth fields and replaces servers by name.
		if err := json.Unmarshal(data, &config); err != nil {
			return config, errors.Wrapf(err, "decode MCP config %s", path)
		}
	}
	return config, nil
}

func (s serverConfig) transportType() (string, error) {
	kind := s.Type
	if kind == "" {
		kind = s.ServerType
	}
	switch kind = strings.ToLower(strings.TrimSpace(kind)); kind {
	case "":
		if s.URL != "" {
			return "http", nil
		}
		return "stdio", nil
	case "streamable_http", "streamable-http", "streamablehttp":
		return "http", nil
	case "stdio", "http", "sse":
		return kind, nil
	default:
		return "", errors.Errorf("invalid MCP server type %q", kind)
	}
}

func (s serverConfig) oauthConfig(global oauthConfig) (oauthConfig, error) {
	// Decoding a server override must not mutate shared global defaults.
	global.Scopes = slices.Clone(global.Scopes)
	if global.OpenBrowser != nil {
		openBrowser := *global.OpenBrowser
		global.OpenBrowser = &openBrowser
	}
	if len(s.OAuth) > 0 {
		if err := json.Unmarshal(s.OAuth, &global); err != nil {
			return global, errors.Wrap(err, "decode server OAuth config")
		}
	}
	for _, field := range []*string{
		&global.ClientID, &global.ClientSecret, &global.Flow, &global.RedirectURI,
		&global.AuthServerMetadataURL, &global.DeviceAuthEndpoint,
	} {
		*field = os.ExpandEnv(strings.TrimSpace(*field))
	}
	scopes := make([]string, 0, len(global.Scopes))
	for _, scope := range global.Scopes {
		if scope = os.ExpandEnv(strings.TrimSpace(scope)); scope != "" {
			scopes = append(scopes, scope)
		}
	}
	global.Scopes = scopes
	return global, nil
}
