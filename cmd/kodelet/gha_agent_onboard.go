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
	"github.com/jingkaihe/kodelet/pkg/logger"
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
		logger.G(ctx).WithFields(map[string]interface{}{
			"github_app":   config.GithubApp,
			"auth_gateway": config.AuthGatewayEndpoint,
		}).Info("Starting GitHub Actions agent onboarding")

		// Check prerequisites
		if !isGitRepository() {
			presenter.Error(fmt.Errorf("not a git repository"), "Please run this command from a git repository")
			logger.G(ctx).Error("Command executed outside git repository")
			os.Exit(1)
		}

		if !isGhCliInstalled() {
			presenter.Error(fmt.Errorf("GitHub CLI not installed"), "GitHub CLI (gh) is not installed. Please install it first")
			presenter.Info("Visit https://cli.github.com/ for installation instructions")
			logger.G(ctx).Error("GitHub CLI not found in PATH")
			os.Exit(1)
		}

		if !isGhAuthenticated() {
			presenter.Error(fmt.Errorf("not authenticated with GitHub"), "You are not authenticated with GitHub. Please run 'gh auth login' first")
			logger.G(ctx).Error("GitHub CLI authentication check failed")
			os.Exit(1)
		}

		// Step 1: Open GitHub app installation page
		presenter.Section("Step 1: GitHub App Installation")
		presenter.Info(fmt.Sprintf("Opening GitHub app installation page for '%s'...", config.GithubApp))
		appURL := fmt.Sprintf("https://github.com/apps/%s", config.GithubApp)
		logger.G(ctx).WithField("app_url", appURL).Info("Opening GitHub app installation page")

		// Validate the URL before opening
		if err := validateURL(appURL); err != nil {
			presenter.Error(err, "Invalid GitHub app URL")
			logger.G(ctx).WithError(err).WithField("url", appURL).Error("GitHub app URL validation failed")
			os.Exit(1)
		}

		if err := openInBrowser(appURL); err != nil {
			presenter.Warning(fmt.Sprintf("Failed to open browser automatically. Please manually open: %s", appURL))
			logger.G(ctx).WithError(err).Warn("Failed to open browser automatically")
		} else {
			presenter.Success(fmt.Sprintf("Opened: %s", appURL))
			logger.G(ctx).Info("Successfully opened GitHub app page in browser")
		}

		// Wait for user confirmation
		presenter.Info("Press Enter to continue once the app is installed...")
		reader := bufio.NewReader(os.Stdin)
		reader.ReadString('\n')

		// Step 2: Check ANTHROPIC_API_KEY secret
		presenter.Section("Step 2: API Key Setup")
		presenter.Info("Checking ANTHROPIC_API_KEY secret...")
		logger.G(ctx).Info("Starting ANTHROPIC_API_KEY setup")
		if err := setupAnthropicAPIKey(ctx); err != nil {
			presenter.Error(err, "Failed to set up ANTHROPIC_API_KEY")
			logger.G(ctx).WithError(err).Error("ANTHROPIC_API_KEY setup failed")
			os.Exit(1)
		}

		// Step 3: Store current branch and create new branch and workflow file
		presenter.Section("Step 3: Branch and Workflow Setup")
		presenter.Info("Creating git branch and workflow file...")
		logger.G(ctx).Info("Starting branch and workflow creation")

		// Get current branch before creating new one
		currentBranch, err := getCurrentBranch()
		if err != nil {
			presenter.Error(err, "Failed to get current branch")
			logger.G(ctx).WithError(err).Error("Could not determine current git branch")
			os.Exit(1)
		}
		logger.G(ctx).WithField("current_branch", currentBranch).Info("Current branch identified")

		branchName := fmt.Sprintf("kodelet-background-agent-onboard-%d", time.Now().Unix())
		logger.G(ctx).WithField("new_branch", branchName).Info("Generated new branch name")

		if err := createBranchAndWorkflow(ctx, branchName, config); err != nil {
			presenter.Error(err, "Failed to create branch and workflow")
			logger.G(ctx).WithError(err).WithField("branch", branchName).Error("Branch and workflow creation failed")
			os.Exit(1)
		}

		// Step 4: Update the workflow file
		presenter.Section("Step 4: Workflow Customization")
		presenter.Info("Updating workflow configuration...")

		binaryPath, err := os.Executable()
		if err != nil {
			presenter.Error(err, "Failed to get executable path")
			logger.G(ctx).WithError(err).Error("Could not determine kodelet binary path")
			os.Exit(1)
		}
		logger.G(ctx).WithField("binary_path", binaryPath).Info("Identified kodelet executable path")

		prompt := `
	Update the 'Set up Agent Environment' step in .github/workflows/kodelet.yaml based on your understanding of the codebase.

	Here are some of the references you can use to update the step:
	* Your context of the codebase
	* README.md
	* Pre-existing github actions workflow files
	`

		kodeletRunCmd := exec.Command(binaryPath, "run", "--no-save", prompt)
		logger.G(ctx).Info("Executing kodelet command to customize workflow")
		if err := executeCommandWithStreaming(ctx, kodeletRunCmd); err != nil {
			presenter.Error(err, "Failed to customize workflow")
			logger.G(ctx).WithError(err).Error("Workflow customization command failed")
			os.Exit(1)
		}

		// Step 5: Commit and create PR
		presenter.Section("Step 5: Commit and Pull Request")
		presenter.Info("Creating commit and pull request...")
		logger.G(ctx).WithField("branch", branchName).Info("Starting commit and PR creation")

		prURL, err := commitAndCreatePR(ctx, branchName)
		if err != nil {
			presenter.Error(err, "Failed to create commit and PR")
			logger.G(ctx).WithError(err).WithField("branch", branchName).Error("Commit and PR creation failed")
			os.Exit(1)
		}
		logger.G(ctx).WithField("pr_url", prURL).Info("Pull request created successfully")

		// Step 6: Checkout back to original branch
		presenter.Info(fmt.Sprintf("Checking out back to original branch: %s", currentBranch))
		if err := checkoutBranch(currentBranch); err != nil {
			presenter.Warning(fmt.Sprintf("Failed to checkout back to original branch %s: %s", currentBranch, err))
			logger.G(ctx).WithError(err).WithField("branch", currentBranch).Warn("Failed to checkout back to original branch")
		} else {
			logger.G(ctx).WithField("branch", currentBranch).Info("Successfully checked out back to original branch")
		}

		// Success message
		presenter.Separator()
		presenter.Success("GitHub Actions background agent onboarding completed successfully!")
		presenter.Info(fmt.Sprintf("📝 Pull Request: %s", prURL))
		presenter.Info("🚀 Once the PR is merged, the GitHub Actions-based background agent will be up and running.")
		logger.G(ctx).WithField("pr_url", prURL).Info("GitHub Actions agent onboarding completed successfully")
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
		logger.G(ctx).WithError(err).Error("Failed to check existing GitHub secret")
		return errors.Wrap(err, "error checking secret")
	}
	logger.G(ctx).WithField("secret_exists", secretExists).Info("Checked ANTHROPIC_API_KEY secret existence")

	if secretExists {
		presenter.Success("ANTHROPIC_API_KEY secret already exists")
		logger.G(ctx).Info("ANTHROPIC_API_KEY secret already configured")
		return nil
	}

	// Check if env var exists
	apiKey := os.Getenv("ANTHROPIC_API_KEY")
	hasEnvVar := apiKey != ""
	logger.G(ctx).WithField("has_env_var", hasEnvVar).Info("Checked environment variable")

	if apiKey == "" {
		// Ask user for the key
		presenter.Info("ANTHROPIC_API_KEY not found. Please enter your Anthropic API key: ")
		reader := bufio.NewReader(os.Stdin)
		input, err := reader.ReadString('\n')
		if err != nil {
			logger.G(ctx).WithError(err).Error("Failed to read user input")
			return errors.Wrap(err, "error reading input")
		}
		apiKey = strings.TrimSpace(input)
		logger.G(ctx).Info("API key provided by user input")
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
				logger.G(ctx).WithError(err).Error("Failed to read user input")
				return errors.Wrap(err, "error reading input")
			}
			apiKey = strings.TrimSpace(input)
			logger.G(ctx).Info("User chose to provide different API key")
		} else {
			logger.G(ctx).Info("User chose to use environment variable")
		}
	}

	// Set the secret
	cmd := exec.Command("gh", "secret", "set", "ANTHROPIC_API_KEY", "--body", apiKey)
	logger.G(ctx).Info("Setting GitHub secret")
	if err := cmd.Run(); err != nil {
		logger.G(ctx).WithError(err).Error("Failed to set GitHub secret")
		return errors.Wrap(err, "error setting GitHub secret")
	}

	presenter.Success("ANTHROPIC_API_KEY secret set successfully")
	logger.G(ctx).Info("GitHub secret configured successfully")
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
	logger.G(ctx).WithField("branch", branchName).Info("Creating new git branch")
	if err := cmd.Run(); err != nil {
		logger.G(ctx).WithError(err).WithField("branch", branchName).Error("Failed to create git branch")
		return errors.Wrap(err, "error creating branch")
	}

	// Create .github/workflows directory if it doesn't exist
	workflowDir := ".github/workflows"
	logger.G(ctx).WithField("directory", workflowDir).Info("Creating workflow directory")
	if err := os.MkdirAll(workflowDir, 0755); err != nil {
		logger.G(ctx).WithError(err).WithField("directory", workflowDir).Error("Failed to create workflow directory")
		return errors.Wrap(err, "error creating workflow directory")
	}

	// Generate the workflow template with config
	logger.G(ctx).Info("Generating workflow template")
	workflowContent, err := generateWorkflowTemplate(config)
	if err != nil {
		logger.G(ctx).WithError(err).Error("Failed to generate workflow template")
		return errors.Wrap(err, "error generating workflow template")
	}

	// Write the workflow file
	workflowPath := fmt.Sprintf("%s/kodelet.yaml", workflowDir)
	logger.G(ctx).WithField("workflow_path", workflowPath).Info("Writing workflow file")
	if err := os.WriteFile(workflowPath, []byte(workflowContent), 0644); err != nil {
		logger.G(ctx).WithError(err).WithField("workflow_path", workflowPath).Error("Failed to write workflow file")
		return errors.Wrap(err, "error writing workflow file")
	}

	presenter.Success(fmt.Sprintf("Created workflow file: %s", workflowPath))
	presenter.Success(fmt.Sprintf("Created branch: %s", branchName))
	logger.G(ctx).WithFields(map[string]interface{}{
		"workflow_path": workflowPath,
		"branch":        branchName,
	}).Info("Branch and workflow created successfully")
	return nil
}

