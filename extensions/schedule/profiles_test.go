package main

import (
	"testing"

	"github.com/jingkaihe/kodelet/sdk/go/extension"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func profilesConfig() map[string]any {
	return map[string]any{"profiles": map[string]any{
		"smart": "  Everyday checks\n and summaries ",
		"deep": map[string]any{
			"description":      "Suitable for deep engineering work",
			"provider":         "anthropic",
			"model":            "claude-opus",
			"reasoning_effort": "high",
		},
		"visible":   map[string]any{"provider": "openai", "model": "gpt-5", "hidden": false, "openai": map[string]any{"service_tier": "flex"}},
		"described": map[string]any{"description": "A profile Kodelet already has"},
	}}
}

func TestParseProfiles(t *testing.T) {
	catalog, err := parseProfiles(profilesConfig())
	require.NoError(t, err)
	assert.Equal(t, profileCatalog{
		{Name: "described", Description: "A profile Kodelet already has"},
		{
			Name: "schedule-deep", Alias: "deep", Description: "Suitable for deep engineering work",
			Registration: &extension.ProfileRegistration{
				Name: "schedule-deep", Provider: "anthropic", Model: "claude-opus", Hidden: true,
				Settings: map[string]any{"reasoning_effort": "high"},
			},
		},
		{
			Name: "schedule-visible", Alias: "visible",
			Registration: &extension.ProfileRegistration{
				Name: "schedule-visible", Provider: "openai", Model: "gpt-5",
				Settings: map[string]any{"openai": map[string]any{"service_tier": "flex"}},
			},
		},
		{Name: "smart", Description: "Everyday checks and summaries"},
	}, catalog)
	assert.Equal(t, "\n- described: A profile Kodelet already has\n- schedule-deep: Suitable for deep engineering work\n- schedule-visible\n- smart: Everyday checks and summaries", catalog.list())
	assert.Len(t, catalog.defined(), 2)
	assert.Len(t, catalog.withoutDefinitions(), 2)

	for _, name := range []string{"deep", "schedule-deep", " deep "} {
		resolved, err := catalog.resolve(name)
		require.NoError(t, err, name)
		assert.Equal(t, "schedule-deep", resolved)
	}
	resolved, err := catalog.resolve("smart")
	require.NoError(t, err)
	assert.Equal(t, "smart", resolved)
	_, err = catalog.resolve("opus")
	assert.ErrorContains(t, err, `unknown profile "opus"; use one of described, schedule-deep, schedule-visible, smart`)

	empty, err := parseProfiles(map[string]any{})
	require.NoError(t, err)
	assert.Empty(t, empty)
}

func TestParseProfilesRejectsInvalidEntries(t *testing.T) {
	long := make([]byte, maxProfileDescription+1)
	for i := range long {
		long[i] = 'a'
	}
	tests := map[string]struct {
		profiles any
		want     string
	}{
		"not a map":          {profiles: []any{"smart"}, want: "must map profile names"},
		"bad name":           {profiles: map[string]any{"-bad": "x"}, want: "ASCII slugs"},
		"bad value":          {profiles: map[string]any{"smart": 1.0}, want: "description or a definition"},
		"long description":   {profiles: map[string]any{"smart": string(long)}, want: "at most 200 characters"},
		"provider only":      {profiles: map[string]any{"deep": map[string]any{"provider": "anthropic"}}, want: "model must be"},
		"model only":         {profiles: map[string]any{"deep": map[string]any{"model": "claude"}}, want: "provider must be"},
		"unknown provider":   {profiles: map[string]any{"deep": map[string]any{"provider": "gemini", "model": "m"}}, want: "provider must be"},
		"settings only":      {profiles: map[string]any{"deep": map[string]any{"reasoning_effort": "high"}}, want: "needs provider and model"},
		"hidden not boolean": {profiles: map[string]any{"deep": map[string]any{"provider": "openai", "model": "m", "hidden": "yes"}}, want: "hidden must be"},
		"name setting":       {profiles: map[string]any{"deep": map[string]any{"provider": "openai", "model": "m", "name": "x"}}, want: "name cannot be set"},
		"base url":           {profiles: map[string]any{"deep": map[string]any{"provider": "openai", "model": "m", "openai": map[string]any{"base_url": "https://evil.example"}}}, want: "openai.base_url cannot be set"},
		"api key variable":   {profiles: map[string]any{"deep": map[string]any{"provider": "anthropic", "model": "m", "api_key_env_var": "OTHER_KEY"}}, want: "api_key_env_var cannot be set"},
		"clashing names":     {profiles: map[string]any{"deep": map[string]any{"provider": "openai", "model": "m"}, "schedule-deep": "x"}, want: "listed twice"},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			_, err := parseProfiles(map[string]any{"profiles": tt.profiles})
			assert.ErrorContains(t, err, tt.want)
		})
	}
	many := map[string]any{}
	for i := range maxProfiles + 1 {
		many[string(rune('a'+i%26))+string(rune('a'+i/26))] = "x"
	}
	_, err := parseProfiles(map[string]any{"profiles": many})
	assert.ErrorContains(t, err, "at most 32 profiles")
}
