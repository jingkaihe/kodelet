// Package agent runs Kodelet agent sessions from Go.
//
// A [Client] starts "kodelet acp" as a thin client of the Kodelet daemon. The
// daemon owns the model loop, credentials, and conversation history; the
// selected runner owns workspace execution and tool policy. Session working
// directories refer to the runner's machine.
//
//	client := agent.NewClient(agent.ClientOptions{})
//	defer client.Close()
//	session, err := client.CreateSession(ctx, agent.SessionOptions{})
//	if err != nil { ... }
//	response, err := session.RunAndWait(ctx, agent.RunOptions{Message: "Summarize README.md"})
//
// Sessions can attach inline extensions ([extension.Extension] values) whose
// callbacks run in this process while the runner exposes their tools to the
// model, subject to the daemon and runner's tool and extension policy:
//
//	session, err := client.CreateSession(ctx, agent.SessionOptions{
//		Extensions: []*extension.Extension{ext},
//		UI:         &agent.UIHandlers{Confirm: confirm},
//	})
//
// Callbacks are never replayed after a disconnect. To resume a conversation
// with callbacks, pass the same extensions again, in the same order, with
// SessionOptions.Resume.
package agent
