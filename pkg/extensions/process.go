package extensions

import (
	"context"
	"encoding/json"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/jingkaihe/kodelet/pkg/osutil"
	conversationtypes "github.com/jingkaihe/kodelet/pkg/types/conversations"
	"github.com/pkg/errors"
)

// Process is a running extension subprocess.
type Process struct {
	Extension Extension
	cmd       *exec.Cmd
	client    *rpcClient
	stdin     io.WriteCloser
	stdout    io.ReadCloser
	config    Config
	cwd       string
	mu        sync.Mutex
	closed    bool
	shutdown  bool
	disabled  bool
	failures  int
}

const (
	maxProcessFailures = 3
	restartBackoffBase = 100 * time.Millisecond
	restartBackoffMax  = 2 * time.Second
)

// StartProcess starts an extension subprocess and initializes its JSON-RPC client.
func StartProcess(ctx context.Context, ext Extension, config Config) (*Process, error) {
	p := &Process{
		Extension: ext,
		config:    config,
		closed:    true,
	}
	if err := p.start(ctx); err != nil {
		return nil, err
	}
	return p, nil
}

func (p *Process) start(ctx context.Context) error {
	cmd := exec.CommandContext(processContext(ctx), p.Extension.ExecPath)
	cmd.Dir = p.Extension.Dir
	cmd.Env = extensionEnv(p.config, p.Extension)
	cmd.Stderr = os.Stderr
	osutil.SetProcessGroup(cmd)
	osutil.SetProcessGroupKill(cmd)

	stdin, err := cmd.StdinPipe()
	if err != nil {
		return errors.Wrap(err, "failed to create extension stdin")
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return errors.Wrap(err, "failed to create extension stdout")
	}
	if err := cmd.Start(); err != nil {
		return errors.Wrap(err, "failed to start extension process")
	}

	p.cmd = cmd
	p.client = newRPCClient(stdout, stdin, p.config.Timeout)
	p.stdin = stdin
	p.stdout = stdout
	p.closed = false
	return nil
}

func processContext(ctx context.Context) context.Context {
	if ctx == nil {
		return context.Background()
	}
	return context.WithoutCancel(ctx)
}

func (p *Process) ensureRunning(ctx context.Context) error {
	p.mu.Lock()
	defer p.mu.Unlock()

	if p.disabled {
		return errors.Errorf("extension %s disabled after repeated failures", p.Extension.ID)
	}
	if p.shutdown {
		return errors.Errorf("extension %s is shut down", p.Extension.ID)
	}
	if !p.closed {
		return nil
	}

	backoff := restartBackoffBase
	for range max(0, p.failures-1) {
		backoff *= 2
		if backoff >= restartBackoffMax {
			backoff = restartBackoffMax
			break
		}
	}
	if backoff > restartBackoffMax {
		backoff = restartBackoffMax
	}
	if backoff > 0 {
		timer := time.NewTimer(backoff)
		select {
		case <-ctx.Done():
			if !timer.Stop() {
				select {
				case <-timer.C:
				default:
				}
			}
			return ctx.Err()
		case <-timer.C:
		}
	}

	if err := p.start(ctx); err != nil {
		p.recordFailureLocked()
		return err
	}

	initCtx := ctx
	cancel := func() {}
	if timeout := timeoutOrDefault(p.config.Timeout, DefaultConfig().Timeout); timeout > 0 {
		var cancelFunc context.CancelFunc
		initCtx, cancelFunc = context.WithTimeout(ctx, timeout)
		cancel = cancelFunc
	}
	_, err := p.initialize(initCtx, p.cwd)
	cancel()
	if err != nil {
		p.closeProcessLocked()
		p.recordFailureLocked()
		return err
	}
	p.failures = 0
	return nil
}

func (p *Process) recordFailureLocked() {
	if p.shutdown || p.disabled {
		return
	}
	p.failures++
	if p.failures >= maxProcessFailures {
		p.disabled = true
	}
}

