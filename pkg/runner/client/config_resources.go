package client

import (
	"encoding/json"

	"github.com/pkg/errors"
)

// resourceSettingKeys are the trusted top-level sections runners reread.
var resourceSettingKeys = []string{"extensions", "skills", "context"}

// ResourceSettingsLoader supplies trusted top-level extensions, skills and
// context settings. It is called only when opening or explicitly reloading an
// environment; other settings and profile definitions remain pinned.
type ResourceSettingsLoader func() (map[string]any, error)

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
