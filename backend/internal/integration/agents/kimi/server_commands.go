package kimi

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"github.com/futrx-com/remote.futrx.com/internal/agent"
)

// Commands are explicit user actions. Plain prompts are always sent verbatim;
// command arguments are never interpolated into a shell or an arbitrary URL.
func (r *serverRun) submit(ctx context.Context, p *serverTransport) (bool, error) {
	prompt := r.req.Prompt
	commandSource := r.req.UserPrompt
	if commandSource == "" {
		commandSource = prompt
	}
	commandLine, rest, hasRest := strings.Cut(strings.TrimSpace(commandSource), "\n")
	command, args, _ := strings.Cut(commandLine, " ")
	args = strings.TrimSpace(args)
	prefix := ""
	if r.req.UserPrompt != "" && strings.HasSuffix(prompt, r.req.UserPrompt) {
		prefix = strings.TrimSuffix(prompt, r.req.UserPrompt)
	}

	body := map[string]any{"disabled_tools": []string{}, "content": []any{map[string]any{"type": "text", "text": prompt}}}
	if !r.req.EnableBrowser {
		body["disabled_tools"] = []string{"mcp__remote_browser__*"}
	}
	profile := func(config map[string]any) error {
		return p.api(ctx, "POST", r.path()+"/profile", map[string]any{"agent_config": config}, nil)
	}
	done := func(message string, err error) (bool, error) {
		if err == nil {
			r.publish(agent.Event{Type: agent.EventAssistantTextDelta, Text: message})
		}
		return true, err
	}
	switch command {
	case "/btw":
		question := strings.TrimSpace(args + "\n" + rest)
		if question == "" {
			return true, fmt.Errorf("use /btw <side question>")
		}
		var side struct {
			AgentID string `json:"agent_id"`
		}
		if err := p.api(ctx, "POST", r.path()+":btw", map[string]any{}, &side); err != nil {
			return false, err
		}
		if side.AgentID == "" {
			return false, fmt.Errorf("Kimi did not return a side-conversation agent")
		}
		c := r.child(side.AgentID)
		c.name, c.description, c.sideConversation = "Side question", question, true
		r.childEvent(c, nil)
		r.mainEnded = true
		body["agent_id"] = side.AgentID
		body["content"] = []any{map[string]any{"type": "text", "text": prefix + question}}
		delete(body, "disabled_tools") // Keep the native side agent's tool policy.
		return false, p.api(ctx, "POST", r.path()+"/prompts", body, nil)
	case "/tower":
		if args != "on" && args != "off" {
			return true, fmt.Errorf("use /tower on or /tower off")
		}
		if err := profile(map[string]any{"tower_mode": args == "on"}); err != nil {
			return true, err
		}
		if !hasRest {
			return done("Kimi tower mode "+args+".", nil)
		}
	case "/swarm":
		enabled := args != "off"
		if args != "" && args != "on" && args != "off" {
			return true, fmt.Errorf("use /swarm on or /swarm off, followed by an optional prompt on the next line")
		}
		if err := profile(map[string]any{"swarm_mode": enabled}); err != nil {
			return true, err
		}
		if !hasRest {
			return done("Kimi swarm mode "+map[bool]string{true: "enabled.", false: "disabled."}[enabled], nil)
		}
	case "/goal":
		config := map[string]any{}
		switch args {
		case "pause", "resume", "cancel":
			config["goal_control"] = args
		default:
			if args == "" {
				return true, fmt.Errorf("use /goal <objective>, /goal pause, /goal resume, or /goal cancel")
			}
			config["goal_objective"] = args
		}
		if err := profile(config); err != nil {
			return true, err
		}
		if args == "pause" || args == "cancel" {
			return done("Kimi goal "+args+" requested.", nil)
		}
		if !hasRest {
			rest = "Continue the current goal."
		}
	case "/agent":
		if args == "" || !hasRest || strings.TrimSpace(rest) == "" {
			return true, fmt.Errorf("use /agent <profile> followed by a prompt on the next line")
		}
		body["profile"] = args
		body["model"] = r.req.Model
		if r.thinking != "" {
			body["thinking"] = r.thinking
		}
	case "/compact":
		r.compacting = true
		err := p.api(ctx, "POST", r.path()+":compact", map[string]any{"instruction": strings.TrimSpace(prefix + args + "\n" + rest)}, nil)
		return false, err
	case "/undo":
		count := 1
		if args != "" {
			var err error
			count, err = strconv.Atoi(args)
			if err != nil || count < 1 {
				return true, fmt.Errorf("use /undo or /undo <positive number of turns>")
			}
		}
		return done(fmt.Sprintf("Rewound Kimi's context by %d turn(s).", count), p.api(ctx, "POST", r.path()+":undo", map[string]any{"count": count}, nil))
	case "/kimi":
		if args == "" || args == "help" {
			return done("Kimi controls:\n- /agent <profile> followed by a prompt on the next line\n- /btw <side question>\n- /swarm on or /swarm off\n- /tower on or /tower off\n- /goal <objective>, pause, resume, or cancel\n- /compact [instructions]\n- /undo [turn count]\n- /skill:<name> [arguments]\nModels, Thinking, Plan mode, and approvals are in the composer. Ask Kimi to manage tasks, MCP servers, plugins, hooks, or scheduled work using its native tools.", nil)
		}
		return true, fmt.Errorf("unknown Kimi command; use /kimi help")
	default:
		if strings.HasPrefix(command, "/skill:") {
			name := strings.TrimPrefix(command, "/skill:")
			if name == "" {
				return true, fmt.Errorf("missing Kimi skill name")
			}
			body["skills"] = []any{map[string]any{"name": name, "args": strings.TrimSpace(args + "\n" + rest)}}
			body["content"] = []any{map[string]any{"type": "text", "text": prefix + "Use the selected skill."}}
			return false, p.api(ctx, "POST", r.path()+"/prompts", body, nil)
		}
		return false, p.api(ctx, "POST", r.path()+"/prompts", body, nil)
	}
	body["content"] = []any{map[string]any{"type": "text", "text": prefix + rest}}
	return false, p.api(ctx, "POST", r.path()+"/prompts", body, nil)
}