// Initialize initializes the extension process and returns its registrations.
func (p *Process) Initialize(ctx context.Context, cwd string) (*InitializeResult, error) {
	p.mu.Lock()
	p.cwd = cwd
	p.mu.Unlock()
	return p.initialize(ctx, cwd)
}

func (p *Process) initialize(ctx context.Context, cwd string) (*InitializeResult, error) {
	dataDir, err := extensionDataDir(p.Extension.ID)
	if err != nil {
		return nil, err
	}
	params := initializeParams{
		ProtocolVersion: protocolVersion,
		Kodelet: map[string]any{
			"version": "dev",
		},
		Extension: initializeExtensionInfo{
			ID:      p.Extension.ID,
			Config:  map[string]any{},
			CWD:     cwd,
			DataDir: dataDir,
		},
		Capabilities: map[string]any{
			"tools":    true,
			"commands": true,
			"events": []string{
				"session.start",
				"resources.discover",
				"user.message",
				"agent.init",
				"agent.start",
				"turn.start",
				"tool.call",
				"tool.result",
				"turn.end",
				"agent.end",
				"session.end",
			},
		},
	}

	var result InitializeResult
	if err := p.client.call(ctx, "extension.initialize", params, &result); err != nil {
		return nil, err
	}
	return &result, nil
}

func extensionDataDir(extensionID string) (string, error) {
	basePath, err := conversationtypes.GetDefaultBasePath()
	if err == nil {
		basePath = osutil.CanonicalizePath(basePath)
	} else if home, homeErr := os.UserHomeDir(); homeErr == nil {
		basePath = osutil.CanonicalizePath(filepath.Join(home, ".kodelet"))
	} else {
		return "", errors.Wrap(err, "failed to resolve extension data directory")
	}
	dataDir := filepath.Join(basePath, "extensions", "data", safeDataDirName(extensionID))
	if err := os.MkdirAll(dataDir, 0o700); err != nil {
		return "", errors.Wrap(err, "failed to create extension data directory")
	}
	return osutil.CanonicalizePath(dataDir), nil
}

func safeDataDirName(extensionID string) string {
	extensionID = strings.TrimSpace(extensionID)
	if extensionID == "" {
		return "extension"
	}
	var builder strings.Builder
	for _, char := range extensionID {
		switch {
		case char >= 'a' && char <= 'z':
			builder.WriteRune(char)
		case char >= 'A' && char <= 'Z':
			builder.WriteRune(char)
		case char >= '0' && char <= '9':
			builder.WriteRune(char)
		case char == '.', char == '-', char == '_', char == '@':
			builder.WriteRune(char)
		default:
			builder.WriteRune('_')
		}
	}
	name := strings.Trim(builder.String(), "._")
	if name == "" {
		return "extension"
	}
	return name
}

// ExecuteTool invokes an extension-provided tool.
func (p *Process) ExecuteTool(ctx context.Context, name string, input json.RawMessage, callContext ExtensionCallContext) (*ToolExecutionResult, error) {
	if err := p.ensureRunning(ctx); err != nil {
		return nil, err
	}
	client := p.rpcClient()
	if client == nil {
		return nil, errors.Errorf("extension %s is not running", p.Extension.ID)
	}

	params := executeToolParams{Name: name, Input: input, Context: callContext}
	var result ToolExecutionResult
	if err := client.call(ctx, "extension.tool.execute", params, &result); err != nil {
		if shouldRestartAfterCallError(err) {
			p.closeForRestart()
		}
		return nil, err
	}
	return &result, nil
}

// ExecuteCommand invokes an extension-provided command over JSON-RPC.
func (p *Process) ExecuteCommand(ctx context.Context, name string, input map[string]any, invocation CommandInvocation, callContext ExtensionCallContext) (*CommandResult, error) {
	if err := p.ensureRunning(ctx); err != nil {
		return nil, err
	}
	client := p.rpcClient()
	if client == nil {
		return nil, errors.Errorf("extension %s is not running", p.Extension.ID)
	}

	params := executeCommandParams{Name: name, Input: input, Invocation: invocation, Context: callContext}
	var result CommandResult
	if err := client.call(ctx, "extension.command.execute", params, &result); err != nil {
		if shouldRestartAfterCallError(err) {
			p.closeForRestart()
		}
		return nil, err
	}
	return &result, nil
}

