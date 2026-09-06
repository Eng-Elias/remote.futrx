// Package browser coordinates browser provisioning and runtime transitions for
// project containers.
package browser

import (
	"context"

	"github.com/futrx-com/remote.futrx.com/internal/agent"
	serviceproject "github.com/futrx-com/remote.futrx.com/internal/service/project"
)

// StackProvisioner prepares the browser packages and runtime assets in a
// container.
type StackProvisioner interface {
	Provision(ctx context.Context, containerName string) error
}

// Runtime controls the already-provisioned browser processes.
type Runtime interface {
	Start(ctx context.Context, containerName string) error
	StartCore(ctx context.Context, containerName string) error
	StartView(ctx context.Context, containerName string) error
	Stop(ctx context.Context, containerName string) error
	StopView(ctx context.Context, containerName string) error
	Running(ctx context.Context, containerName string) (bool, error)
	Status(ctx context.Context, containerName string) (serviceproject.AgentBrowserInfo, error)
}

// Tooling publishes browser assets consumed by shared agent and launch
// preparation.
type Tooling interface {
	EnsureSkill(ctx context.Context, containerName string) error
	EnsureScript(ctx context.Context, containerName string) error
	EnsureMCP(ctx context.Context, containerName string) error
	EnsureNesting(ctx context.Context, containerName string) error
}

// Connector supplies scoped agent and human-view connections when the
// browser runtime is hosted outside the project container.
type Connector interface {
	Connection(ctx context.Context, containerName string) (agent.BrowserConnection, error)
	ViewTarget(ctx context.Context, containerName string) (serviceproject.AgentBrowserViewTarget, error)
}

// Dependencies groups the independently replaceable browser adapters.
type Dependencies struct {
	Provisioner StackProvisioner
	Runtime     Runtime
	Tooling     Tooling
	Connector   Connector
}

// Service owns the provision-before-start policy and exposes browser
// capabilities to projects plus launch and shared agent preparation.
type Service struct {
	provisioner StackProvisioner
	runtime     Runtime
	tooling     Tooling
	connector   Connector
	port        int
}

func NewService(deps Dependencies, port int) *Service {
	return &Service{
		provisioner: deps.Provisioner,
		runtime:     deps.Runtime,
		tooling:     deps.Tooling,
		connector:   deps.Connector,
		port:        port,
	}
}

// Port returns the legacy noVNC port, or zero for the shared broker view.
func (s *Service) Port() int { return s.port }

// Ensure provisions the selected browser runtime before starting core and view.
func (s *Service) Ensure(ctx context.Context, containerName string) error {
	return s.provisionAndStart(ctx, containerName, s.runtime.Start)
}

// EnsureCore provisions the browser stack before starting its shared core.
func (s *Service) EnsureCore(ctx context.Context, containerName string) error {
	return s.provisionAndStart(ctx, containerName, s.runtime.StartCore)
}

// EnsureView provisions the browser runtime before starting its human view.
func (s *Service) EnsureView(ctx context.Context, containerName string) error {
	return s.provisionAndStart(ctx, containerName, s.runtime.StartView)
}

func (s *Service) provisionAndStart(
	ctx context.Context,
	containerName string,
	start func(context.Context, string) error,
) error {
	if err := s.provisioner.Provision(ctx, containerName); err != nil {
		return err
	}
	return start(ctx, containerName)
}

func (s *Service) Stop(ctx context.Context, containerName string) error {
	return s.runtime.Stop(ctx, containerName)
}

// Delete stops the browser and removes broker-owned project state when the
// selected runtime supports it. Legacy profile data is removed with the
// project's workspace by the normal project deletion path.
func (s *Service) Delete(ctx context.Context, containerName string) error {
	if deleter, ok := s.runtime.(interface {
		Delete(context.Context, string) error
	}); ok {
		return deleter.Delete(ctx, containerName)
	}
	return s.runtime.Stop(ctx, containerName)
}

func (s *Service) StopView(ctx context.Context, containerName string) error {
	return s.runtime.StopView(ctx, containerName)
}

func (s *Service) Running(ctx context.Context, containerName string) (bool, error) {
	return s.runtime.Running(ctx, containerName)
}

func (s *Service) Status(ctx context.Context, containerName string) (serviceproject.AgentBrowserInfo, error) {
	return s.runtime.Status(ctx, containerName)
}

func (s *Service) EnsureSkill(ctx context.Context, containerName string) error {
	return s.tooling.EnsureSkill(ctx, containerName)
}

func (s *Service) EnsureScript(ctx context.Context, containerName string) error {
	return s.tooling.EnsureScript(ctx, containerName)
}

func (s *Service) EnsureMCP(ctx context.Context, containerName string) error {
	return s.tooling.EnsureMCP(ctx, containerName)
}

func (s *Service) EnsureNesting(ctx context.Context, containerName string) error {
	return s.tooling.EnsureNesting(ctx, containerName)
}

// Connection returns the scoped remote MCP endpoint. A zero connection keeps
// legacy in-container CDP behavior for installations without the broker.
func (s *Service) Connection(ctx context.Context, containerName string) (agent.BrowserConnection, error) {
	if s.connector == nil {
		return agent.BrowserConnection{}, nil
	}
	return s.connector.Connection(ctx, containerName)
}

// ViewTarget returns the internal WebSocket endpoint used by the authenticated
// HTTP handler. The bearer credential is injected server-side.
func (s *Service) ViewTarget(ctx context.Context, containerName string) (serviceproject.AgentBrowserViewTarget, error) {
	if s.connector == nil {
		return serviceproject.AgentBrowserViewTarget{}, serviceproject.ErrAgentBrowserViewUnavailable
	}
	return s.connector.ViewTarget(ctx, containerName)
}
