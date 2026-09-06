package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/signal"
	"slices"
	"strings"
	"syscall"
	"time"

	"github.com/jingkaihe/kodelet/pkg/chat"
	"github.com/jingkaihe/kodelet/pkg/extensions"
	llmbase "github.com/jingkaihe/kodelet/pkg/llm/base"
	"github.com/jingkaihe/kodelet/pkg/runner/localstate"
	"github.com/jingkaihe/kodelet/pkg/tools/renderers"
	convtypes "github.com/jingkaihe/kodelet/pkg/types/conversations"
	llmtypes "github.com/jingkaihe/kodelet/pkg/types/llm"
	"github.com/pkg/errors"
	"github.com/spf13/cobra"
)

func addRemoteRunFlags(cmd *cobra.Command) {
	cmd.Flags().String("server", defaultRunnerServer, "Execute through a control-plane daemon (or KODELET_SERVER)")
	cmd.Flags().String("auth-token", "", "Control-plane client authentication token (or KODELET_AUTH_TOKEN)")
	cmd.Flags().String("runner", "", "Workspace runner ID, ID prefix, or display name")
	cmd.Flags().String("runner-profile", "", "Runner-owned environment profile")
}

func runControlPlaneCommand(cmd *cobra.Command, args []string) error {
	ctx, stop := signal.NotifyContext(cmd.Context(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	request, resultOnly, err := remoteRunRequest(cmd, args)
	if err != nil {
		return err
	}
	server, _ := serverFlagOrConfig(cmd)
	token, _, err := resolveControlPlaneAuthToken(cmd, server)
	if err != nil {
		return err
	}
	runner, err := prepareOneShotRunner(ctx, cmd, server, token, &request)
	if err != nil {
		return errors.Wrap(err, "cannot prepare daemon execution; start kodelet serve or check --server and client authentication (no local fallback)")
	}
	if !resultOnly {
		ctx = extensions.ContextWithUIInputBroker(ctx, extensions.NewTerminalUIInputBroker(os.Stdin, cmd.ErrOrStderr()))
	}
	return executeRemoteRun(ctx, runner, request, resultOnly, cmd.OutOrStdout(), cmd.ErrOrStderr())
}

func remoteRunRequest(cmd *cobra.Command, args []string) (chat.ChatRequest, bool, error) {
	var request chat.ChatRequest
	// Every unsupported explicit flag fails before attachment reads or submission.
	// These runner/provider installation settings must not be silently forwarded.
	for _, name := range []string{"fragment-dirs", "sysprompt", "sysprompt-arg", "allowed-domains-file", "anthropic-api-access", "account", "tool-mode", "context-patterns", "compact-ratio", "enable-openai-search"} {
		if cmd.Flags().Changed(name) {
			return request, false, errors.Errorf("--%s is not yet supported by daemon-backed run; configure it on the owning daemon or runner", name)
		}
	}
	if cmd.Flags().Changed("no-save") {
		return request, false, errors.New("--no-save is not supported by daemon-backed run; all conversations are saved")
	}
	request.ConversationID, _ = cmd.Flags().GetString("resume")
	request.ConversationID = strings.TrimSpace(request.ConversationID)
	follow, _ := cmd.Flags().GetBool("follow")
	if follow && request.ConversationID != "" {
		return request, false, errors.New("--follow and --resume cannot be used together")
	}
	request.CWD, _ = cmd.Flags().GetString("cwd")
	request.CWD = strings.TrimSpace(request.CWD)
	request.EnvironmentProfile, _ = cmd.Flags().GetString("runner-profile")
	if cmd.Flags().Changed("profile") {
		request.Profile, _ = cmd.Flags().GetString("profile")
	}
	options, err := remoteRunExecutionOptions(cmd)
	if err != nil {
		return request, false, err
	}
	request.Options = options
	query, err := getQueryFromStdinOrArgs(args)
	recipe, _ := cmd.Flags().GetString("recipe")
	images, _ := cmd.Flags().GetStringSlice("image")
	if err != nil && recipe == "" && len(images) == 0 {
		return request, false, err
	}
	if recipe != "" {
		arguments, _ := cmd.Flags().GetStringToString("arg")
		query = strings.TrimSpace("/" + strings.TrimPrefix(recipe, "/") + " " + formatFragmentDisplayArgs(arguments) + " " + query)
	} else if cmd.Flags().Changed("arg") {
		return request, false, errors.New("--arg requires --recipe")
	}
	request.Message = query
	for _, image := range images {
		block, err := remoteRunImage(image)
		if err != nil {
			return request, false, err
		}
		request.Content = append(request.Content, block)
	}
	if _, _, err := chat.NormalizeRequest(request); err != nil {
		return request, false, err
	}
	if strings.TrimSpace(query) == "" && len(request.Content) == 0 {
		return request, false, errors.New("no query provided")
	}
	resultOnly, _ := cmd.Flags().GetBool("result-only")
	return request, resultOnly, nil
}

func remoteRunImage(input string) (chat.ChatContentBlock, error) {
	input = strings.TrimSpace(input)
	if strings.HasPrefix(input, "https://") || strings.HasPrefix(input, "data:") {
		return chat.ChatContentBlock{Type: "image", ImageURL: &chat.ChatImageURLSource{URL: input}}, nil
	}
	if strings.Contains(input, "://") && !strings.HasPrefix(input, "file://") {
		return chat.ChatContentBlock{}, errors.New("image URLs must use HTTPS")
	}
	data, err := llmbase.ReadImageFileAsDataURL(strings.TrimPrefix(input, "file://"))
	if err != nil {
		return chat.ChatContentBlock{}, errors.Wrap(err, "failed to read client image attachment")
	}
	return chat.ChatContentBlock{Type: "image", ImageURL: &chat.ChatImageURLSource{URL: data}}, nil
}

func remoteRunExecutionOptions(cmd *cobra.Command, ignoredFlags ...string) (*llmtypes.ExecutionOptions, error) {
	options := &llmtypes.ExecutionOptions{}
	for flag, target := range map[string]**string{
		"provider": &options.Provider, "model": &options.Model,
		"weak-model": &options.WeakModel, "reasoning-effort": &options.ReasoningEffort,
	} {
		if slices.Contains(ignoredFlags, flag) {
			continue
		}
		if cmd.Flags().Changed(flag) {
			value, err := cmd.Flags().GetString(flag)
			if err != nil {
				return nil, err
			}
			*target = &value
		}
	}
	for flag, target := range map[string]**int{
		"max-tokens": &options.MaxTokens, "weak-model-max-tokens": &options.WeakModelMaxTokens,
		"thinking-budget-tokens": &options.ThinkingBudgetTokens, "max-turns": &options.MaxTurns,
	} {
		if cmd.Flags().Changed(flag) {
			value, err := cmd.Flags().GetInt(flag)
			if err != nil {
				return nil, err
			}
			*target = &value
		}
	}
	for flag, target := range map[string]**bool{
		"use-weak-model": &options.UseWeakModel, "no-tools": &options.NoTools,
		"no-extensions": &options.NoExtensions, "no-skills": &options.NoSkills,
		"enable-fs-search-tools": &options.EnableFSSearchTools,
	} {
		if cmd.Flags().Changed(flag) {
			value, err := cmd.Flags().GetBool(flag)
			if err != nil {
				return nil, err
			}
			*target = &value
		}
	}
	for flag, target := range map[string]**[]string{
		"allowed-tools": &options.AllowedTools, "allowed-commands": &options.AllowedCommands,
	} {
		if cmd.Flags().Changed(flag) {
			value, err := cmd.Flags().GetStringSlice(flag)
			if err != nil {
				return nil, err
			}
			// Encode an explicit empty list as [], never null (which is rejected).
			value = append([]string{}, value...)
			*target = &value
		}
	}
	return options, options.Validate()
}

func prepareOneShotRunner(ctx context.Context, cmd *cobra.Command, server, token string, request *chat.ChatRequest) (*chat.ControlPlaneChatRunner, error) {
	selector, _ := cmd.Flags().GetString("runner")
	var runner *chat.ControlPlaneChatRunner
	var defaultCWD string
	var err error
	if strings.TrimSpace(selector) != "" {
		runner, defaultCWD, err = prepareRemoteChatRunner(ctx, &ChatConfig{Server: server, AuthToken: token, Runner: selector})
	} else {
		runner, err = chat.NewControlPlaneChatRunner(server, token, "")
	}
	if err != nil {
		return nil, err
	}
	follow, _ := cmd.Flags().GetBool("follow")
	if follow {
		if strings.TrimSpace(selector) == "" && request.CWD == "" {
			return nil, errors.New("daemon-backed --follow requires --runner or --cwd to scope history")
		}
		cwd := request.CWD
		if cwd == "" {
			cwd = defaultCWD
		}
		if strings.TrimSpace(selector) == "" {
			settings, err := runner.ChatSettings(ctx, request.Profile)
			if err != nil {
				return nil, err
			}
			if settings.DefaultRunnerID == "" || !settings.DefaultRunnerReady {
				return nil, errors.New("daemon has no ready default runner; start serve --embedded-runner or select --runner explicitly")
			}
			request.RunnerID = settings.DefaultRunnerID
		}
		target, err := runner.DiscoverWorkspace(ctx, chat.WorkspaceTarget{
			RunnerID: request.RunnerID, CWD: cwd, Profile: request.Profile,
			EnvironmentProfile: request.EnvironmentProfile, Options: request.Options.Restrictions(),
		})
		if err != nil {
			return nil, err
		}
		if target.CWD == "" {
			return nil, errors.New("runner discovery returned no validated directory")
		}
		request.CWD = target.CWD
		history, err := runner.ListConversationsInCWD(ctx, 1, target.CWD)
		if err != nil {
			return nil, err
		}
		if len(history) == 0 {
			return nil, errors.New("no conversation found in the selected runner/workspace; omit --follow to start one")
		}
		request.ConversationID = history[0].ID
	}
	if request.ConversationID != "" {
		history, err := runner.LoadConversation(ctx, request.ConversationID)
		if err != nil {
			return nil, err
		}
		if history.RunnerID == "" {
			return nil, errors.New("legacy conversation has no runner affinity; adopt it before continuing, or start a new conversation")
		}
		// Omitted CWD stays omitted: the server validates the stored affinity.
		request.RunnerID = history.RunnerID
	} else {
		if strings.TrimSpace(selector) == "" {
			settings, err := runner.ChatSettings(ctx, request.Profile)
			if err != nil {
				return nil, err
			}
			if settings.DefaultRunnerID == "" || !settings.DefaultRunnerReady {
				return nil, errors.New("daemon has no ready default runner; start serve --embedded-runner or select --runner explicitly")
			}
			request.RunnerID = settings.DefaultRunnerID
			if request.CWD == "" {
				request.CWD, err = sameHostDefaultCWD(settings)
				if err != nil {
					return nil, err
				}
			}
		}
		request.ConversationID = convtypes.GenerateID()
	}
	request.TurnID = convtypes.GenerateID()
	return runner, nil
}

func sameHostDefaultCWD(settings chat.ControlPlaneChatSettings) (string, error) {
	if settings.DefaultRunnerID == "" || settings.DefaultRunnerHostID == "" {
		return "", nil
	}
	identity, err := localstate.LoadDefaultHostIdentity()
	if err != nil || identity.InstanceID != settings.DefaultRunnerHostID {
		// Missing/unreadable state is normal for a remote-only client. An HTTP
		// loopback address, including a forwarded port, proves nothing about CWD.
		return "", nil
	}
	cwd, err := os.Getwd()
	return cwd, errors.Wrap(err, "failed to resolve invoking directory for the same-host daemon")
}

type remoteOneShotClient interface {
	Run(context.Context, chat.ChatRequest, chat.ChatEventSink) (string, error)
	StopConversationTurn(context.Context, string, string) error
}

func executeRemoteRun(ctx context.Context, runner remoteOneShotClient, request chat.ChatRequest, resultOnly bool, output, diagnostics io.Writer) error {
	sink := &remoteRunSink{output: output, diagnostics: diagnostics, resultOnly: resultOnly}
	return executeRemoteRunWithSink(ctx, runner, request, sink)
}

func executeRemoteRunWithSink(ctx context.Context, runner remoteOneShotClient, request chat.ChatRequest, sink *remoteRunSink) error {
	_, err := runner.Run(ctx, request, sink)
	if ctx.Err() != nil {
		// Detaching the stream does not cancel daemon-owned work. Use a fresh,
		// bounded request and the exact turn identity, including during admission.
		stopCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
		defer cancel()
		if stopErr := runner.StopConversationTurn(stopCtx, request.ConversationID, request.TurnID); stopErr != nil {
			return errors.Wrapf(ctx.Err(), "cancellation could not be acknowledged; conversation %s may still be running: %v", request.ConversationID, stopErr)
		}
		return errors.Wrap(ctx.Err(), "daemon acknowledged cancellation")
	}
	if err != nil {
		return errors.Wrapf(err, "daemon execution failed or detached (conversation %s, turn %s); inspect history before resubmitting, since submission is never automatically retried", request.ConversationID, request.TurnID)
	}
	if sink.cancelled {
		return errors.Wrap(context.Canceled, "daemon execution was canceled")
	}
	if sink.resultOnly {
		if sink.result == nil {
			return errors.New("daemon did not provide a final result; upgrade the daemon before using --result-only")
		}
		_, err = fmt.Fprintln(sink.output, *sink.result)
		return err
	}
	if sink.usage != nil {
		if _, err := fmt.Fprintf(sink.diagnostics, "Usage: %d input tokens, %d output tokens\n", sink.usage.InputTokens, sink.usage.OutputTokens); err != nil {
			return err
		}
	}
	_, err = fmt.Fprintf(sink.diagnostics, "Conversation: %s\n", request.ConversationID)
	return err
}

type remoteRunSink struct {
	output      io.Writer
	diagnostics io.Writer
	resultOnly  bool
	result      *string
	usage       *llmtypes.Usage
	cancelled   bool
	streamed    bool
}

func (s *remoteRunSink) Send(event chat.ChatEvent) error {
	switch event.Kind {
	case "result":
		s.result = event.Result
	case "usage":
		s.usage = event.Usage
	case "done":
		s.cancelled = event.Cancelled
	}
	if s.resultOnly {
		return nil
	}
	var err error
	switch event.Kind {
	case "text-delta":
		s.streamed = true
		_, err = fmt.Fprint(s.output, event.Delta)
	case "text":
		if !s.streamed {
			if text, ok := event.Content.(string); ok {
				_, err = fmt.Fprintln(s.output, text)
			}
		}
	case "content-end":
		if s.streamed {
			_, err = fmt.Fprintln(s.output)
		}
		s.streamed = false
	case "tool-use":
		_, err = fmt.Fprintf(s.diagnostics, "Using tool: %s\n%s\n", event.ToolName, event.Input)
	case "tool-result":
		result := event.ToolOutput
		if event.ToolResult != nil {
			result = renderers.NewRendererRegistry().Render(*event.ToolResult)
		}
		_, err = fmt.Fprintln(s.diagnostics, result)
	}
	return err
}
