package devinharness

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/futrx-com/remote.futrx.com/internal/agent"
)

// acpRequestHandler stores pending server-to-client requests (permission
// prompts, elicitations) by JSON-RPC id, emits EventInteractionRequest, and
// routes InteractionResponse back when the run-scoped channel delivers a user
// answer.
type acpRequestHandler struct {
	req     agent.RunRequest
	emit    func(agent.Event)
	write   func(any) error
	pending map[string]acpPendingRequest
}

type acpPendingRequest struct {
	envelope  acpEnvelope
	createdAt time.Time
}

func newACPRequestHandler(
	req agent.RunRequest,
	emit func(agent.Event),
	write func(any) error,
) *acpRequestHandler {
	return &acpRequestHandler{
		req:     req,
		emit:    emit,
		write:   write,
		pending: make(map[string]acpPendingRequest),
	}
}

// Handle stores a server-to-client request and emits an EventInteractionRequest
// for the UI. The request ID is preserved verbatim so string and numeric IDs
// remain distinct.
func (handler *acpRequestHandler) Handle(envelope acpEnvelope) error {
	requestID, err := jsonRPCIDKey(envelope.ID)
	if err != nil {
		return err
	}
	if _, exists := handler.pending[requestID]; exists {
		return fmt.Errorf("duplicate ACP request %s", requestID)
	}

	handler.pending[requestID] = acpPendingRequest{envelope: envelope, createdAt: time.Now()}
	handler.emit(agent.Event{
		T:              time.Now().UnixMilli(),
		Type:           agent.EventInteractionRequest,
		Provider:       handler.req.Provider,
		ConversationID: handler.req.ConversationID,
		ToolName:       envelope.Method,
		Input:          cloneRaw(envelope.Params),
		InteractionID:  requestID,
		Status:         interactionKind(envelope.Method),
		Native: &agent.NativeEnvelope{
			SchemaVersion: agent.NativeEnvelopeSchemaVersion,
			Method:        envelope.Method,
			RequestID:     requestID,
			Payload:       cloneRaw(envelope.Params),
		},
	})
	return nil
}

// Respond routes a user-supplied InteractionResponse back to the ACP server.
func (handler *acpRequestHandler) Respond(response agent.InteractionResponse) error {
	pending, ok := handler.pending[response.ID]
	if !ok {
		return fmt.Errorf("%w: %s", errors.New("unknown ACP interaction"), response.ID)
	}

	request := pending.envelope
	wire := map[string]any{"jsonrpc": "2.0", "id": request.ID}
	switch {
	case len(response.Error) > 0:
		if !json.Valid(response.Error) {
			return errors.New("invalid JSON-RPC interaction error")
		}
		wire["error"] = json.RawMessage(response.Error)
	default:
		result := response.Result
		if len(result) == 0 {
			result = json.RawMessage("null")
		}
		if !json.Valid(result) {
			return errors.New("invalid JSON-RPC interaction result")
		}
		wire["result"] = json.RawMessage(result)
	}

	if err := handler.write(wire); err != nil {
		return err
	}
	delete(handler.pending, response.ID)
	handler.emit(handler.resolvedEvent(request, response.ID, interactionResponseStatus(request.Method, response.Result, response.Error)))
	return nil
}

// ResolveAll emits resolved events for all pending requests without sending
// responses to the server. Used when the turn ends.
func (handler *acpRequestHandler) ResolveAll(status string) {
	for requestID, pending := range handler.pending {
		handler.emit(handler.resolvedEvent(pending.envelope, requestID, status))
		delete(handler.pending, requestID)
	}
}

func (handler *acpRequestHandler) resolvedEvent(request acpEnvelope, requestID string, status string) agent.Event {
	return agent.Event{
		T:              time.Now().UnixMilli(),
		Type:           agent.EventInteractionDone,
		Provider:       handler.req.Provider,
		ConversationID: handler.req.ConversationID,
		ToolName:       request.Method,
		InteractionID:  requestID,
		Status:         status,
		Native: &agent.NativeEnvelope{
			SchemaVersion: agent.NativeEnvelopeSchemaVersion,
			Method:        request.Method,
			RequestID:     requestID,
		},
	}
}

func jsonRPCIDKey(raw json.RawMessage) (string, error) {
	if len(raw) == 0 || !json.Valid(raw) {
		return "", errors.New("ACP request has an invalid id")
	}
	var compact bytes.Buffer
	if err := json.Compact(&compact, raw); err != nil {
		return "", err
	}
	key := compact.String()
	if key == "null" || key == "" || (key[0] != '"' && !strings.ContainsAny(key[:1], "-0123456789")) {
		return "", errors.New("ACP request id must be a string or number")
	}
	return key, nil
}

func interactionKind(method string) string {
	switch method {
	case "session/request_permission":
		return "permission"
	case "session/elicitation/create":
		return "elicitation"
	default:
		return "provider_request"
	}
}

func interactionResponseStatus(method string, result, responseError json.RawMessage) string {
	if len(responseError) > 0 {
		return "response_error"
	}
	var value map[string]json.RawMessage
	if json.Unmarshal(result, &value) != nil {
		return "answered"
	}
	if raw, exists := value["outcome"]; exists {
		var outcome string
		if json.Unmarshal(raw, &outcome) == nil {
			switch outcome {
			case "allow", "accepted":
				return "approved"
			case "deny", "denied":
				return "denied"
			}
		}
	}
	if raw, exists := value["action"]; exists {
		var action string
		if json.Unmarshal(raw, &action) == nil {
			switch action {
			case "accept":
				return "accepted"
			case "decline":
				return "denied"
			case "cancel":
				return "cancelled"
			}
		}
	}
	return "answered"
}
