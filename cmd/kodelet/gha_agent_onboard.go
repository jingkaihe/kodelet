package main

import (
	"bufio"
	"context"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"

	"github.com/jingkaihe/kodelet/pkg/github"
	"github.com/jingkaihe/kodelet/pkg/presenter"
	"github.com/pkg/errors"
	"github.com/spf13/cobra"
)

// GhaAgentOnboardConfig holds configuration for the gha-agent-onboard command
type GhaAgentOnboardConfig struct {
	GithubApp           string
	AuthGatewayEndpoint string
}

// NewGhaAgentOnboardConfig creates a new GhaAgentOnboardConfig with default values
func NewGhaAgentOnboardConfig() *GhaAgentOnboardConfig {
	return &GhaAgentOnboardConfig{
		GithubApp:           "kodelet",
		AuthGatewayEndpoint: "https://gha-auth-gateway.kodelet.com/api/github",
	}
}

// generateWorkflowTemplate generates the GitHub workflow template with the provided configuration
func generateWorkflowTemplate(config *GhaAgentOnboardConfig) (string, error) {
	templateData := github.WorkflowTemplateData{
		AuthGatewayEndpoint: config.AuthGatewayEndpoint,
	}

	return github.RenderBackgroundAgentWorkflow(templateData)
}

var ghaAgentOnboardCmd = &cobra.Command{
	Use:   "gha-agent-onboard",
	Short: "Onboard the GitHub Actions-based background Kodelet agent",
	Long: `Onboard the GitHub Actions-based background Kodelet agent by installing the GitHub app,
setting up the required secrets, and creating the workflow file.

This command will:
1. Open the GitHub app installation page in your browser
2. Check and set up the ANTHROPIC_API_KEY secret
3. Create a git branch and install the Kodelet workflow file
4. Create a pull request for the changes`,
	Run: func(cmd *cobra.Command, args []string) {
		ctx := cmd.Context()

		// Get config from flags
		config := getGhaAgentOnboardConfigFromFlags(cmd)

		// Check prerequisites
		if !isGitRepository() {
			presenter.Error(fmt.Errorf("not a git repository"), "Please run this command from a git repository")
			os.Exit(1)
		}

		if !isGhCliInstalled() {
			presenter.Error(fmt.Errorf("GitHub CLI not installed"), "GitHub CLI (gh) is not installed. Please install it first")
			presenter.Info("Visit https://cli.github.com/ for installation instructions")
			os.Exit(1)
		}

		if !isGhAuthenticated() {
			presenter.Error(fmt.Errorf("not authenticated with GitHub"), "You are not authenticated with GitHub. Please run 'gh auth login' first")
			os.Exit(1)
		}

		// Step 1: Open GitHub app installation page
		presenter.Section("Step 1: GitHub App Installation")
		presenter.Info(fmt.Sprintf("Opening GitHub app installation page for '%s'...", config.GithubApp))
		appURL := fmt.Sprintf("https://github.com/apps/%s", config.GithubApp)

		// Validate the URL before opening
		if err := validateURL(appURL); err != nil {
			presenter.Error(err, "Invalid GitHub app URL")
			os.Exit(1)
		}

		if err := openInBrowser(appURL); err != nil {
			presenter.Warning(fmt.Sprintf("Failed to open browser automatically. Please manually open: %s", appURL))
		} else {
			presenter.Success(fmt.Sprintf("Opened: %s", appURL))
		}

		// Wait for user confirmation
		presenter.Info("Press Enter to continue once the app is installed...")
		reader := bufio.NewReader(os.Stdin)
		reader.ReadString('\n')

		// Step 2: Check ANTHROPIC_API_KEY secret
		presenter.Section("Step 2: API Key Setup")
		presenter.Info("Checking ANTHROPIC_API_KEY secret...")
		if err := setupAnthropicAPIKey(ctx); err != nil {
			presenter.Error(err, "Failed to set up ANTHROPIC_API_KEY")
			os.Exit(1)
		}

		// Step 3: Store current branch and create new branch and workflow file
		presenter.Section("Step 3: Branch and Workflow Setup")
		presenter.Info("Creating git branch and workflow file...")

		// Get current branch before creating new one
		currentBranch, err := getCurrentBranch()
		if err != nil {
			presenter.Error(err, "Failed to get current branch")
			os.Exit(1)
		}

		branchName := fmt.Sprintf("kodelet-background-agent-onboard-%d", time.Now().Unix())

		if err := createBranchAndWorkflow(ctx, branchName, config); err != nil {
			presenter.Error(err, "Failed to create branch and workflow")
			os.Exit(1)
		}

		// Step 4: Update the workflow file
		presenter.Section("Step 4: Workflow Customization")
		presenter.Info("Updating workflow configuration...")

		binaryPath, err := os.Executable()
		if err != nil {
			presenter.Error(err, "Failed to get executable path")
			os.Exit(1)
		}

		prompt := `
	Update the 'Set up Agent Environment' step in .github/workflows/kodelet.yaml based on your understanding of the codebase.

	Here are some of the references you can use to update the step:
	* Your context of the codebase
	* README.md
	* Pre-existing github actions workflow files
	`

		kodeletRunCmd := exec.Command(binaryPath, "run", "--no-save", prompt)
		if err := executeCommandWithStreaming(ctx, kodeletRunCmd); err != nil {
			presenter.Error(err, "Failed to customize workflow")
			os.Exit(1)
		}

		// Step 5: Commit and create PR
		presenter.Section("Step 5: Commit and Pull Request")
		presenter.Info("Creating commit and pull request...")

		prURL, err := commitAndCreatePR(ctx, branchName)
		if err != nil {
			presenter.Error(err, "Failed to create commit and PR")
			os.Exit(1)
		}

		// Step 6: Checkout back to original branch
		presenter.Info(fmt.Sprintf("Checking out back to original branch: %s", currentBranch))
		if err := checkoutBranch(currentBranch); err != nil {
			presenter.Warning(fmt.Sprintf("Failed to checkout back to original branch %s: %s", currentBranch, err))
		}

		// Success message
		presenter.Separator()
		presenter.Success("GitHub Actions background agent onboarding completed successfully!")
		presenter.Info(fmt.Sprintf("📝 Pull Request: %s", prURL))
		presenter.Info("🚀 Once the PR is merged, the GitHub Actions-based background agent will be up and running.")
	},
}

