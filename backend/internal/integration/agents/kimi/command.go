package kimi

import (
	"context"
	"os"
	"os/exec"

	"github.com/futrx-com/remote.futrx.com/internal/agent"
	agentruntime "github.com/futrx-com/remote.futrx.com/internal/integration/agents/runtime"
)

// containerKimiHome is the KIMI_CODE_HOME inside a project container — where
// kimi-code reads its OAuth credentials, config, and sessions.
const containerKimiHome = "/root/.kimi-code"

func (p *Provider) args(req agent.RunRequest) []string {
	return []string{"--input-type=module", "-e", serverBridge}
}

func (p *Provider) buildCmd(
	ctx context.Context,
	req agent.RunRequest,
	args []string,
	emit func(agent.Event),
) (*exec.Cmd, string, error) {
	cwd := req.Cwd
	if cwd == "" {
		cwd = os.Getenv("HOME")
		if cwd == "" {
			cwd = "/root"
		}
	}

	if req.ProjectID == "" || p.projectPreparer == nil {
		cmd := exec.CommandContext(context.WithoutCancel(ctx), "node", args...)
		cmd.Dir = cwd
		cmd.Env = append(os.Environ(), "KIMI_CODE_HOME="+hostKimiHome())
		cmd.Env = agent.WithRuntimeEnvironment(cmd.Env, req.RuntimeEnv)
		return cmd, "", nil
	}

	project, err := p.projectPreparer.Prepare(ctx, agent.ProjectPreparationRequest{
		ProjectID:           agent.ProjectID(req.ProjectID),
		ConversationID:      req.ConversationID,
		EnableBrowser:       req.EnableBrowser,
		EnableScheduleTools: req.EnableScheduleTools,
	}, emit)
	if err != nil {
		return nil, "", err
	}
	cmd := agentruntime.BuildContainerCommand(context.WithoutCancel(ctx), agentruntime.ContainerCommandSpec{
		ContainerName:      project.ContainerName,
		PrefixEnvironment:  []string{"HOME=/root", "KIMI_CODE_HOME=" + containerKimiHome},
		Secrets:            project.Secrets,
		RuntimeEnvironment: req.RuntimeEnv,
		Binary:             "node",
		Arguments:          args,
	})
	return cmd, project.ContainerName, nil
}

func hostKimiHome() string {
	if v := os.Getenv("KIMI_CODE_HOME"); v != "" {
		return v
	}
	if home := os.Getenv("HOME"); home != "" {
		return home + "/.kimi-code"
	}
	return "/root/.kimi-code"
}
