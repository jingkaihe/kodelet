package agent

import (
	"context"
	"errors"
	"os"
	"strings"
	"sync"
)

// Client creates daemon-backed agent sessions. Each session runs its own
// kodelet acp process.
type Client struct {
	opts  ClientOptions
	start starter

	mu       sync.Mutex
	sessions map[*Session]struct{}
}

// NewClient returns a client.
func NewClient(opts ClientOptions) *Client {
	if opts.Command == "" {
		opts.Command = "kodelet"
	}
	return &Client{opts: opts, start: startProcess, sessions: map[*Session]struct{}{}}
}

// CreateSession starts kodelet acp and creates or resumes a conversation. The
// context bounds startup only; the session lives until Close.
func (c *Client) CreateSession(ctx context.Context, opts SessionOptions) (*Session, error) {
	if err := opts.validate(); err != nil {
		return nil, err
	}
	cwd := opts.CWD
	if cwd == "" {
		cwd = c.opts.CWD
	}
	if cwd == "" {
		var err error
		if cwd, err = os.Getwd(); err != nil {
			return nil, err
		}
	}
	args := []string{"acp"}
	if c.opts.Server != "" {
		args = append(args, "--server", c.opts.Server)
	}
	if c.opts.Runner != "" {
		args = append(args, "--runner", c.opts.Runner)
	}
	args = append(args, opts.Options.args()...)
	if opts.Profile != "" {
		args = append(args, "--profile="+opts.Profile)
	}
	if opts.EnvironmentProfile != "" {
		args = append(args, "--runner-profile="+opts.EnvironmentProfile)
	}
	env := os.Environ()
	for key, value := range c.opts.Env {
		env = append(env, key+"="+value)
	}
	proc, err := c.start(c.opts.Command, args, env)
	if err != nil {
		return nil, err
	}
	conn := newACPConn(proc, opts.Extensions, opts.UI)
	sessionID, err := c.openSession(ctx, conn, opts, cwd)
	if err != nil {
		if closeErr := conn.close(); closeErr != nil {
			return nil, errors.Join(err, closeErr)
		}
		return nil, err
	}
	session := newSession(c, conn, sessionID, cwd)
	c.mu.Lock()
	c.sessions[session] = struct{}{}
	c.mu.Unlock()
	return session, nil
}

func (c *Client) openSession(ctx context.Context, conn *acpConn, opts SessionOptions, cwd string) (string, error) {
	if err := conn.initialize(ctx); err != nil {
		return "", err
	}
	if opts.Resume != "" {
		return opts.Resume, conn.loadSession(ctx, opts.Resume, cwd)
	}
	return conn.createSession(ctx, cwd, strings.TrimSpace(opts.ParentConversationID))
}

// Close closes every session created by the client.
func (c *Client) Close() error {
	c.mu.Lock()
	sessions := make([]*Session, 0, len(c.sessions))
	for session := range c.sessions {
		sessions = append(sessions, session)
	}
	c.mu.Unlock()
	errs := make([]error, len(sessions))
	var wg sync.WaitGroup
	for i, session := range sessions {
		wg.Add(1)
		go func() {
			defer wg.Done()
			errs[i] = session.Close()
		}()
	}
	wg.Wait()
	return errors.Join(errs...)
}

func (c *Client) forget(session *Session) {
	c.mu.Lock()
	delete(c.sessions, session)
	c.mu.Unlock()
}
