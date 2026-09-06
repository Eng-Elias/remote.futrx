package kimi

import (
	"context"
	"errors"
	"fmt"
	"log"
	"time"

	"github.com/futrx-com/remote.futrx.com/internal/agent"
	"github.com/futrx-com/remote.futrx.com/internal/agent/provisioning"
	agentruntime "github.com/futrx-com/remote.futrx.com/internal/integration/agents/runtime"
)

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
	return agent.ProviderKimi
}

func (p *Provider) Parser(req agent.RunRequest) agent.LineParser {
	return NewParser(req)
}

func (p *Provider) Run(ctx context.Context, req agent.RunRequest, emit func(agent.Event)) error {
	if emit == nil {
		emit = func(agent.Event) {}
	}
	if req.Provider == "" {
		req.Provider = agent.ProviderKimi
	}
	if req.Mode != "" && req.Mode != agent.RunModeDefault {
		return fmt.Errorf("Kimi does not support %q mode through Remote's prompt runner; select Default mode", req.Mode)
	}
	// This adapter does not invoke Kimi's fork command; forked chats start fresh.
	if req.Fork {
		req.ResumeID = ""
	}

	cmd, containerName, err := p.buildCmd(ctx, req, p.args(req), emit)
	if err != nil {
		return err
	}
	var completed *agent.Event
	sawOutput := false
	err = agentruntime.RunProcess(ctx, cmd, p.Parser(req), func(ev agent.Event) {
		// The resume hint can precede a failing exit (for example, a blocked
		// goal). Publish completion only after the process exits successfully.
		if ev.Type == agent.EventRunCompleted {
			completed = &ev
			return
		}
		if ev.Type == agent.EventAssistantTextDelta || ev.Type == agent.EventToolStarted || ev.Type == agent.EventToolCompleted {
			sawOutput = true
		}
		emit(ev)
	}, agentruntime.ProcessOptions{
		Name:           "kimi",
		LogID:          req.ConversationID,
		Provider:       agent.ProviderKimi,
		ConversationID: req.ConversationID,
	})
	if errors.Is(ctx.Err(), context.Canceled) {
		return nil
	}
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if err != nil {
		diagnostic := processDiagnostic(agentruntime.ErrorStderr(err))
		if !sawOutput && isMissingSession(diagnostic, req.ResumeID) {
			return agent.ErrSessionNotFound
		}
		message := fmt.Sprintf("Kimi run failed (%v)", err)
		if diagnostic != "" {
			message += ": " + diagnostic
		}
		emit(agent.Event{
			T: time.Now().UnixMilli(), Type: agent.EventRunFailed,
			Provider: agent.ProviderKimi, ConversationID: req.ConversationID,
			Message: message, IsError: true,
		})
		return agent.ErrRunFailed
	}
	if completed == nil {
		return fmt.Errorf("Kimi exited without a completion record; the response may be incomplete")
	}
	emit(*completed)
	if containerName != "" && p.credentialCollector != nil {
		syncCtx, cancel := context.WithTimeout(context.Background(), p.credentialSyncTimeout)
		defer cancel()
		if syncErr := p.credentialCollector.SyncFromContainer(syncCtx, containerName, p.profile.Credentials); syncErr != nil {
			log.Printf("kimi[%s] sync auth from %s: %v", req.ConversationID, containerName, syncErr)
		}
	}
	return err
}