func init() {
	defaults := NewGhaAgentOnboardConfig()
	ghaAgentOnboardCmd.Flags().String("github-app", defaults.GithubApp, "GitHub app name")
	ghaAgentOnboardCmd.Flags().String("auth-gateway-endpoint", defaults.AuthGatewayEndpoint, "Auth gateway endpoint")
}

// getGhaAgentOnboardConfigFromFlags extracts configuration from command flags
func getGhaAgentOnboardConfigFromFlags(cmd *cobra.Command) *GhaAgentOnboardConfig {
	config := NewGhaAgentOnboardConfig()

	if githubApp, err := cmd.Flags().GetString("github-app"); err == nil {
		config.GithubApp = githubApp
	}
	if authGatewayEndpoint, err := cmd.Flags().GetString("auth-gateway-endpoint"); err == nil {
		config.AuthGatewayEndpoint = authGatewayEndpoint
	}

	return config
}

// openInBrowser opens the given URL in the default browser
func openInBrowser(url string) error {
	var cmd *exec.Cmd

	switch {
	case commandExists("xdg-open"): // Linux
		cmd = exec.Command("xdg-open", url)
	case commandExists("open"): // macOS
		cmd = exec.Command("open", url)
	case commandExists("cmd"): // Windows
		cmd = exec.Command("cmd", "/c", "start", url)
	default:
		return errors.New("unable to detect command to open browser")
	}

	return cmd.Start()
}