const remoteBrowserName = "remote_browser"

func (r *serverRun) prepareBrowser(ctx context.Context, p *serverTransport) error {
	if !r.req.EnableBrowser {
		return nil
	}
	desired := map[string]any{"transport": "stdio", "command": "npx", "args": []string{"@playwright/mcp", "--cdp-endpoint", "http://127.0.0.1:9222", "--caps=vision"}}
	var existing struct {
		Config struct {
			Command string   `json:"command"`
			Args    []string `json:"args"`
		} `json:"config"`
	}
	err := p.api(ctx, "GET", "/api/v2/mcp/servers/"+remoteBrowserName, nil, &existing)
	if err == nil {
		args, _ := json.Marshal(existing.Config.Args)
		want, _ := json.Marshal(desired["args"])
		if existing.Config.Command != "npx" || string(args) != string(want) {
			return fmt.Errorf("Kimi MCP name %q is already configured differently", remoteBrowserName)
		}
		return nil
	}
	apiErr, ok := err.(*serverError)
	if !ok || apiErr.Code != 40408 {
		return err
	}
	// The dedicated entry is merged by Kimi's management API, which preserves
	// unrelated user/project/plugin entries and refuses read-only collisions.
	desired["name"] = remoteBrowserName
	return p.api(ctx, "POST", "/api/v2/mcp/servers", desired, nil)
}

func (r *serverRun) userCommand() string {
	source := r.req.UserPrompt
	if source == "" {
		source = r.req.Prompt
	}
	fields := strings.Fields(source)
	if len(fields) == 0 {
		return ""
	}
	return fields[0]
}
