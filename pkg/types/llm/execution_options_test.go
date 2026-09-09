package llm

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestExtensionProfileOptionsJSONRoundTrip(t *testing.T) {
	inputs := []string{
		`{"provider":"openai","model":"search"}`,
		`{"provider":"openai","model":"search","openai":{}}`,
		`{"provider":"openai","model":"search","weakModel":"weak","maxTokens":4096,"weakModelMaxTokens":2048,"thinkingBudgetTokens":0,"reasoningEffort":"none","openai":{"platform":"codex","api_mode":"responses","service_tier":"fast"}}`,
		`{"provider":"openai","model":"search","openai":{"platform":"codex","api_mode":"chat_completions"}}`,
		`{"provider":"openai","model":"search","openai":{"platform":"future","api_mode":false,"service_tier":null,"future":{"enabled":false,"values":[1,null,{"key":"value"}]}}}`,
		`{"provider":"openai","model":"search","openai":{"base_url":"https://example.invalid","api_key_env_var":"KEY","api_key":"example","account":"other"}}`,
		`{"provider":"anthropic","model":"search","anthropic":{}}`,
		`{"provider":"anthropic","model":"search","anthropic":{"platform":"copilot"}}`,
		`{"provider":"anthropic","model":"search","anthropic":{"platform":"copilot","future":{"values":[false,null]}},"anthropicAPIAccess":"subscription"}`,
		`{"provider":"anthropic","model":"search","anthropic":{"base_url":"https://example.invalid","api_key_env_var":"KEY","api_key":"example","account":"other"}}`,
		`{"provider":"anthropic","model":"search","anthropicAPIAccess":"subscription"}`,
	}
	for _, access := range []string{"auto", "subscription", "api-key"} {
		inputs = append(inputs, `{"provider":"anthropic","model":"search","anthropic":{"platform":"anthropic"},"anthropicAPIAccess":"`+access+`"}`)
	}
	for _, input := range inputs {
		t.Run(input, func(t *testing.T) {
			var options ExtensionProfileOptions
			require.NoError(t, json.Unmarshal([]byte(input), &options))
			require.NoError(t, options.Validate())
			data, err := json.Marshal(options)
			require.NoError(t, err)
			assert.JSONEq(t, input, string(data))
			assert.Equal(t, &options, options.Clone())
		})
	}
	var absent *ExtensionProfileOptions
	assert.Nil(t, absent.Clone())
	assert.Nil(t, absent.ModelOptions())
	assert.ErrorContains(t, absent.Validate(), "require provider and model")
}

func TestExtensionProfileOptionsRejectInvalidJSON(t *testing.T) {
	inputs := []string{
		`null`, `[]`, `true`, `"options"`, `123`, `{} {}`, `{`, `{}`,
		`{"provider":"openai"}`, `{"model":"search"}`,
		`{"provider":"","model":"search"}`, `{"provider":"openai","model":""}`,
		`{"provider":"unknown","model":"search"}`,
		`{"provider":"openai","model":" "}`,
		`{"provider":"openai","model":"bad\u0000model"}`,
		`{"provider":"openai","model":"search","maxTokens":0}`,
		`{"provider":"openai","model":"search","weakModelMaxTokens":-1}`,
		`{"provider":"openai","model":"search","thinkingBudgetTokens":-1}`,
		`{"provider":"openai","model":"search","reasoningEffort":"unknown"}`,
		`{"provider":"openai","model":"search","weakModel":""}`,
		`{"provider":"openai","model":"search","anthropic":{}}`,
		`{"provider":"openai","model":"search","anthropicAPIAccess":"subscription"}`,
		`{"provider":"anthropic","model":"search","openai":{}}`,
	}
	for _, field := range []string{
		`"maxTurns":0`, `"useWeakModel":false`, `"noTools":false`,
		`"noExtensions":false`, `"noSkills":false`, `"allowedTools":[]`,
		`"allowedCommands":[]`, `"enableFSSearchTools":false`,
		`"apiKey":"secret"`, `"baseURL":"https://untrusted.invalid"`,
		`"anthropicAccount":"other"`, `"sysprompt":"/file"`, `"unknown":true`,
	} {
		inputs = append(inputs, `{"provider":"openai","model":"search",`+field+`}`)
	}
	for _, provider := range []string{"openai", "anthropic"} {
		for _, nested := range []string{`null`, `[]`, `true`, `"settings"`, `12`} {
			inputs = append(inputs, `{"provider":"`+provider+`","model":"search","`+provider+`":`+nested+`}`)
		}
	}
	for _, access := range []string{"", "token", " SUBSCRIPTION "} {
		inputs = append(inputs, `{"provider":"anthropic","model":"search","anthropicAPIAccess":"`+access+`"}`)
	}
	typ := reflect.TypeFor[ExtensionProfileOptions]()
	for i := range typ.NumField() {
		name := strings.Split(typ.Field(i).Tag.Get("json"), ",")[0]
		inputs = append(inputs, `{"provider":"openai","model":"search","`+name+`":null}`)
	}
	for _, input := range inputs {
		t.Run(input, func(t *testing.T) {
			options := ExtensionProfileOptions{Provider: new("openai"), Model: new("unchanged")}
			before := options.Clone()
			require.Error(t, json.Unmarshal([]byte(input), &options))
			assert.Equal(t, before, &options)
		})
	}
}