// HandleEvent invokes an extension event handler.
func (p *Process) HandleEvent(ctx context.Context, eventID string, eventName string, payload any, callContext ExtensionCallContext) (*EventResult, error) {
	if err := p.ensureRunning(ctx); err != nil {
		return nil, err
	}
	client := p.rpcClient()
	if client == nil {
		return nil, errors.Errorf("extension %s is not running", p.Extension.ID)
	}

	params := eventParams{ID: eventID, Event: eventName, Context: callContext, Payload: payload}
	var result EventResult
	if err := client.call(ctx, "extension.event.handle", params, &result); err != nil {
		if shouldRestartAfterCallError(err) {
			p.closeForRestart()
		}
		return nil, err
	}
	return &result, nil
}

func (p *Process) rpcClient() *rpcClient {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.closed || p.disabled || p.shutdown {
		return nil
	}
	return p.client
}

func shouldRestartAfterCallError(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
		return true
	}
	return !strings.Contains(err.Error(), "extension rpc error")
}

func (p *Process) closeForRestart() {
	p.mu.Lock()
	defer p.mu.Unlock()
	_ = p.closeProcessLocked()
	p.recordFailureLocked()
}

// Close terminates the extension process.
func (p *Process) Close() error {
	if p == nil {
		return nil
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	p.shutdown = true
	return p.closeProcessLocked()
}

func (p *Process) closeProcessLocked() error {
	if p.closed || p.cmd == nil || p.cmd.Process == nil {
		return nil
	}
	p.closed = true
	_ = p.stdin.Close()
	_ = p.stdout.Close()
	if p.cmd.Cancel != nil {
		_ = p.cmd.Cancel()
	} else {
		_ = p.cmd.Process.Kill()
	}
	_, err := p.cmd.Process.Wait()
	if err != nil && strings.Contains(err.Error(), "process already finished") {
		return nil
	}
	return err
}

func extensionEnv(config Config, ext Extension) []string {
	env := sanitizedExtensionEnv()
	if extensionConfig, ok := config.Processes[ext.ID]; ok {
		env = applyEnvOverrides(env, extensionConfig.Env)
	}
	if ext.PluginRef != "" {
		if extensionConfig, ok := config.Processes[ext.PluginRef]; ok {
			env = applyEnvOverrides(env, extensionConfig.Env)
		}
	}
	return env
}

func sanitizedExtensionEnv() []string {
	allowedNames := map[string]struct{}{
		"HOME":              {},
		"PATH":              {},
		"SHELL":             {},
		"TMPDIR":            {},
		"TEMP":              {},
		"TMP":               {},
		"USER":              {},
		"USERNAME":          {},
		"LOGNAME":           {},
		"LANG":              {},
		"LC_ALL":            {},
		"NODE_OPTIONS":      {},
		"KODELET_BASE_PATH": {},
	}

	env := []string{}
	for _, entry := range os.Environ() {
		name, _, ok := strings.Cut(entry, "=")
		if !ok {
			continue
		}
		if _, allowed := allowedNames[name]; allowed {
			env = append(env, entry)
		}
	}
	return env
}

func applyEnvOverrides(env []string, overrides map[string]*string) []string {
	if len(overrides) == 0 {
		return env
	}
	values := map[string]string{}
	for _, entry := range env {
		name, value, ok := strings.Cut(entry, "=")
		if ok {
			values[name] = value
		}
	}
	for name, value := range overrides {
		if strings.TrimSpace(name) == "" {
			continue
		}
		if value == nil {
			if inherited, ok := os.LookupEnv(name); ok {
				values[name] = inherited
			}
			continue
		}
		values[name] = *value
	}
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	merged := make([]string, 0, len(keys))
	for _, key := range keys {
		merged = append(merged, key+"="+values[key])
	}
	return merged
}
