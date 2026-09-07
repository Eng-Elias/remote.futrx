package devin

import (
	"context"
	"errors"
	"time"

	"github.com/futrx-com/remote.futrx.com/internal/agent"
	"github.com/futrx-com/remote.futrx.com/internal/agent/provisioning"
)

// Provider adapts the Devin CLI as a headless agent provider. The full Run,
// Capabilities, and Parser implementations arrive in a later phase; this
// placeholder satisfies the agent.Provider interface so the module can be
// registered and the host installer can converge the binary.
type Provider struct {
	projectPreparer       agent.ProjectPreparer
	credentialCollector   provisioning.CredentialCollector
	profile               provisioning.Profile
	credentialSyncTimeout time.Duration
}

func newProvider(
	projectPreparer agent.ProjectPreparer,
	credentialCollector provisioning.CredentialCollector,
	profile provisioning.Profile,
	credentialSyncTimeout time.Duration,
) *Provider {
	return &Provider{
		projectPreparer:       projectPreparer,
		credentialCollector:   credentialCollector,
		profile:               profile.Clone(),
		credentialSyncTimeout: credentialSyncTimeout,
	}
}

func (p *Provider) ID() agent.ProviderID {
	return agent.ProviderDevin
}

// Parser returns the line parser for a run. Not yet implemented; returns nil.
func (p *Provider) Parser(req agent.RunRequest) agent.LineParser {
	return nil
}

// Run executes a Devin CLI turn. Not yet implemented.
func (p *Provider) Run(ctx context.Context, req agent.RunRequest, emit func(agent.Event)) error {
	return errors.New("devin provider: not yet implemented")
}

// Capabilities discovers the Devin model catalog. Not yet implemented.
func (p *Provider) Capabilities(ctx context.Context, req agent.CapabilityRequest) (agent.Capabilities, error) {
	return agent.Capabilities{}, errors.New("devin provider: not yet implemented")
}
