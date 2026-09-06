package agentenv

// SetChildPrompt installs runner-resolved prompt content for a delegated child.
// Call only before opening the environment. No daemon-side file is read.
func (e *RemoteEnvironment) SetChildPrompt(prompt string) {
	e.childPrompt = &prompt
}

// SetChildRunID pins the centrally allocated durable execution identity.
func (e *RemoteEnvironment) SetChildRunID(runID string) {
	e.newRunID = func() (string, error) { return runID, nil }
}