// commandExists checks if a command exists in the system
func commandExists(cmd string) bool {
	_, err := exec.LookPath(cmd)
	return err == nil
}

// getCurrentBranch gets the current git branch name
func getCurrentBranch() (string, error) {
	cmd := exec.Command("git", "rev-parse", "--abbrev-ref", "HEAD")
	output, err := cmd.Output()
	if err != nil {
		return "", errors.Wrap(err, "error getting current branch")
	}
	return strings.TrimSpace(string(output)), nil
}

// checkoutBranch checks out to the specified branch
func checkoutBranch(branchName string) error {
	cmd := exec.Command("git", "checkout", branchName)
	if err := cmd.Run(); err != nil {
		return errors.Wrap(err, fmt.Sprintf("error checking out to branch %s", branchName))
	}
	return nil
}

// validateURL validates that the provided URL is well-formed
func validateURL(urlStr string) error {
	parsedURL, err := url.Parse(urlStr)
	if err != nil {
		return errors.Wrap(err, "malformed URL")
	}

	if parsedURL.Scheme == "" {
		return errors.New("URL scheme is required")
	}

	if parsedURL.Host == "" {
		return errors.New("URL host is required")
	}

	// Additional validation for GitHub app URLs
	if parsedURL.Scheme != "https" {
		return errors.New("GitHub app URLs must use HTTPS")
	}

	if parsedURL.Host != "github.com" {
		return errors.New("GitHub app URLs must be on github.com domain")
	}

	return nil
}

// setupAnthropicAPIKey checks and sets up the ANTHROPIC_API_KEY secret
func setupAnthropicAPIKey(ctx context.Context) error {
	// Check if secret already exists
	secretExists, err := checkGitHubSecret("ANTHROPIC_API_KEY")
	if err != nil {
		return errors.Wrap(err, "error checking secret")
	}

	if secretExists {
		presenter.Success("ANTHROPIC_API_KEY secret already exists")
		return nil
	}

	// Check if env var exists
	apiKey := os.Getenv("ANTHROPIC_API_KEY")

	if apiKey == "" {
		// Ask user for the key
		presenter.Info("ANTHROPIC_API_KEY not found. Please enter your Anthropic API key: ")
		reader := bufio.NewReader(os.Stdin)
		input, err := reader.ReadString('\n')
		if err != nil {
			return errors.Wrap(err, "error reading input")
		}
		apiKey = strings.TrimSpace(input)
	} else {
		// Ask if user wants to use the env var
		presenter.Info("Found ANTHROPIC_API_KEY environment variable. Use it for the GitHub secret? [Y/n]: ")
		reader := bufio.NewReader(os.Stdin)
		response, _ := reader.ReadString('\n')
		response = strings.ToLower(strings.TrimSpace(response))

		if response == "n" || response == "no" {
			presenter.Info("Please enter your Anthropic API key: ")
			input, err := reader.ReadString('\n')
			if err != nil {
				return errors.Wrap(err, "error reading input")
			}
			apiKey = strings.TrimSpace(input)
		}
	}

	// Set the secret
	cmd := exec.Command("gh", "secret", "set", "ANTHROPIC_API_KEY", "--body", apiKey)
	if err := cmd.Run(); err != nil {
		return errors.Wrap(err, "error setting GitHub secret")
	}

	presenter.Success("ANTHROPIC_API_KEY secret set successfully")
	return nil
}

// checkGitHubSecret checks if a GitHub secret exists
func checkGitHubSecret(secretName string) (bool, error) {
	cmd := exec.Command("gh", "secret", "list")
	output, err := cmd.Output()
	if err != nil {
		return false, err
	}

	return strings.Contains(string(output), secretName), nil
}

