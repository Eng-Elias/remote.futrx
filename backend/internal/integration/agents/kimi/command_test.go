package kimi

import (
	"slices"
	"testing"

	"github.com/futrx-com/remote.futrx.com/internal/agent"
)

func TestArgsNeverCombinePromptAndPlan(t *testing.T) {
	provider := &Provider{}
	for _, mode := range []agent.RunMode{"", agent.RunModeDefault, agent.RunModePlan} {
		args := provider.args(agent.RunRequest{Prompt: "inspect", Mode: mode})
		if slices.Contains(args, "--plan") {
			t.Fatalf("prompt mode cannot accept --plan: %#v", args)
		}
	}
}

func TestArgsPreserveExactConfiguredModelAlias(t *testing.T) {
	provider := &Provider{}
	args := provider.args(agent.RunRequest{Prompt: "inspect", Model: "moonshot/kimi-k2[1m]"})
	for index := 0; index+1 < len(args); index++ {
		if args[index] == "--model" && args[index+1] == "moonshot/kimi-k2[1m]" {
			return
		}
	}
	t.Fatalf("exact model alias missing: %#v", args)
}
