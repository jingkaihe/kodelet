package agents

import (
	"context"

	"github.com/pkg/errors"

	"github.com/jingkaihe/kodelet/pkg/logger"
	tooltypes "github.com/jingkaihe/kodelet/pkg/types/tools"
)

// AgentManager handles the loading and management of named agents
type AgentManager struct {
	processor *AgentProcessor
	agents    []*Agent
	tools     []tooltypes.Tool
}

// NewAgentManager creates a new agent manager with default configuration
func NewAgentManager(ctx context.Context) (*AgentManager, error) {
	processor, err := NewAgentProcessor()
	if err != nil {
		return nil, errors.Wrap(err, "failed to create agent processor")
	}

	return &AgentManager{
		processor: processor,
	}, nil
}

// LoadAllAgents loads all available agents and converts them to tools
func (am *AgentManager) LoadAllAgents(ctx context.Context) error {
	agents, err := am.processor.ListAgents(ctx)
	if err != nil {
		return errors.Wrap(err, "failed to list agents")
	}

	var tools []tooltypes.Tool
	for _, agent := range agents {
		// Validate each agent before creating a tool
		if err := am.processor.ValidateAgent(agent); err != nil {
			logger.G(ctx).WithField("agent", agent.Metadata.Name).WithError(err).Warn("Invalid agent configuration, skipping")
			continue
		}

		tool := NewNamedAgentTool(agent)
		tools = append(tools, tool)

		logger.G(ctx).WithFields(map[string]interface{}{
			"agent":    agent.Metadata.Name,
			"provider": agent.Metadata.Provider,
			"model":    agent.Metadata.Model,
		}).Debug("Registered named agent as tool")
	}

	am.agents = agents
	am.tools = tools

	logger.G(ctx).WithField("count", len(tools)).Info("Loaded named agents as tools")
	return nil
}

// GetAgentTools returns all loaded agent tools
func (am *AgentManager) GetAgentTools() []tooltypes.Tool {
	return am.tools
}

// GetAgent returns a specific agent by name
func (am *AgentManager) GetAgent(name string) (*Agent, error) {
	for _, agent := range am.agents {
		if agent.Metadata.Name == name {
			return agent, nil
		}
	}
	return nil, errors.Errorf("agent '%s' not found", name)
}

// ListAgentNames returns the names of all loaded agents
func (am *AgentManager) ListAgentNames() []string {
	var names []string
	for _, agent := range am.agents {
		names = append(names, agent.Metadata.Name)
	}
	return names
}

// CreateAgentManagerFromContext creates an agent manager and loads all agents
// This is the main entry point for integrating with the rest of kodelet
func CreateAgentManagerFromContext(ctx context.Context) (*AgentManager, error) {
	manager, err := NewAgentManager(ctx)
	if err != nil {
		return nil, errors.Wrap(err, "failed to create agent manager")
	}

	if err := manager.LoadAllAgents(ctx); err != nil {
		return nil, errors.Wrap(err, "failed to load agents")
	}

	return manager, nil
}