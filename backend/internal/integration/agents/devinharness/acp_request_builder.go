package devinharness

import (
	"encoding/json"
	"strings"

	"github.com/futrx-com/remote.futrx.com/internal/agent"
)

// acpRequestID enumerates the client-to-agent JSON-RPC requests in the order
// they are sent during one run. Numeric IDs keep the wire protocol compact and
// match the pattern used by codexharness.
type acpRequestID int

const (
	acpInitializeRequestID acpRequestID = iota + 1
	acpAuthenticateRequestID
	acpSessionRequestID
	acpPromptRequestID
)

// clientVersion is the version reported in the ACP initialize clientInfo. It
// identifies Remote as the ACP host without claiming Windsurf identity.
const clientVersion = "0.1.0"

// buildInitialize constructs the ACP initialize request. The client requests
// protocol version 2; the agent may respond with a lower version (confirmed
// v1 by live traffic — the harness accepts whatever the agent returns).
func buildInitialize() map[string]any {
	return map[string]any{
		"jsonrpc": "2.0",
		"id":      acpInitializeRequestID,
		"method":  "initialize",
		"params": acpInitializeParams{
			ProtocolVersion: 2,
			ClientCapabilities: acpClientCapabilities{
				Elicitation: &acpElicitationCapability{},
				FS: &acpFSCapability{
					ReadTextFile:  true,
					WriteTextFile: true,
				},
			},
			ClientInfo: acpClientInfo{
				Name:    "remote.futrx",
				Version: clientVersion,
			},
		},
	}
}

// buildInitialized constructs the notifications/initialized notification that
// must follow the initialize response.
func buildInitialized() map[string]any {
	return map[string]any{
		"jsonrpc": "2.0",
		"method":  "notifications/initialized",
	}
}

// buildAuthenticate constructs the authenticate request. The ACP server does
// not read on-disk credentials; the client must call authenticate with the
// methodId from the initialize response's authMethods (confirmed "devin-browser"
// by live traffic).
func buildAuthenticate(methodID string) map[string]any {
	return map[string]any{
		"jsonrpc": "2.0",
		"id":      acpAuthenticateRequestID,
		"method":  "authenticate",
		"params":  acpAuthenticateParams{MethodID: methodID},
	}
}

// buildSessionNew constructs the session/new request. mcpServers is required
// (can be empty); without it the server returns "missing field 'mcpServers'".
func buildSessionNew(req agent.RunRequest) map[string]any {
	return map[string]any{
		"jsonrpc": "2.0",
		"id":      acpSessionRequestID,
		"method":  "session/new",
		"params": acpSessionNewParams{
			Cwd:        strings.TrimSpace(req.Cwd),
			McpServers: []json.RawMessage{},
		},
	}
}

// buildSessionResume constructs the session/resume request for continuing an
// existing conversation.
func buildSessionResume(req agent.RunRequest) map[string]any {
	return map[string]any{
		"jsonrpc": "2.0",
		"id":      acpSessionRequestID,
		"method":  "session/resume",
		"params": acpSessionResumeParams{
			SessionID:  req.ResumeID,
			Cwd:        strings.TrimSpace(req.Cwd),
			McpServers: []json.RawMessage{},
		},
	}
}

// buildSessionPrompt constructs the session/prompt request. The prompt is a
// slice of ContentBlocks; for text input it is a single text block.
func buildSessionPrompt(sessionID, prompt string) map[string]any {
	return map[string]any{
		"jsonrpc": "2.0",
		"id":      acpPromptRequestID,
		"method":  "session/prompt",
		"params": acpSessionPromptParams{
			SessionID: sessionID,
			Prompt: []acpContentBlock{
				{Type: "text", Text: prompt},
			},
		},
	}
}

// buildSessionCancel constructs the session/cancel notification. It is a
// notification (no id, no response expected). The cancelled turn is confirmed
// by the session/prompt response arriving with stopReason "cancelled".
func buildSessionCancel(sessionID string) map[string]any {
	return map[string]any{
		"jsonrpc": "2.0",
		"method":  "session/cancel",
		"params":  acpSessionCancelParams{SessionID: sessionID},
	}
}