func TestExtensionProfileOptionsCloneAndModelOptions(t *testing.T) {
	options := &ExtensionProfileOptions{
		Provider:             new("openai"),
		Model:                new("main"),
		WeakModel:            new("weak"),
		MaxTokens:            new(4096),
		WeakModelMaxTokens:   new(2048),
		ThinkingBudgetTokens: new(0),
		ReasoningEffort:      new("none"),
		OpenAI: map[string]any{
			"future": map[string]any{"values": []any{map[string]any{"enabled": false}, nil}},
		},
	}
	cloned := options.Clone()
	assert.Equal(t, options, cloned)
	originalValue, clonedValue := reflect.ValueOf(*options), reflect.ValueOf(*cloned)
	for i := range originalValue.NumField() {
		if originalValue.Field(i).Kind() == reflect.Pointer && !originalValue.Field(i).IsNil() {
			assert.NotEqual(t, originalValue.Field(i).Pointer(), clonedValue.Field(i).Pointer(), originalValue.Type().Field(i).Name)
		}
	}
	cloned.OpenAI["added"] = true
	clonedValues := cloned.OpenAI["future"].(map[string]any)["values"].([]any)
	clonedValues[0].(map[string]any)["enabled"] = true
	clonedValues[1] = "changed"
	assert.NotContains(t, options.OpenAI, "added")
	assert.Equal(t, map[string]any{"values": []any{map[string]any{"enabled": false}, nil}}, options.OpenAI["future"])
	model := options.ModelOptions()
	assert.Equal(t, &ExecutionOptions{
		Provider:             new("openai"),
		Model:                new("main"),
		WeakModel:            new("weak"),
		MaxTokens:            new(4096),
		WeakModelMaxTokens:   new(2048),
		ThinkingBudgetTokens: new(0),
		ReasoningEffort:      new("none"),
	}, model)
	modelValue := reflect.ValueOf(*model)
	for i := range modelValue.NumField() {
		if !modelValue.Field(i).IsNil() {
			name := modelValue.Type().Field(i).Name
			assert.NotEqual(t, originalValue.FieldByName(name).Pointer(), modelValue.Field(i).Pointer(), name)
		}
	}
	*model.Model = "changed"
	assert.Equal(t, "main", *options.Model)
	options.Provider = new("anthropic")
	options.OpenAI = nil
	options.Anthropic = map[string]any{"platform": "anthropic", "future": []any{map[string]any{"enabled": false}}}
	options.AnthropicAPIAccess = new(AnthropicAPIAccessSubscription)
	cloned = options.Clone()
	assert.Equal(t, options, cloned)
	cloned.Anthropic["platform"] = "copilot"
	cloned.Anthropic["future"].([]any)[0].(map[string]any)["enabled"] = true
	*cloned.AnthropicAPIAccess = AnthropicAPIAccessAPIKey
	assert.Equal(t, "anthropic", options.Anthropic["platform"])
	assert.Equal(t, []any{map[string]any{"enabled": false}}, options.Anthropic["future"])
	assert.Equal(t, AnthropicAPIAccessSubscription, *options.AnthropicAPIAccess)
}

