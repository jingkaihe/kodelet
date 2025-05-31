package github

import (
	"context"

	"github.com/google/go-github/v57/github"
	"github.com/jingkaihe/kodelet/pkg/logger"
	"golang.org/x/oauth2"
)

// Client wraps the GitHub API client with additional functionality
type Client struct {
	client *github.Client
}

// NewClient creates a new GitHub client with authentication
func NewClient(ctx context.Context, token string) *Client {
	log := logger.G(ctx)
	
	if token == "" {
		log.Warn("No GitHub token provided - API rate limits will be restricted")
		return &Client{
			client: github.NewClient(nil),
		}
	}
	
	ts := oauth2.StaticTokenSource(&oauth2.Token{AccessToken: token})
	tc := oauth2.NewClient(ctx, ts)
	
	log.Debug("GitHub client initialized with authentication")
	return &Client{
		client: github.NewClient(tc),
	}
}

// GetClient returns the underlying GitHub client
func (c *Client) GetClient() *github.Client {
	return c.client
}