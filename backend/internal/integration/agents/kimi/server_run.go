package kimi

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"time"

	"github.com/futrx-com/remote.futrx.com/internal/agent"
	configconstants "github.com/futrx-com/remote.futrx.com/internal/config/constants"
)

func (r *serverRun) path() string { return "/api/v1/sessions/" + url.PathEscape(r.session) }

type nativeSession struct {
	ID       string `json:"id"`
	Metadata struct {
		Cwd      string          `json:"cwd"`
		CronJobs map[string]bool `json:"remote_kimi_cron_jobs"`
	} `json:"metadata"`
}
type nativeSnapshot struct {
	Seq   int64  `json:"as_of_seq"`
	Epoch string `json:"epoch"`
}
type nativeTasks struct {
	Items []struct {
		ID      string `json:"id"`
		Status  string `json:"status"`
		AgentID string `json:"agent_id"`
	} `json:"items"`
}

func (r *serverRun) execute(ctx context.Context, p *serverTransport) error {
	cwd := r.req.Cwd
	if r.req.ProjectID != "" {
		if cwd == "" {
			cwd = agent.ProjectWorkspacePath
		}
	} else if cwd == "" {
		cwd = os.Getenv("HOME")
		if cwd == "" {
			cwd = "/root"
		}
	}
	if err := r.prepareBrowser(ctx, p); err != nil {
		return err
	}
	var session nativeSession
	if r.req.ResumeID != "" {
		r.session = r.req.ResumeID
		if err := p.api(ctx, "GET", r.path(), nil, &session); err != nil {
			var apiErr *serverError
			if errors.As(err, &apiErr) && apiErr.Code == 40401 {
				return agent.ErrSessionNotFound
			}
			return err
		}
		if session.Metadata.Cwd != "" && filepath.Clean(session.Metadata.Cwd) != filepath.Clean(cwd) {
			return fmt.Errorf("Kimi session belongs to %s, not %s", session.Metadata.Cwd, cwd)
		}
		if r.req.Fork {
			if err := p.api(ctx, "POST", r.path()+":fork", map[string]any{}, &session); err != nil {
				return err
			}
		}
	} else {
		if err := p.api(ctx, "POST", "/api/v1/sessions", map[string]any{"metadata": map[string]any{"cwd": cwd}}, &session); err != nil {
			return err
		}
	}
	if session.ID == "" {
		return errors.New("Kimi did not return a session ID")
	}
	r.session = session.ID
	if session.Metadata.CronJobs != nil {
		r.cronJobs = session.Metadata.CronJobs
	}
	r.publish(agent.Event{Type: agent.EventSessionUpdated})
	var snapshot nativeSnapshot
	if err := p.api(ctx, "GET", r.path()+"/snapshot", nil, &snapshot); err != nil {
		return err
	}
	r.seq = snapshot.Seq
	r.epoch = snapshot.Epoch
	if err := p.request(ctx, map[string]any{"type": "subscribe", "payload": map[string]any{"session_ids": []string{r.session}, "cursors": map[string]any{r.session: map[string]any{"seq": r.seq, "epoch": r.epoch}}}}, nil); err != nil {
		return err
	}
	var status struct {
		Model string `json:"model"`
		Busy  bool   `json:"busy"`
	}
	if err := p.api(ctx, "GET", r.path()+"/status", nil, &status); err != nil {
		return err
	}
	if status.Busy {
		return errors.New("Kimi session already has an active turn")
	}
	var defaults struct {
		Model    string `json:"default_model"`
		Thinking struct {
			Effort  string `json:"effort"`
			Enabled *bool  `json:"enabled"`
		} `json:"thinking"`
	}
	if r.req.Model == "" || r.req.Preferences.ReasoningEffort == "" {
		if err := p.api(ctx, "GET", "/api/v1/config", nil, &defaults); err != nil {
			return err
		}
	}
	if r.req.Model == "" {
		r.req.Model = defaults.Model
	}
	thinking := string(r.req.Preferences.ReasoningEffort)
	if thinking == "" {
		thinking = defaults.Thinking.Effort
		if defaults.Thinking.Enabled != nil && !*defaults.Thinking.Enabled {
			thinking = "off"
		}
		if thinking == "" {
			var catalog struct {
				Items []struct {
					Model        string   `json:"model"`
					Capabilities []string `json:"capabilities"`
					Efforts      []string `json:"support_efforts"`
				} `json:"items"`
			}
			if err := p.api(ctx, "GET", "/api/v1/models", nil, &catalog); err != nil {
				return err
			}
			thinking = "off"
			for _, model := range catalog.Items {
				if model.Model != r.req.Model {
					continue
				}
				if len(model.Efforts) > 0 {
					thinking = "on"
				}
				for _, cap := range model.Capabilities {
					if cap == "thinking" || cap == "always_thinking" {
						thinking = "on"
					}
				}
			}
		}
	}

	config := map[string]any{"plan_mode": r.req.Mode == agent.RunModePlan}
	if model := normalizeKimiModel(r.req.Model); model != "" {
		config["model"] = model
		r.usage.Model = model
	}
	config["thinking"] = thinking
	r.thinking = thinking
	if r.userCommand() == "/agent" {
		delete(config, "model")
		delete(config, "thinking")
	}
	switch r.req.Preferences.ApprovalPolicy {
	case "never":
		config["permission_mode"] = "auto"
	case "untrusted":
		config["permission_mode"] = "manual"
	case "on-request":
		config["permission_mode"] = "yolo"
	case "":
	default:
		return fmt.Errorf("unsupported Kimi approval policy %q", r.req.Preferences.ApprovalPolicy)
	}
	if err := p.api(ctx, "POST", r.path()+"/profile", map[string]any{"agent_config": config}, nil); err != nil {
		return err
	}
	done, err := r.submit(ctx, p)
	if err != nil || done {
		return err
	}

	ticker := time.NewTicker(configconstants.KimiRunIdlePollInterval)
	defer ticker.Stop()
	responses := r.req.InteractionResponses
	// Confirm idle twice so background task completion callbacks can enqueue
	// follow-up turns. The main agent's first turn.ended is not run completion.
	idleCount := 0
	for {
		if r.failure != "" {
			return errors.New(r.failure)
		}
		if r.interrupted {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case frame, ok := <-p.frames:
			if !ok {
				return errors.New("Kimi event stream closed before completion")
			}
			if frame.Type == "fatal" {
				return errors.New(frame.Message)
			}
			if frame.Type != "event" {
				return errors.New("unexpected Kimi bridge response")
			}
			if err := r.onEvent(frame.Event); err != nil {
				return err
			}
			idleCount = 0
		case response, ok := <-responses:
			if !ok {
				responses = nil
				continue
			}
			if err := r.answer(ctx, p, response); err != nil {
				return err
			}
		case <-ticker.C:
			if r.compacting || !r.mainEnded || len(r.pending) > 0 {
				idleCount = 0
				continue
			}
			idle, err := r.idle(ctx, p)
			if err != nil {
				return err
			}
			if r.failure != "" {
				return errors.New(r.failure)
			}
			if r.interrupted {
				return nil
			}
			if !idle || !r.mainEnded || len(r.pending) > 0 {
				idleCount = 0
				continue
			}
			idleCount++
			if idleCount >= 2 {
				return nil
			}
		}
	}
}