func TestExecutionOptionsJSONPresence(t *testing.T) {
	var omitted ExecutionOptions
	require.NoError(t, json.Unmarshal([]byte(`{}`), &omitted))
	assert.Equal(t, ExecutionOptions{}, omitted)
	assert.True(t, omitted.ToolAllowed("bash"))
	assert.False(t, omitted.ToolsDisabled())

	input := `{"maxTurns":0,"thinkingBudgetTokens":0,"useWeakModel":false,"noTools":false,"noExtensions":false,"noSkills":false,"enableFSSearchTools":false,"allowedTools":[],"allowedCommands":[]}`
	var explicit ExecutionOptions
	require.NoError(t, json.Unmarshal([]byte(input), &explicit))
	assert.Equal(t, new(0), explicit.MaxTurns)
	assert.Equal(t, new(0), explicit.ThinkingBudgetTokens)
	for _, value := range []*bool{explicit.UseWeakModel, explicit.NoTools, explicit.NoExtensions, explicit.NoSkills, explicit.EnableFSSearchTools} {
		assert.Equal(t, new(false), value)
	}
	require.NotNil(t, explicit.AllowedTools)
	require.NotNil(t, explicit.AllowedCommands)
	assert.Equal(t, []string{}, *explicit.AllowedTools)
	assert.Equal(t, []string{}, *explicit.AllowedCommands)
	assert.True(t, explicit.ToolsDisabled(), "an empty allowlist wins over noTools=false")
	data, err := json.Marshal(explicit.Clone())
	require.NoError(t, err)
	assert.JSONEq(t, input, string(data))

	var absent *ExecutionOptions
	require.NoError(t, absent.Validate())
	assert.Nil(t, absent.Clone())
	assert.Nil(t, absent.Restrictions())
	assert.True(t, absent.ToolAllowed("bash"))
	assert.False(t, absent.ToolsDisabled())
	assert.False(t, absent.HasModelOptions())
}

func TestExecutionOptionsNilSlicePointersMarshalAsDenyAll(t *testing.T) {
	options := ExecutionOptions{AllowedTools: new([]string(nil)), AllowedCommands: new([]string(nil))}
	data, err := json.Marshal(options)
	require.NoError(t, err)
	assert.JSONEq(t, `{"allowedTools":[],"allowedCommands":[]}`, string(data))
	var decoded ExecutionOptions
	require.NoError(t, json.Unmarshal(data, &decoded))
	assert.True(t, decoded.ToolsDisabled())
	assert.False(t, decoded.ToolAllowed("bash"))
	assert.Nil(t, *options.AllowedTools, "serialization does not mutate the caller's lists")
	assert.Nil(t, *options.AllowedCommands)
}

func TestExecutionOptionsRejectInvalidJSON(t *testing.T) {
	inputs := []string{
		`null`, `[]`, `true`, `"options"`, `{} {}`, `{`,
		`{"unknown":true}`, `{"noSave":false}`, `{"max_tokens":100}`,
		`{"openai":{"base_url":"https://untrusted.invalid"}}`,
		`{"openai":{"platform":"codex"}}`, `{"anthropic":{"platform":"anthropic"}}`,
		`{"anthropicAPIAccess":"subscription"}`,
		`{"apiKey":"secret"}`, `{"extensions":{}}`, `{"sysprompt":"/client/file"}`,
		`{"noTools":"false"}`, `{"maxTurns":1.5}`, `{"allowedTools":"bash"}`,
		`{"provider":"other"}`, `{"model":" "}`, `{"weakModel":""}`,
		`{"model":"bad\u0000model"}`, `{"reasoningEffort":"extreme"}`,
		`{"maxTokens":0}`, `{"weakModelMaxTokens":-1}`, `{"maxTurns":-1}`,
		`{"thinkingBudgetTokens":-1}`, `{"allowedTools":[null]}`,
		`{"allowedTools":[""]}`, `{"allowedCommands":[" "]}`,
		`{"allowedCommands":["ls\u0000"]}`,
	}
	// Every known option must reject null, including new fields added later.
	typ := reflect.TypeFor[ExecutionOptions]()
	for i := range typ.NumField() {
		name := strings.Split(typ.Field(i).Tag.Get("json"), ",")[0]
		inputs = append(inputs, `{"`+name+`":null}`)
	}
	for _, input := range inputs {
		t.Run(input, func(t *testing.T) {
			options := ExecutionOptions{NoTools: new(true)}
			require.Error(t, json.Unmarshal([]byte(input), &options))
			assert.Equal(t, ExecutionOptions{NoTools: new(true)}, options, "failed decoding must not replace retained options")
		})
	}
}

