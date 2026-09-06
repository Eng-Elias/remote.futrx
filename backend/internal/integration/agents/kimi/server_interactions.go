package kimi

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"strings"

	"github.com/futrx-com/remote.futrx.com/internal/agent"
)

type nativeQuestion struct {
	ID       string `json:"id"`
	Question string `json:"question"`
	Header   string `json:"header"`
	Body     string `json:"body"`
	Options  []struct {
		ID          string `json:"id"`
		Label       string `json:"label"`
		Description string `json:"description"`
	} `json:"options"`
	MultiSelect bool `json:"multi_select"`
	AllowOther  bool `json:"allow_other"`
}
type pendingInteraction struct {
	kind, id  string
	questions []nativeQuestion
}

func (r *serverRun) interactionEvent(kind string, raw json.RawMessage) error {
	var p struct {
		AgentID    string           `json:"agentId"`
		ApprovalID string           `json:"approval_id"`
		QuestionID string           `json:"question_id"`
		ToolName   string           `json:"tool_name"`
		Action     string           `json:"action"`
		Display    json.RawMessage  `json:"tool_input_display"`
		Questions  []nativeQuestion `json:"questions"`
	}
	if err := json.Unmarshal(raw, &p); err != nil {
		return err
	}
	family, _, _ := strings.Cut(kind, ".")
	id := p.ApprovalID
	if family == "question" {
		id = p.QuestionID
	}
	if id == "" {
		return fmt.Errorf("Kimi %s has no request ID", kind)
	}
	key := family + ":" + id
	ev := agent.Event{Type: agent.EventInteractionDone, InteractionID: key, ToolName: "kimi/" + family, Status: "resolved"}
	// Resolution payloads can contain private answers; never persist them.
	if !strings.HasSuffix(kind, ".requested") {
		delete(r.pending, key)
		r.publish(ev)
		return nil
	}
	if _, exists := r.pending[key]; exists {
		return nil
	}
	r.pending[key] = pendingInteraction{kind: family, id: id, questions: p.Questions}
	ev.Type = agent.EventInteractionRequest
	input := map[string]any{"agentId": p.AgentID, "tool": p.ToolName, "reason": p.Action, "display": p.Display}
	if family == "question" {
		ev.Status = "user_input"
		questions := make([]any, 0, len(p.Questions))
		for _, q := range p.Questions {
			questions = append(questions, map[string]any{"id": q.ID, "question": q.Question, "header": q.Header, "body": q.Body, "options": q.Options, "multiSelect": q.MultiSelect, "isOther": q.AllowOther})
		}
		input = map[string]any{"questions": questions, "allowDismiss": true}
	} else {
		ev.Status = "approval"
		input["allowFeedback"] = true
	}
	ev.Input, _ = json.Marshal(input)
	r.publish(ev)
	return nil
}

func (r *serverRun) answer(ctx context.Context, p *serverTransport, response agent.InteractionResponse) error {
	if taskID, ok := strings.CutPrefix(response.ID, "task:"+r.session+":"); ok {
		if !r.tasks[taskID] {
			return nil
		}
		var action struct {
			Action string `json:"action"`
		}
		if json.Unmarshal(response.Result, &action) != nil || (action.Action != "cancel" && action.Action != "detach") {
			return fmt.Errorf("invalid Kimi task action")
		}
		err := p.api(ctx, "POST", r.path()+"/tasks/"+url.PathEscape(taskID)+":"+action.Action, map[string]any{}, nil)
		if err == nil && action.Action == "detach" {
			for _, c := range r.children {
				if c.taskID == taskID {
					c.background = true
					r.childEvent(c, nil)
				}
			}
		}
		if e, ok := err.(*serverError); ok && e.Code == 40904 {
			return nil
		}
		return err
	}
	pending, ok := r.pending[response.ID]
	if !ok {
		return nil
	} // Late answers cannot target another request.
	var result struct {
		Decision      string `json:"decision"`
		Feedback      string `json:"feedback"`
		SelectedLabel string `json:"selected_label"`
		Dismiss       bool   `json:"dismiss"`
		Answers       map[string]struct {
			Answers []string `json:"answers"`
		} `json:"answers"`
	}
	if len(response.Result) > 0 {
		if err := json.Unmarshal(response.Result, &result); err != nil {
			return fmt.Errorf("invalid Kimi interaction response: %w", err)
		}
	}
	path := r.path() + "/" + pending.kind + "s/" + url.PathEscape(pending.id)
	body := map[string]any{}
	if pending.kind == "approval" {
		decision := ""
		switch result.Decision {
		case "accept", "approved":
			decision = "approved"
		case "acceptForSession":
			decision = "approved"
			body["scope"] = "session"
		case "decline", "rejected":
			decision = "rejected"
		case "cancel", "cancelled":
			decision = "cancelled"
		}
		if len(response.Error) > 0 {
			decision = "rejected"
		}
		if decision == "" {
			return fmt.Errorf("invalid Kimi approval decision")
		}
		body["decision"] = decision
		if result.Feedback != "" {
			body["feedback"] = result.Feedback
		}
		if result.SelectedLabel != "" {
			body["selected_label"] = result.SelectedLabel
		}
	} else if result.Dismiss || len(response.Error) > 0 {
		path += ":dismiss"
	} else {
		answers := map[string]any{}
		for _, q := range pending.questions {
			values, exists := result.Answers[q.ID]
			if !exists {
				return fmt.Errorf("missing answer to Kimi question %s", q.ID)
			}
			ids := []string{}
			other := ""
			seen := map[string]bool{}
			for _, value := range values.Answers {
				if seen[value] {
					continue
				}
				seen[value] = true
				found := ""
				for _, option := range q.Options {
					if value == option.ID {
						found = option.ID
						break
					}
				}
				if found == "" {
					if !q.AllowOther || other != "" {
						return fmt.Errorf("invalid answer to Kimi question %s", q.ID)
					}
					other = value
				} else {
					ids = append(ids, found)
				}
			}
			if !q.MultiSelect && len(ids)+boolInt(other != "") > 1 {
				return fmt.Errorf("Kimi question %s allows one answer", q.ID)
			}
			answer := map[string]any{"kind": "skipped"}
			switch {
			case other != "" && len(ids) > 0:
				answer = map[string]any{"kind": "multi_with_other", "option_ids": ids, "other_text": other}
			case other != "":
				answer = map[string]any{"kind": "other", "text": other}
			case q.MultiSelect && len(ids) > 0:
				answer = map[string]any{"kind": "multi", "option_ids": ids}
			case len(ids) == 1:
				answer = map[string]any{"kind": "single", "option_id": ids[0]}
			}
			answers[q.ID] = answer
		}
		body = map[string]any{"answers": answers, "method": "click"}
	}
	err := p.api(ctx, "POST", path, body, nil)
	if e, ok := err.(*serverError); ok && (e.Code == 40902 || e.Code == 40909 || e.Code == 40404 || e.Code == 40405) {
		err = nil
	}
	if err != nil {
		return err
	}
	if _, stillPending := r.pending[response.ID]; stillPending {
		delete(r.pending, response.ID)
		r.publish(agent.Event{Type: agent.EventInteractionDone, InteractionID: response.ID, ToolName: "kimi/" + pending.kind, Status: "resolved"})
	}
	return nil
}
func boolInt(b bool) int {
	if b {
		return 1
	}
	return 0
}