func (r *serverRun) idle(ctx context.Context, p *serverTransport) (bool, error) {
	var status struct {
		Busy bool `json:"busy"`
	}
	if err := p.api(ctx, "GET", r.path()+"/status", nil, &status); err != nil {
		return false, err
	}
	if status.Busy {
		return false, nil
	}
	if err := r.saveCron(ctx, p); err != nil {
		return false, err
	}
	if len(r.cronJobs) > 0 {
		return false, nil
	}
	var tasks nativeTasks
	if err := p.api(ctx, "GET", r.path()+"/tasks?status=running", nil, &tasks); err != nil {
		return false, err
	}
	if len(tasks.Items) > 0 {
		return false, nil
	}
	var prompts struct {
		Items []struct {
			Status string `json:"status"`
		} `json:"items"`
	}
	if err := p.api(ctx, "GET", r.path()+"/prompts", nil, &prompts); err != nil {
		return false, err
	}
	for _, prompt := range prompts.Items {
		if prompt.Status == "queued" || prompt.Status == "running" {
			return false, nil
		}
	}
	var goal *struct {
		Status string `json:"status"`
	}
	if err := p.api(ctx, "GET", r.path()+"/goal", nil, &goal); err != nil {
		return false, err
	}
	if goal != nil && goal.Status == "active" {
		return false, nil
	}
	for _, c := range r.children {
		if c.status == "running" {
			return false, nil
		}
	}
	return true, nil
}

func (r *serverRun) abort(p *serverTransport) {
	if r.session == "" {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), configconstants.KimiRunAbortTimeout)
	defer cancel()
	_ = r.saveCron(ctx, p)
	// Server shutdown disposes all agents too. First persist cancellations.
	_ = p.api(ctx, "POST", r.path()+":abort", map[string]any{}, nil)
	var tasks nativeTasks
	if p.api(ctx, "GET", r.path()+"/tasks?status=running", nil, &tasks) == nil {
		for _, task := range tasks.Items {
			_ = p.api(ctx, "POST", r.path()+"/tasks/"+url.PathEscape(task.ID)+":cancel", map[string]any{}, nil)
		}
	}
}