func TestExecutionOptionsCloneAndRestrictions(t *testing.T) {
	var options ExecutionOptions
	require.NoError(t, json.Unmarshal([]byte(`{"provider":"anthropic","model":"main","weakModel":"weak","maxTokens":4096,"weakModelMaxTokens":2048,"thinkingBudgetTokens":1024,"reasoningEffort":"high","maxTurns":2,"useWeakModel":true,"noTools":false,"noExtensions":true,"noSkills":true,"enableFSSearchTools":false,"allowedTools":["file_read"],"allowedCommands":["git status"]}`), &options))
	cloned := options.Clone()
	assert.Equal(t, &options, cloned)
	// All scalar pointers and allowlist storage belong to the clone.
	originalValue, clonedValue := reflect.ValueOf(options), reflect.ValueOf(*cloned)
	for i := range originalValue.NumField() {
		assert.NotEqual(t, originalValue.Field(i).Pointer(), clonedValue.Field(i).Pointer(), originalValue.Type().Field(i).Name)
	}
	*cloned.Model = "changed"
	(*cloned.AllowedTools)[0] = "bash"
	(*cloned.AllowedCommands)[0] = "rm *"
	assert.Equal(t, "main", *options.Model)
	assert.Equal(t, []string{"file_read"}, *options.AllowedTools)
	assert.Equal(t, []string{"git status"}, *options.AllowedCommands)

	restrictions := options.Restrictions()
	assert.True(t, options.HasModelOptions())
	assert.False(t, restrictions.HasModelOptions())
	assert.Nil(t, restrictions.MaxTurns)
	assert.Nil(t, restrictions.UseWeakModel)
	assert.Equal(t, options.NoExtensions, restrictions.NoExtensions)
	assert.Equal(t, options.NoSkills, restrictions.NoSkills)
	(*restrictions.AllowedTools)[0] = "changed"
	assert.Equal(t, "file_read", (*options.AllowedTools)[0])
	assert.False(t, (&ExecutionOptions{MaxTurns: new(0), UseWeakModel: new(false)}).HasModelOptions())
}

func TestExecutionOptionsToolPolicy(t *testing.T) {
	for _, tt := range []struct {
		name    string
		options ExecutionOptions
		allowed []string
		denied  []string
	}{
		{"omitted", ExecutionOptions{}, []string{"bash", "file_read", "ext_tool", "web_search"}, nil},
		{"explicit false", ExecutionOptions{NoTools: new(false)}, []string{"bash", "ext_tool", "web_search"}, nil},
		{"tool free", ExecutionOptions{NoTools: new(true)}, nil, []string{"bash", "file_read", "ext_tool", "web_search"}},
		{"empty tools", ExecutionOptions{AllowedTools: new([]string{})}, nil, []string{"bash", "ext_tool", "web_search"}},
		{"allowlist", ExecutionOptions{AllowedTools: new([]string{"file_read", "ext_tool"})}, []string{"file_read", "ext_tool"}, []string{"bash", "web_search"}},
		{"empty commands", ExecutionOptions{AllowedCommands: new([]string{})}, []string{"file_read"}, []string{"bash"}},
		{"skills disabled", ExecutionOptions{NoSkills: new(true)}, []string{"file_read"}, []string{"skill"}},
		{"search disabled", ExecutionOptions{EnableFSSearchTools: new(false)}, []string{"file_read"}, []string{"glob_tool", "grep_tool"}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			for _, name := range tt.allowed {
				assert.True(t, tt.options.ToolAllowed(name), name)
			}
			for _, name := range tt.denied {
				assert.False(t, tt.options.ToolAllowed(name), name)
			}
		})
	}
}

