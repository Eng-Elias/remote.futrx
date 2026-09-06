package kimi

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/futrx-com/remote.futrx.com/internal/agent"
)

type serverEvent struct {
	Type      string          `json:"type"`
	SessionID string          `json:"session_id"`
	Seq       int64           `json:"seq"`
	Epoch     string          `json:"epoch"`
	Volatile  bool            `json:"volatile"`
	Payload   json.RawMessage `json:"payload"`
}

type nativePayload struct {
	Type             string          `json:"type"`
	AgentID          string          `json:"agentId"`
	TurnID           int64           `json:"turnId"`
	Step             int             `json:"step"`
	StepID           string          `json:"stepId"`
	Delta            string          `json:"delta"`
	ToolCallID       string          `json:"toolCallId"`
	Name             string          `json:"name"`
	Args             json.RawMessage `json:"args"`
	Output           json.RawMessage `json:"output"`
	IsError          bool            `json:"isError"`
	Reason           string          `json:"reason"`
	Error            json.RawMessage `json:"error"`
	SubagentID       string          `json:"subagentId"`
	SubagentName     string          `json:"subagentName"`
	ParentAgentID    string          `json:"parentAgentId"`
	CallerAgentID    string          `json:"callerAgentId"`
	ParentToolCallID string          `json:"parentToolCallId"`
	Description      string          `json:"description"`
	RunInBackground  bool            `json:"runInBackground"`
	TaskID           string          `json:"taskId"`
	ResultSummary    string          `json:"resultSummary"`
	Usage            *struct {
		InputOther         int64 `json:"inputOther"`
		Output             int64 `json:"output"`
		InputCacheRead     int64 `json:"inputCacheRead"`
		InputCacheCreation int64 `json:"inputCacheCreation"`
	} `json:"usage"`
}

type childTool struct {
	ID          string          `json:"id"`
	Name        string          `json:"name"`
	Status      string          `json:"status"`
	Input       json.RawMessage `json:"input,omitempty"`
	Output      string          `json:"output,omitempty"`
	IsError     bool            `json:"isError,omitempty"`
	StartedAt   int64           `json:"startedAt,omitempty"`
	CompletedAt int64           `json:"completedAt,omitempty"`
}
type childAgent struct {
	sideConversation                                                          bool
	lastEmit                                                                  time.Time
	id, name, parent, parentTool, taskID, status, text, thinking, description string
	background                                                                bool
	tools                                                                     []*childTool
}

type serverRun struct {
	thinking    string
	tasks       map[string]bool
	cronJobs    map[string]bool
	cronDirty   bool
	compacting  bool
	req         agent.RunRequest
	emit        func(agent.Event)
	session     string
	started     time.Time
	mainEnded   bool
	failure     string
	interrupted bool
	seq         int64
	epoch       string
	children    map[string]*childAgent
	tools       map[string]*childTool
	steps       map[string]bool
	pending     map[string]pendingInteraction
	usage       agent.Usage
}

