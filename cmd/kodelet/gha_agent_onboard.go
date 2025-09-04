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

type GhaAgentOnboardConfig struct {
	GithubApp           string
	AuthGatewayEndpoint string
}

func NewGhaAgentOnboardConfig() *GhaAgentOnboardConfig {
	return &GhaAgentOnboardConfig{
		GithubApp:           "kodelet",
		AuthGatewayEndpoint: "https://gha-auth-gateway.kodelet.com/api/github",
	}
}

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

		config := getGhaAgentOnboardConfigFromFlags(cmd)

		if !isGitRepository() {
			presenter.Error(errors.New("not a git repository"), "Please run this command from a git repository")
			os.Exit(1)
		}

		if !isGhCliInstalled() {
			presenter.Error(errors.New("GitHub CLI not installed"), "GitHub CLI (gh) is not installed. Please install it first")
			presenter.Info("Visit https://cli.github.com/ for installation instructions")
			os.Exit(1)
		}

		if !isGhAuthenticated() {
			presenter.Error(errors.New("not authenticated with GitHub"), "You are not authenticated with GitHub. Please run 'gh auth login' first")
			os.Exit(1)
		}

		presenter.Section("Step 1: GitHub App Installation")
		presenter.Info(fmt.Sprintf("Opening GitHub app installation page for '%s'...", config.GithubApp))
		appURL := fmt.Sprintf("https://github.com/apps/%s", config.GithubApp)

		if err := validateURL(appURL); err != nil {
			presenter.Error(err, "Invalid GitHub app URL")
			os.Exit(1)
		}

		if err := openInBrowser(appURL); err != nil {
			presenter.Warning(fmt.Sprintf("Failed to open browser automatically. Please manually open: %s", appURL))
		} else {
			presenter.Success(fmt.Sprintf("Opened: %s", appURL))
		}

		presenter.Info("Press Enter to continue once the app is installed...")
		reader := bufio.NewReader(os.Stdin)
		reader.ReadString('\n')

		presenter.Section("Step 2: API Key Setup")
		presenter.Info("Checking ANTHROPIC_API_KEY secret...")
		if err := setupAnthropicAPIKey(ctx); err != nil {
			presenter.Error(err, "Failed to set up ANTHROPIC_API_KEY")
			os.Exit(1)
		}

		presenter.Section("Step 3: Branch and Workflow Setup")
		presenter.Info("Creating git branch and workflow file...")

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

		presenter.Section("Step 5: Commit and Pull Request")
		presenter.Info("Creating commit and pull request...")

		prURL, err := commitAndCreatePR(ctx, branchName)
		if err != nil {
			presenter.Error(err, "Failed to create commit and PR")
			os.Exit(1)
		}

		presenter.Info(fmt.Sprintf("Checking out back to original branch: %s", currentBranch))
		if err := checkoutBranch(currentBranch); err != nil {
			presenter.Warning(fmt.Sprintf("Failed to checkout back to original branch %s: %s", currentBranch, err))
		}

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

func commandExists(cmd string) bool {
	_, err := exec.LookPath(cmd)
	return err == nil
}

func getCurrentBranch() (string, error) {
	cmd := exec.Command("git", "rev-parse", "--abbrev-ref", "HEAD")
	output, err := cmd.Output()
	if err != nil {
		return "", errors.Wrap(err, "error getting current branch")
	}
	return strings.TrimSpace(string(output)), nil
}

func checkoutBranch(branchName string) error {
	cmd := exec.Command("git", "checkout", branchName)
	if err := cmd.Run(); err != nil {
		return errors.Wrap(err, fmt.Sprintf("error checking out to branch %s", branchName))
	}
	return nil
}

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

	if parsedURL.Scheme != "https" {
		return errors.New("GitHub app URLs must use HTTPS")
	}

	if parsedURL.Host != "github.com" {
		return errors.New("GitHub app URLs must be on github.com domain")
	}

	return nil
}

func setupAnthropicAPIKey(_ context.Context) error {
	secretExists, err := checkGitHubSecret("ANTHROPIC_API_KEY")
	if err != nil {
		return errors.Wrap(err, "error checking secret")
	}

	if secretExists {
		presenter.Success("ANTHROPIC_API_KEY secret already exists")
		return nil
	}

	apiKey := os.Getenv("ANTHROPIC_API_KEY")

	if apiKey == "" {
		presenter.Info("ANTHROPIC_API_KEY not found. Please enter your Anthropic API key: ")
		reader := bufio.NewReader(os.Stdin)
		input, err := reader.ReadString('\n')
		if err != nil {
			return errors.Wrap(err, "error reading input")
		}
		apiKey = strings.TrimSpace(input)
	} else {
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

	cmd := exec.Command("gh", "secret", "set", "ANTHROPIC_API_KEY", "--body", apiKey)
	if err := cmd.Run(); err != nil {
		return errors.Wrap(err, "error setting GitHub secret")
	}

	presenter.Success("ANTHROPIC_API_KEY secret set successfully")
	return nil
}

func checkGitHubSecret(secretName string) (bool, error) {
	cmd := exec.Command("gh", "secret", "list")
	output, err := cmd.Output()
	if err != nil {
		return false, err
	}

	return strings.Contains(string(output), secretName), nil
}

func createBranchAndWorkflow(_ context.Context, branchName string, config *GhaAgentOnboardConfig) error {
	cmd := exec.Command("git", "checkout", "-b", branchName)
	if err := cmd.Run(); err != nil {
		return errors.Wrap(err, "error creating branch")
	}

	workflowDir := ".github/workflows"
	if err := os.MkdirAll(workflowDir, 0755); err != nil {
		return errors.Wrap(err, "error creating workflow directory")
	}

	workflowContent, err := generateWorkflowTemplate(config)
	if err != nil {
		return errors.Wrap(err, "error generating workflow template")
	}

	workflowPath := fmt.Sprintf("%s/kodelet.yaml", workflowDir)
	if err := os.WriteFile(workflowPath, []byte(workflowContent), 0644); err != nil {
		return errors.Wrap(err, "error writing workflow file")
	}

	presenter.Success(fmt.Sprintf("Created workflow file: %s", workflowPath))
	presenter.Success(fmt.Sprintf("Created branch: %s", branchName))
	return nil
}

func executeCommandWithStreaming(_ context.Context, cmd *exec.Cmd) error {
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return errors.Wrap(err, "error creating stdout pipe")
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return errors.Wrap(err, "error creating stderr pipe")
	}

	if err := cmd.Start(); err != nil {
		return errors.Wrap(err, "error starting command")
	}

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

	wg.Wait()

	if err := cmd.Wait(); err != nil {
		return errors.Wrap(err, "error executing command")
	}
	return nil
}

func commitAndCreatePR(_ context.Context, branchName string) (string, error) {
	workflowFile := ".github/workflows/kodelet.yaml"
	cmd := exec.Command("git", "add", workflowFile)
	if err := cmd.Run(); err != nil {
		return "", errors.Wrap(err, "error adding workflow file")
	}

	commitMsg := "onboard kodelet background agent"
	cmd = exec.Command("git", "commit", "-m", commitMsg)
	if err := cmd.Run(); err != nil {
		return "", errors.Wrap(err, "error committing changes")
	}

	cmd = exec.Command("git", "push", "origin", branchName)
	if err := cmd.Run(); err != nil {
		return "", errors.Wrap(err, "error pushing branch")
	}

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