func TestApplyEnvironmentOptionsNarrowsPolicy(t *testing.T) {
	for _, tt := range []struct {
		name      string
		host      []string
		requested *[]string
		want      *[]string
	}{
		{"unrestricted omitted", nil, nil, nil},
		{"legacy empty omitted", []string{}, nil, nil},
		{"inherit host", []string{"bash", "file_read"}, nil, new([]string{"bash", "file_read"})},
		{"explicit empty", nil, new([]string{}), new([]string{})},
		{"empty narrows host", []string{"bash"}, new([]string{}), new([]string{})},
		{"restrict default", nil, new([]string{"file_read"}), new([]string{"file_read"})},
		{"cannot widen host", []string{"file_read"}, new([]string{"file_read", "bash"}), new([]string{"file_read"})},
		{"disjoint denies all", []string{"file_read"}, new([]string{"bash"}), new([]string{})},
	} {
		t.Run(tt.name, func(t *testing.T) {
			host := Config{AllowedTools: tt.host, AllowedCommands: tt.host}
			options := &ExecutionOptions{AllowedTools: tt.requested, AllowedCommands: tt.requested}
			config, err := ApplyEnvironmentOptions(host, options)
			require.NoError(t, err)
			assert.Equal(t, tt.want, config.ExecutionOptions.AllowedTools)
			assert.Equal(t, tt.want, config.ExecutionOptions.AllowedCommands)
			if tt.want != nil {
				assert.Equal(t, *tt.want, config.AllowedCommands, "bash must receive the effective command policy")
				if len(*tt.want) == 0 {
					assert.False(t, config.ExecutionOptions.ToolAllowed("bash"))
				}
			}
			assert.Equal(t, tt.host, host.AllowedCommands)
			assert.Equal(t, tt.requested, options.AllowedCommands)
		})
	}
}

func TestApplyEnvironmentOptionsFeaturePolicy(t *testing.T) {
	host := Config{
		EnableFSSearchTools: true,
		Skills:              &SkillsConfig{Enabled: true, Allowed: []string{"review"}},
		ExtensionSettings:   map[string]any{"enabled": true, "commands": []string{"server"}},
		Extensions:          new("runtime"),
	}
	config, err := ApplyEnvironmentOptions(host, &ExecutionOptions{NoExtensions: new(true), NoSkills: new(true), EnableFSSearchTools: new(false)})
	require.NoError(t, err)
	assert.False(t, config.EnableFSSearchTools)
	assert.False(t, config.Skills.Enabled)
	assert.Equal(t, false, config.ExtensionSettings["enabled"])
	assert.Nil(t, config.Extensions)
	assert.True(t, host.EnableFSSearchTools)
	assert.True(t, host.Skills.Enabled)
	assert.Equal(t, true, host.ExtensionSettings["enabled"])
	assert.NotNil(t, host.Extensions)

	_, err = ApplyEnvironmentOptions(config, &ExecutionOptions{EnableFSSearchTools: new(true)})
	require.ErrorContains(t, err, "runner policy")
	config, err = ApplyEnvironmentOptions(config, &ExecutionOptions{NoTools: new(false), NoExtensions: new(false), NoSkills: new(false)})
	require.NoError(t, err)
	assert.Equal(t, new(true), config.ExecutionOptions.NoExtensions)
	assert.Equal(t, new(true), config.ExecutionOptions.NoSkills)
	assert.False(t, config.Skills.Enabled)
	assert.Equal(t, false, config.ExtensionSettings["enabled"])

	for _, options := range []*ExecutionOptions{{Model: new("main")}, {Provider: new("openai")}, {MaxTurns: new(0)}, {UseWeakModel: new(false)}, {AllowedCommands: new([]string{""})}} {
		_, err := ApplyEnvironmentOptions(host, options)
		require.Error(t, err)
	}
}

