package client

import (
	"context"
	"encoding/json"
	"sync"

	"github.com/jingkaihe/kodelet/pkg/logger"
	"github.com/pkg/errors"
)

// resourceSettingKeys are the trusted top-level sections runners reread.
var resourceSettingKeys = []string{"extensions", "skills", "context"}

// ResourceSettingsLoader supplies trusted top-level extensions, skills and
// context settings. It is called only when opening or explicitly reloading an
// environment; other settings and profile definitions remain pinned. Callers
// must not mutate the returned maps.
type ResourceSettingsLoader func() (map[string]any, error)

// ResourceSettings rereads trusted resource settings and retains the last
// valid result. Load falls back to that result, so a transiently invalid file
// cannot break routine environment resolution; Refresh stays strict, so an
// explicit reload can refuse and report the error.
type ResourceSettings struct {
	read ResourceSettingsLoader

	mu      sync.Mutex
	last    map[string]any
	warning string
}

// NewResourceSettings performs a strict initial read.
func NewResourceSettings(read ResourceSettingsLoader) (*ResourceSettings, error) {
	if read == nil {
		return nil, errors.New("resource settings reader is required")
	}
	settings := &ResourceSettings{read: read}
	if err := settings.Refresh(); err != nil {
		return nil, err
	}
	return settings, nil
}

// Refresh rereads the settings, returning any error without falling back.
// A successful refresh becomes the fallback for later loads.
func (r *ResourceSettings) Refresh() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.refreshLocked()
}

// Load rereads the settings and falls back to the last valid result, logging
// each distinct failure once. Reads are serialized so a slow stale read cannot
// replace a newer result.
func (r *ResourceSettings) Load() (map[string]any, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if err := r.refreshLocked(); err != nil {
		if message := err.Error(); message != r.warning {
			r.warning = message
			logger.G(context.Background()).WithError(err).Warn("using the last valid trusted resource settings")
		}
	}
	return r.last, nil
}

func (r *ResourceSettings) refreshLocked() error {
	values, err := r.read()
	if err != nil {
		return err
	}
	r.last = values
	r.warning = ""
	return nil
}

// refreshResourceSettings replaces the resource sections of settings in place
// and returns it, allocating a map when settings is nil. Sections are replaced
// rather than merged so deleted settings do not survive a reload, and values
// are cloned so later Viper merges cannot mutate the loader's maps.
func refreshResourceSettings(settings map[string]any, load ResourceSettingsLoader) (map[string]any, error) {
	if load == nil {
		return settings, nil
	}
	values, err := load()
	if err != nil {
		return nil, errors.Wrap(err, "failed to refresh runner resource settings")
	}
	projected := make(map[string]any)
	for _, key := range resourceSettingKeys {
		if value, ok := values[key]; ok {
			projected[key] = value
		}
	}
	data, err := json.Marshal(projected)
	if err != nil {
		return nil, errors.Wrap(err, "failed to snapshot runner resource settings")
	}
	var cloned map[string]any
	if err := json.Unmarshal(data, &cloned); err != nil {
		return nil, errors.Wrap(err, "failed to decode runner resource settings")
	}
	if settings == nil {
		settings = make(map[string]any)
	}
	for _, key := range resourceSettingKeys {
		delete(settings, key)
		if value, ok := cloned[key]; ok {
			settings[key] = value
		}
	}
	return settings, nil
}
