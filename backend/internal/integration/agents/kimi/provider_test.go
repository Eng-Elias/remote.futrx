package kimi

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/futrx-com/remote.futrx.com/internal/agent"
)

func fakeCLI(t *testing.T, script string) {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "kimi"), []byte("#!/bin/sh\n"+script), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir)
}

func TestRunReportsCLIDiagnosticsWithoutCompleting(t *testing.T) {
	for _, tc := range []struct {
		name, script, message string
	}{
		{
			name: "HTTP redirect failure",
			script: `printf '%s\n' 'error: failed to run prompt: API request failed with status 307 Temporary Redirect' >&2
exit 1`,
			message: "307 Temporary Redirect",
		},
		{
			name: "failure after resume hint",
			script: `printf '%s\n' '{"role":"meta","type":"session.resume_hint","session_id":"s1"}'
printf '%s\n' 'error: goal blocked' >&2
exit 3`,
			message: "goal blocked",
		},
		{
			name:    "exit without diagnostics",
			script:  "exit 137",
			message: "exit status 137",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fakeCLI(t, tc.script)
			var events []agent.Event
			err := (&Provider{}).Run(context.Background(), agent.RunRequest{ConversationID: "c", Cwd: t.TempDir()}, func(ev agent.Event) {
				events = append(events, ev)
			})
			if !errors.Is(err, agent.ErrRunFailed) {
				t.Fatalf("error = %v, want ErrRunFailed", err)
			}
			failures := 0
			for _, ev := range events {
				if ev.Type == agent.EventRunCompleted {
					t.Fatal("failed run emitted completion")
				}
				if ev.Type == agent.EventRunFailed {
					failures++
					if !strings.Contains(ev.Message, tc.message) || !ev.IsError || ev.ConversationID != "c" || ev.Provider != agent.ProviderKimi {
						t.Fatalf("failure = %+v", ev)
					}
				}
			}
			if failures != 1 {
				t.Fatalf("failures = %d, want 1", failures)
			}
		})
	}
}

func TestRunRecoversOnlyMissingSessionBeforeOutput(t *testing.T) {
	for _, tc := range []struct {
		name, stdout, diagnostic string
		wantRecovery             bool
	}{
		{"missing session", "", `error: failed to run prompt: Session "missing" not found.`, true},
		{"different session", "", `error: failed to run prompt: Session "other" not found.`, false},
		{"wrong directory", "", `error: failed to run prompt: Session "missing" was created under a different directory.`, false},
		{"output already emitted", `{"role":"assistant","content":"Already ran a command"}`, `error: failed to run prompt: Session "missing" not found.`, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			// All fixture strings are fixed above and contain no single quotes.
			fakeCLI(t, "printf '%s\\n' '"+tc.stdout+"'\nprintf '%s\\n' '"+tc.diagnostic+"' >&2\nexit 1")
			failures := 0
			err := (&Provider{}).Run(context.Background(), agent.RunRequest{Cwd: t.TempDir(), ResumeID: "missing"}, func(ev agent.Event) {
				if ev.Type == agent.EventRunFailed {
					failures++
				}
			})
			if errors.Is(err, agent.ErrSessionNotFound) != tc.wantRecovery {
				t.Fatalf("error = %v, recovery = %t", err, tc.wantRecovery)
			}
			if tc.wantRecovery && failures != 0 {
				t.Fatal("recoverable failure was shown to the user")
			}
		})
	}
}

func TestRunCompletesSuccessfulTurn(t *testing.T) {
	fakeCLI(t, `printf '%s\n' '{"role":"assistant","content":"Done"}' '{"role":"meta","type":"session.resume_hint","session_id":"s1"}'`)
	var events []agent.Event
	err := (&Provider{}).Run(context.Background(), agent.RunRequest{Cwd: t.TempDir(), Model: "selected-model"}, func(ev agent.Event) {
		events = append(events, ev)
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 3 || events[0].Type != agent.EventAssistantTextDelta || events[1].Type != agent.EventSessionUpdated || events[2].Type != agent.EventRunCompleted {
		t.Fatalf("events = %+v", events)
	}
	usage, ok := agent.ParseUsage(events[2].Usage)
	if !ok || usage.Model != "selected-model" {
		t.Fatalf("usage = %+v", usage)
	}
}

func TestRunRejectsUnsupportedModeBeforeLaunching(t *testing.T) {
	t.Setenv("PATH", t.TempDir()) // No CLI can be spawned.
	err := (&Provider{}).Run(context.Background(), agent.RunRequest{Mode: agent.RunModePlan}, nil)
	if err == nil || !strings.Contains(err.Error(), "select Default mode") {
		t.Fatalf("error = %v", err)
	}
}

func TestRunRejectsTruncatedSuccessfulExit(t *testing.T) {
	fakeCLI(t, `printf '%s\n' '{"role":"assistant","content":"partial answer"}'`)
	err := (&Provider{}).Run(context.Background(), agent.RunRequest{Cwd: t.TempDir()}, nil)
	if err == nil || !strings.Contains(err.Error(), "without a completion record") {
		t.Fatalf("error = %v", err)
	}
}

func TestRunCancellationDoesNotComplete(t *testing.T) {
	fakeCLI(t, `printf '%s\n' '{"role":"assistant","content":"started"}'
while :; do :; done`)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	err := (&Provider{}).Run(ctx, agent.RunRequest{Cwd: t.TempDir()}, func(ev agent.Event) {
		if ev.Type == agent.EventAssistantTextDelta {
			cancel()
		}
		if ev.Type == agent.EventRunCompleted || ev.Type == agent.EventRunFailed {
			t.Errorf("cancellation emitted %s", ev.Type)
		}
	})
	if err != nil {
		t.Fatal(err)
	}
}