func TestApplyEnvironmentOptionsFilesystemSelectionAndCeiling(t *testing.T) {
	host := Config{ToolMode: ToolModePatch, EnableFSSearchTools: false}
	for _, options := range []*ExecutionOptions{nil, {}} {
		config, err := ApplyEnvironmentOptions(host, options)
		require.NoError(t, err)
		assert.False(t, config.EnableFSSearchTools, "omission preserves runner presentation")
		assert.Nil(t, config.ExecutionOptions.EnableFSSearchTools)
	}
	selected, err := ApplyEnvironmentOptions(host, &ExecutionOptions{EnableFSSearchTools: new(true)})
	require.NoError(t, err)
	assert.True(t, selected.EnableFSSearchTools)
	assert.Equal(t, ToolModePatch, selected.ToolMode)
	assert.False(t, host.EnableFSSearchTools, "request selection does not mutate defaults")
	assert.Nil(t, host.ExecutionOptions)

	denied, err := ApplyEnvironmentOptions(host, &ExecutionOptions{EnableFSSearchTools: new(false)})
	require.NoError(t, err)
	for _, inherited := range []Config{denied, {EnableFSSearchTools: true, ExecutionOptions: &ExecutionOptions{EnableFSSearchTools: new(false)}}} {
		_, err = ApplyEnvironmentOptions(inherited, &ExecutionOptions{EnableFSSearchTools: new(true)})
		require.ErrorContains(t, err, "runner policy")
		preserved, err := ApplyEnvironmentOptions(inherited, nil)
		require.NoError(t, err)
		assert.False(t, preserved.EnableFSSearchTools)
		assert.Equal(t, new(false), preserved.ExecutionOptions.EnableFSSearchTools)
	}
}

func TestApplyEnvironmentOptionsPreservesInheritedRestrictions(t *testing.T) {
	for _, options := range []*ExecutionOptions{
		nil,
		{},
		{NoTools: new(false), NoExtensions: new(false), NoSkills: new(false)},
		{AllowedTools: new([]string{"bash"}), AllowedCommands: new([]string{"rm *"})},
	} {
		host := Config{
			EnableFSSearchTools: true,
			ExecutionOptions: &ExecutionOptions{
				NoTools: new(true), NoExtensions: new(true), NoSkills: new(true),
				EnableFSSearchTools: new(false), AllowedTools: new([]string{}), AllowedCommands: new([]string{}),
			},
		}
		config, err := ApplyEnvironmentOptions(host, options)
		require.NoError(t, err)
		assert.True(t, config.ExecutionOptions.ToolsDisabled())
		assert.Equal(t, new(true), config.ExecutionOptions.NoTools)
		assert.Equal(t, new(true), config.ExecutionOptions.NoExtensions)
		assert.Equal(t, new(true), config.ExecutionOptions.NoSkills)
		assert.Equal(t, new(false), config.ExecutionOptions.EnableFSSearchTools)
		assert.Equal(t, new([]string{}), config.ExecutionOptions.AllowedTools)
		assert.Equal(t, new([]string{}), config.ExecutionOptions.AllowedCommands)
		assert.False(t, config.EnableFSSearchTools)
		assert.False(t, config.Skills.Enabled)
		assert.Equal(t, false, config.ExtensionSettings["enabled"])
		assert.Nil(t, host.Skills, "restrictions do not mutate the inherited host config")
		assert.Nil(t, host.ExtensionSettings)
	}
	// Even without noTools, an inherited empty allowlist must not become the
	// unrestricted legacy Config empty list on a subsequent application.
	host, err := ApplyEnvironmentOptions(Config{}, &ExecutionOptions{AllowedTools: new([]string{}), AllowedCommands: new([]string{})})
	require.NoError(t, err)
	config, err := ApplyEnvironmentOptions(host, &ExecutionOptions{AllowedTools: new([]string{"bash"}), AllowedCommands: new([]string{"git status"})})
	require.NoError(t, err)
	assert.True(t, config.ExecutionOptions.ToolsDisabled())
	assert.False(t, config.ExecutionOptions.ToolAllowed("bash"))

	_, err = ApplyEnvironmentOptions(Config{
		EnableFSSearchTools: true,
		ExecutionOptions:    &ExecutionOptions{EnableFSSearchTools: new(false)},
	}, &ExecutionOptions{EnableFSSearchTools: new(true)})
	require.ErrorContains(t, err, "runner policy")
}