// executeCommandWithStreaming executes a command and streams its output in real-time
func executeCommandWithStreaming(ctx context.Context, cmd *exec.Cmd) error {
	logger.G(ctx).WithField("command", strings.Join(cmd.Args, " ")).Info("Executing command with streaming")

	// Set up pipes for real-time output streaming
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		logger.G(ctx).WithError(err).Error("Failed to create stdout pipe")
		return errors.Wrap(err, "error creating stdout pipe")
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		logger.G(ctx).WithError(err).Error("Failed to create stderr pipe")
		return errors.Wrap(err, "error creating stderr pipe")
	}

	// Start the command
	if err := cmd.Start(); err != nil {
		logger.G(ctx).WithError(err).Error("Failed to start command")
		return errors.Wrap(err, "error starting command")
	}
	logger.G(ctx).WithField("pid", cmd.Process.Pid).Info("Command started")

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
		logger.G(ctx).WithError(err).WithField("exit_code", cmd.ProcessState.ExitCode()).Error("Command execution failed")
		return errors.Wrap(err, "error executing command")
	}

	logger.G(ctx).Info("Command executed successfully")
	return nil
}

// commitAndCreatePR commits the changes and creates a pull request
func commitAndCreatePR(ctx context.Context, branchName string) (string, error) {
	// Add the workflow file
	workflowFile := ".github/workflows/kodelet.yaml"
	cmd := exec.Command("git", "add", workflowFile)
	logger.G(ctx).WithField("file", workflowFile).Info("Adding workflow file to git")
	if err := cmd.Run(); err != nil {
		logger.G(ctx).WithError(err).WithField("file", workflowFile).Error("Failed to add workflow file")
		return "", errors.Wrap(err, "error adding workflow file")
	}

	// Commit the changes
	commitMsg := "onboard kodelet background agent"
	cmd = exec.Command("git", "commit", "-m", commitMsg)
	logger.G(ctx).WithField("commit_message", commitMsg).Info("Creating git commit")
	if err := cmd.Run(); err != nil {
		logger.G(ctx).WithError(err).Error("Failed to create commit")
		return "", errors.Wrap(err, "error committing changes")
	}

	// Push the branch
	cmd = exec.Command("git", "push", "origin", branchName)
	logger.G(ctx).WithField("branch", branchName).Info("Pushing branch to origin")
	if err := cmd.Run(); err != nil {
		logger.G(ctx).WithError(err).WithField("branch", branchName).Error("Failed to push branch")
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
	logger.G(ctx).WithFields(map[string]interface{}{
		"title":  prTitle,
		"base":   "main",
		"branch": branchName,
	}).Info("Creating pull request")

	output, err := cmd.Output()
	if err != nil {
		logger.G(ctx).WithError(err).Error("Failed to create pull request")
		return "", errors.Wrap(err, "error creating PR")
	}

	prURL := strings.TrimSpace(string(output))
	logger.G(ctx).WithField("pr_url", prURL).Info("Pull request created successfully")
	return prURL, nil
}
