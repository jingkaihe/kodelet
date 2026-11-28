package main

import (
	"fmt"
	"regexp"
	"sort"
	"strings"

	"github.com/jingkaihe/kodelet/sdk/go/extension"
	"github.com/pkg/errors"
)

const (
	// definedProfilePrefix keeps defined profiles from clashing with
	// Kodelet's own, which would fail every run on the runner.
	definedProfilePrefix  = "schedule-"
	maxProfiles           = 32
	maxProfileDescription = 200
)

var profileNamePattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,127}$`)

// profileSettingKeys have dedicated fields, so they are not settings.
var profileSettingKeys = map[string]bool{"description": true, "provider": true, "model": true, "hidden": true}

// credentialSettings are refused at any depth, since a repository's
// kodelet-config.yaml can set extensions.config.
var credentialSettings = map[string]bool{"base_url": true, "api_key_env_var": true}

// scheduleProfile is a profile schedules may use.
type scheduleProfile struct {
	// Name is what runs select.
	Name string
	// Alias is a defined profile's configured name.
	Alias       string
	Description string
	// Registration is nil for profiles Kodelet already has.
	Registration *extension.ProfileRegistration
}

// profileCatalog is sorted by name.
type profileCatalog []scheduleProfile

// parseProfiles reads config["profiles"], which maps names to a description
// of an existing profile or to a definition with a provider and model.
func parseProfiles(config map[string]any) (profileCatalog, error) {
	raw, ok := config["profiles"]
	if !ok || raw == nil {
		return nil, nil
	}
	entries, ok := raw.(map[string]any)
	if !ok {
		return nil, errors.New("profiles must map profile names to descriptions or definitions")
	}
	if len(entries) > maxProfiles {
		return nil, errors.Errorf("profiles may list at most %d profiles", maxProfiles)
	}
	catalog := make(profileCatalog, 0, len(entries))
	names := map[string]bool{}
	for key, value := range entries {
		profile, err := parseProfile(key, value)
		if err != nil {
			return nil, errors.Wrapf(err, "invalid profile %q", key)
		}
		for _, name := range []string{profile.Name, profile.Alias} {
			if name == "" {
				continue
			}
			if names[name] {
				return nil, errors.Errorf("profile %q is listed twice", name)
			}
			names[name] = true
		}
		catalog = append(catalog, profile)
	}
	sort.Slice(catalog, func(i, j int) bool { return catalog[i].Name < catalog[j].Name })
	return catalog, nil
}

func parseProfile(key string, value any) (scheduleProfile, error) {
	if !profileNamePattern.MatchString(key) {
		return scheduleProfile{}, errors.New("profile names must be ASCII slugs matching [A-Za-z0-9][A-Za-z0-9._-]* (1-128 characters)")
	}
	profile := scheduleProfile{Name: key}
	var definition map[string]any
	switch value := value.(type) {
	case nil:
	case string:
		profile.Description = value
	case map[string]any:
		definition = value
	default:
		return scheduleProfile{}, errors.New("must be a description or a definition")
	}
	if definition != nil {
		description, ok := definition["description"].(string)
		if _, set := definition["description"]; set && !ok {
			return scheduleProfile{}, errors.New("description must be a string")
		}
		profile.Description = description
	}
	description, err := normalizeProfileDescription(profile.Description)
	if err != nil {
		return scheduleProfile{}, err
	}
	profile.Description = description
	_, hasProvider := definition["provider"]
	_, hasModel := definition["model"]
	if !hasProvider && !hasModel {
		for setting := range definition {
			if setting != "description" {
				return scheduleProfile{}, errors.Errorf("%s needs provider and model to define a profile", setting)
			}
		}
		return profile, nil
	}
	registration, err := parseDefinition(key, definition)
	if err != nil {
		return scheduleProfile{}, err
	}
	profile.Alias = key
	profile.Name = registration.Name
	profile.Registration = &registration
	return profile, nil
}

func parseDefinition(key string, definition map[string]any) (extension.ProfileRegistration, error) {
	registration := extension.ProfileRegistration{Name: definedProfilePrefix + key, Hidden: true}
	if !profileNamePattern.MatchString(registration.Name) {
		return registration, errors.Errorf("names of defined profiles must be at most %d characters", 128-len(definedProfilePrefix))
	}
	provider, _ := definition["provider"].(string)
	registration.Provider = strings.ToLower(strings.TrimSpace(provider))
	if registration.Provider != "openai" && registration.Provider != "anthropic" {
		return registration, errors.New("provider must be openai or anthropic")
	}
	model, _ := definition["model"].(string)
	registration.Model = strings.TrimSpace(model)
	if registration.Model == "" || strings.ContainsRune(registration.Model, 0) {
		return registration, errors.New("model must be a nonempty string")
	}
	if hidden, set := definition["hidden"]; set {
		value, ok := hidden.(bool)
		if !ok {
			return registration, errors.New("hidden must be true or false")
		}
		registration.Hidden = value
	}
	settings := map[string]any{}
	for setting, value := range definition {
		if profileSettingKeys[setting] {
			continue
		}
		if setting == "name" {
			return registration, errors.New("name cannot be set; the entry's key names the profile")
		}
		if path := credentialSetting(setting, value); path != "" {
			return registration, errors.Errorf("%s cannot be set here; define profiles with custom endpoints or credentials in Kodelet's profiles", path)
		}
		settings[setting] = value
	}
	if len(settings) > 0 {
		registration.Settings = settings
	}
	return registration, nil
}

// credentialSetting returns the path of a credential setting, or "".
func credentialSetting(key string, value any) string {
	if credentialSettings[key] {
		return key
	}
	switch value := value.(type) {
	case map[string]any:
		keys := make([]string, 0, len(value))
		for nested := range value {
			keys = append(keys, nested)
		}
		sort.Strings(keys)
		for _, nested := range keys {
			if path := credentialSetting(nested, value[nested]); path != "" {
				return key + "." + path
			}
		}
	case []any:
		for i, item := range value {
			if path := credentialSetting(fmt.Sprint(i), item); path != "" {
				return key + "." + path
			}
		}
	}
	return ""
}

// normalizeProfileDescription collapses whitespace onto one line.
func normalizeProfileDescription(description string) (string, error) {
	description = strings.Join(strings.Fields(description), " ")
	if len(description) > maxProfileDescription {
		return "", errors.Errorf("description must be at most %d characters", maxProfileDescription)
	}
	return description, nil
}

// resolve maps a profile's name or alias to the name runs use.
func (c profileCatalog) resolve(name string) (string, error) {
	name = strings.TrimSpace(name)
	for _, profile := range c {
		if name == profile.Name || (profile.Alias != "" && name == profile.Alias) {
			return profile.Name, nil
		}
	}
	names := make([]string, len(c))
	for i, profile := range c {
		names[i] = profile.Name
	}
	return "", errors.Errorf("unknown profile %q; use one of %s, or omit profile to use this conversation's profile", name, strings.Join(names, ", "))
}

// list returns a "- name: description" line per profile.
func (c profileCatalog) list() string {
	var b strings.Builder
	for _, profile := range c {
		b.WriteString("\n- ")
		b.WriteString(profile.Name)
		if profile.Description != "" {
			b.WriteString(": ")
			b.WriteString(profile.Description)
		}
	}
	return b.String()
}

// defined returns the profiles this extension defines.
func (c profileCatalog) defined() []extension.ProfileRegistration {
	var registrations []extension.ProfileRegistration
	for _, profile := range c {
		if profile.Registration != nil {
			registrations = append(registrations, *profile.Registration)
		}
	}
	return registrations
}

// withoutDefinitions drops defined profiles, for hosts that cannot register them.
func (c profileCatalog) withoutDefinitions() profileCatalog {
	var kept profileCatalog
	for _, profile := range c {
		if profile.Registration == nil {
			kept = append(kept, profile)
		}
	}
	return kept
}