// createBranchAndWorkflow creates a new git branch and adds the workflow file
func createBranchAndWorkflow(ctx context.Context, branchName string, config *GhaAgentOnboardConfig) error {
	// Create and checkout new branch
	cmd := exec.Command("git", "checkout", "-b", branchName)
	if err := cmd.Run(); err != nil {
		return errors.Wrap(err, "error creating branch")
	}

	// Create .github/workflows directory if it doesn't exist
	workflowDir := ".github/workflows"
	if err := os.MkdirAll(workflowDir, 0755); err != nil {
		return errors.Wrap(err, "error creating workflow directory")
	}

	// Generate the workflow template with config
	workflowContent, err := generateWorkflowTemplate(config)
	if err != nil {
		return errors.Wrap(err, "error generating workflow template")
	}

	// Write the workflow file
	workflowPath := fmt.Sprintf("%s/kodelet.yaml", workflowDir)
	if err := os.WriteFile(workflowPath, []byte(workflowContent), 0644); err != nil {
		return errors.Wrap(err, "error writing workflow file")
	}

	presenter.Success(fmt.Sprintf("Created workflow file: %s", workflowPath))
	presenter.Success(fmt.Sprintf("Created branch: %s", branchName))
	return nil
}

// executeCommandWithStreaming executes a command and streams its output in real-time
func executeCommandWithStreaming(ctx context.Context, cmd *exec.Cmd) error {
	// Set up pipes for real-time output streaming
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return errors.Wrap(err, "error creating stdout pipe")
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return errors.Wrap(err, "error creating stderr pipe")
	}

	// Start the command
	if err := cmd.Start(); err != nil {
		return errors.Wrap(err, "error starting command")
	}

	// Stream stdout and stderr in separate goroutines
	var wg sync.WaitGroup
	wg.Add(2)

	go func() {
		defer wg.Done()
		scanner := bufio.NewScanner(stdout)
		for scanner.Scan() {
			fmt.Println(scanner.Text())
		}
	}()

	go func() {
		defer wg.Done()
		scanner := bufio.NewScanner(stderr)
		for scanner.Scan() {
			fmt.Fprintf(os.Stderr, "%s\n", scanner.Text())
		}
	}()

	// Wait for all output to be processed
	wg.Wait()

	// Wait for the command to finish
	if err := cmd.Wait(); err != nil {
		return errors.Wrap(err, "error executing command")
	}
	return nil
}

// commitAndCreatePR commits the changes and creates a pull request
func commitAndCreatePR(ctx context.Context, branchName string) (string, error) {
	// Add the workflow file
	workflowFile := ".github/workflows/kodelet.yaml"
	cmd := exec.Command("git", "add", workflowFile)
	if err := cmd.Run(); err != nil {
		return "", errors.Wrap(err, "error adding workflow file")
	}

	// Commit the changes
	commitMsg := "onboard kodelet background agent"
	cmd = exec.Command("git", "commit", "-m", commitMsg)
	if err := cmd.Run(); err != nil {
		return "", errors.Wrap(err, "error committing changes")
	}

	// Push the branch
	cmd = exec.Command("git", "push", "origin", branchName)
	if err := cmd.Run(); err != nil {
		return "", errors.Wrap(err, "error pushing branch")
	}

	// Create PR
	prTitle := "feat: onboard Kodelet background agent"
	prBody := `## Description
This PR onboards the Kodelet background agent for GitHub Actions.

## Changes
- Add GitHub Actions workflow for background Kodelet agent
- Configure automatic triggers for @kodelet mentions in issues, comments, and reviews
- Set up proper permissions and environment for the agent

## Impact
- Enables automated responses to @kodelet mentions
- Provides background assistance for issues and pull requests
- Improves development workflow with AI assistance`

	cmd = exec.Command("gh", "pr", "create", "--title", prTitle, "--body", prBody, "--base", "main")

	output, err := cmd.Output()
	if err != nil {
		return "", errors.Wrap(err, "error creating PR")
	}

	prURL := strings.TrimSpace(string(output))
	return prURL, nil
}