func newServerRun(req agent.RunRequest, emit func(agent.Event)) *serverRun {
	return &serverRun{tasks: map[string]bool{}, cronJobs: map[string]bool{}, req: req, emit: emit, started: time.Now(), children: map[string]*childAgent{}, tools: map[string]*childTool{}, steps: map[string]bool{}, pending: map[string]pendingInteraction{}, usage: agent.Usage{Model: req.Model}}
}
func (r *serverRun) publish(ev agent.Event) {
	ev.T = time.Now().UnixMilli()
	ev.Provider = agent.ProviderKimi
	ev.ConversationID = r.req.ConversationID
	ev.SessionID = r.session
	r.emit(ev)
}
func (r *serverRun) usageRaw() json.RawMessage {
	r.usage.DurationMs = time.Since(r.started).Milliseconds()
	return r.usage.Raw()
}
func (r *serverRun) child(id string) *childAgent {
	c := r.children[id]
	if c == nil {
		c = &childAgent{id: id, name: id, parent: "main", status: "running"}
		r.children[id] = c
	}
	return c
}
func (r *serverRun) childEvent(c *childAgent, native *agent.NativeEnvelope) {
	if native != nil && (native.Method == "kimi/assistant.delta" || native.Method == "kimi/thinking.delta") && time.Since(c.lastEmit) < 100*time.Millisecond {
		return
	}
	c.lastEmit = time.Now()
	failed := 0
	for _, t := range c.tools {
		if t.IsError {
			failed++
		}
	}
	data, _ := json.Marshal(map[string]any{
		"type": "subagentThread", "senderThreadId": r.session + ":" + c.parent, "receiverThreadIds": []string{r.session + ":" + c.id},
		"agentsStates": map[string]any{r.session + ":" + c.id: map[string]string{"status": c.status, "message": c.text}},
		"prompt":       c.description, "agentNickname": c.name, "agentRole": c.name, "parentAgentId": c.parent, "parentToolCallId": c.parentTool, "taskId": c.taskID,
		"stopInteractionId": r.taskControlID(c), "runInBackground": c.background, "reasoning": c.thinking, "toolCount": len(c.tools), "failedToolCount": failed, "tools": c.tools,
	})
	r.publish(agent.Event{Type: agent.EventCollaboration, ItemID: "subagent:" + r.session + ":" + c.id, ToolName: c.name, Status: c.status, Data: data, Native: native})
}
func (r *serverRun) onEvent(raw json.RawMessage) error {
	var frame serverEvent
	if err := json.Unmarshal(raw, &frame); err != nil {
		return fmt.Errorf("decode Kimi event: %w", err)
	}
	if frame.Type == "resync_required" {
		return fmt.Errorf("Kimi requires event resynchronization; resume the session to recover")
	}
	if frame.SessionID != r.session {
		return nil
	}
	// Durable events can be replayed after subscribing. Volatile deltas share
	// sequence numbers with durable events and must not be deduplicated this way.
	if !frame.Volatile && frame.Seq > 0 {
		if frame.Epoch == r.epoch && frame.Seq <= r.seq {
			return nil
		}
		r.seq = frame.Seq
		r.epoch = frame.Epoch
	}
	var p nativePayload
	if err := json.Unmarshal(frame.Payload, &p); err != nil {
		return err
	}
	kind := strings.TrimPrefix(frame.Type, "event.")
	if p.Type != "" {
		kind = strings.TrimPrefix(p.Type, "event.")
	}
	who := p.AgentID
	if who == "" {
		who = "main"
	}
	native := &agent.NativeEnvelope{SchemaVersion: agent.NativeEnvelopeSchemaVersion, Method: "kimi/" + kind, ThreadID: r.session + ":" + who, TurnID: fmt.Sprint(p.TurnID), ItemID: p.ToolCallID, Payload: frame.Payload}
	ev := agent.Event{Type: agent.EventProviderNative, Native: native, Data: frame.Payload}
	if strings.HasPrefix(kind, "approval.") || strings.HasPrefix(kind, "question.") {
		return r.interactionEvent(kind, frame.Payload)
	}
	child := who != "main"
	switch kind {
	case "task.started", "task.terminated":
		var task struct {
			Info struct {
				ID      string `json:"taskId"`
				AgentID string `json:"agentId"`
				Status  string `json:"status"`
			} `json:"info"`
		}
		if json.Unmarshal(frame.Payload, &task) == nil && task.Info.ID != "" {
			// The public task API controls main-owned tasks. Still reconcile
			// nested task termination: cancellation has no subagent.failed event.
			if !child {
				if kind == "task.started" {
					r.tasks[task.Info.ID] = true
				} else {
					delete(r.tasks, task.Info.ID)
				}
			}
			if task.Info.AgentID != "" && task.Info.AgentID != "main" {
				c := r.child(task.Info.AgentID)
				c.taskID = task.Info.ID
				if kind == "task.terminated" {
					switch task.Info.Status {
					case "killed":
						c.status = "cancelled"
					case "failed", "timed_out", "lost":
						c.status = "failed"
					case "completed":
						c.status = "completed"
					}
				}
				r.childEvent(c, native)
			}
		}
	case "context.spliced":
		// Context snapshots may repeat private interaction answers and inline media.
		// Streaming text/tools already carry the user-visible transcript.
		native.Payload = nil
		ev.Data = nil
	case "cron.fired":
		var fired struct {
			Origin struct {
				JobID string `json:"jobId"`
				Stale bool   `json:"stale"`
			} `json:"origin"`
		}
		if json.Unmarshal(frame.Payload, &fired) == nil {
			if recurring, ok := r.cronJobs[fired.Origin.JobID]; ok && (!recurring || fired.Origin.Stale) {
				delete(r.cronJobs, fired.Origin.JobID)
				r.cronDirty = true
			}
		}
		r.mainEnded = false
	case "compaction.started":
		if !child {
			r.compacting = true
			ev.Type = agent.EventTurnStatus
			ev.Status = "compacting"
		}
	case "compaction.completed":
		if !child {
			r.compacting = false
			r.mainEnded = true
			ev.Type = agent.EventTurnStatus
			ev.Status = "waiting"
		}
	case "compaction.cancelled", "compaction.blocked":
		if !child {
			r.compacting = false
			r.interrupted = true
		}
	case "turn.started":
		if !child {
			r.mainEnded = false
			r.usage.Turns++
			ev.Type = agent.EventTurnStatus
			ev.Status = "running"
		}
	case "turn.ended":
		if child && r.child(who).sideConversation {
			c := r.child(who)
			c.status = p.Reason
			if p.Reason == "failed" || p.Reason == "blocked" {
				r.failure = nativeText(p.Error)
				if r.failure == "" {
					r.failure = "Kimi side question " + p.Reason
				}
				c.status, c.text = "failed", r.failure
			}
			if p.Reason == "cancelled" {
				r.interrupted = true
			}
			r.childEvent(c, native)
			return nil
		}
		if !child {
			r.mainEnded = true
			switch p.Reason {
			case "failed", "blocked":
				r.failure = nativeText(p.Error)
				if r.failure == "" {
					r.failure = "Kimi turn " + p.Reason
				}
			case "cancelled":
				r.interrupted = true
			}
			ev.Type = agent.EventTurnStatus
			ev.Status = p.Reason
			if p.Reason == "completed" {
				ev.Status = "waiting"
			}
		}
	case "turn.step.completed":
		key := fmt.Sprintf("%s:%d:%d:%s", who, p.TurnID, p.Step, p.StepID)
		if p.Usage != nil && !r.steps[key] {
			r.steps[key] = true
			r.usage.InputTokens += p.Usage.InputOther
			r.usage.OutputTokens += p.Usage.Output
			r.usage.CacheReadTokens += p.Usage.InputCacheRead
			r.usage.CacheWriteTokens += p.Usage.InputCacheCreation
			ev.Type = agent.EventUsageUpdated
			ev.Usage = r.usageRaw()
		}
	case "assistant.delta", "thinking.delta":
		if child {
			c := r.child(who)
			if kind == "assistant.delta" {
				c.text += p.Delta
			} else {
				c.thinking += p.Delta
			}
			r.childEvent(c, native)
			return nil
		}
		ev.Type = agent.EventAssistantTextDelta
		if kind == "thinking.delta" {
			ev.Type = agent.EventReasoningDelta
		}
		ev.Text = p.Delta
		ev.MessageID = fmt.Sprintf("%s:%d:%s", r.session, p.TurnID, kind)
	case "tool.call.started":
		id := fmt.Sprintf("%s:%s:%d:%s", r.session, who, p.TurnID, p.ToolCallID)
		t := &childTool{ID: id, Name: p.Name, Status: "running", Input: p.Args, StartedAt: time.Now().UnixMilli()}
		r.tools[id] = t
		if child {
			c := r.child(who)
			c.tools = append(c.tools, t)
			r.childEvent(c, native)
			return nil
		}
		ev.Type = agent.EventToolStarted
		ev.ItemID = id
		ev.ToolName = p.Name
		ev.Input = p.Args
	case "tool.result":
		id := fmt.Sprintf("%s:%s:%d:%s", r.session, who, p.TurnID, p.ToolCallID)
		t := r.tools[id]
		if t != nil {
			t.Status = "completed"
			t.Output = nativeText(p.Output)
			t.IsError = p.IsError
			t.CompletedAt = time.Now().UnixMilli()
			if t.IsError {
				t.Status = "failed"
			}
		}
		if t != nil && !child {
			r.cronTool(t)
		}
		if child {
			r.childEvent(r.child(who), native)
			return nil
		}
		ev.Type = agent.EventToolCompleted
		ev.ItemID = id
		ev.Output = nativeText(p.Output)
		ev.IsError = p.IsError
	case "subagent.spawned":
		c := r.child(p.SubagentID)
		c.name = p.SubagentName
		c.description = p.Description
		c.parent = p.ParentAgentID
		if c.parent == "" {
			c.parent = p.CallerAgentID
		}
		if c.parent == "" {
			c.parent = "main"
		}
		c.parentTool = p.ParentToolCallID
		c.taskID = p.TaskID
		c.background = p.RunInBackground
		c.status = "running"
		r.childEvent(c, native)
		return nil
	case "subagent.started", "subagent.suspended", "subagent.completed", "subagent.failed":
		c := r.child(p.SubagentID)
		switch kind {
		case "subagent.started":
			c.status = "running"
		case "subagent.suspended":
			c.status = "waiting"
		case "subagent.completed":
			c.status = "completed"
			if p.ResultSummary != "" {
				c.text = p.ResultSummary
			}
		case "subagent.failed":
			c.status = "failed"
			c.text = nativeText(p.Error)
		}
		// Child completion usage is cumulative; per-step usage above already includes it.
		r.childEvent(c, native)
		return nil
	}
	r.publish(ev)
	return nil
}
func nativeText(raw json.RawMessage) string {
	if len(raw) == 0 || string(raw) == "null" {
		return ""
	}
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return s
	}
	var e struct {
		Message string `json:"message"`
		Code    string `json:"code"`
	}
	if json.Unmarshal(raw, &e) == nil && e.Message != "" {
		if e.Code != "" {
			return e.Code + ": " + e.Message
		}
		return e.Message
	}
	return string(raw)
}

func (r *serverRun) taskControlID(c *childAgent) string {
	if c.taskID != "" && r.tasks[c.taskID] {
		return "task:" + r.session + ":" + c.taskID
	}
	return ""
}
