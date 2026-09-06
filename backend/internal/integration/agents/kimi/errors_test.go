package kimi

import (
	"strings"
	"testing"
	"unicode/utf8"
)

func TestProcessDiagnosticKeepsFinalError(t *testing.T) {
	stderr := "Warning: stale catalog\nprogress\n\x1b[31merror: Authentication failed\x1b[0m\n\nmessage:\nPlease sign in again.\nSee log: /root/.kimi-code/logs/kimi-code.log\n"
	want := "error: Authentication failed\nmessage:\nPlease sign in again."
	if got := processDiagnostic(stderr); got != want {
		t.Fatalf("diagnostic = %q", got)
	}
}

func TestProcessDiagnosticBoundsUnicodeOutput(t *testing.T) {
	got := processDiagnostic("error: " + strings.Repeat("界", 3000))
	if !utf8.ValidString(got) || utf8.RuneCountInString(got) != 2003 || !strings.HasSuffix(got, "...") {
		t.Fatalf("invalid truncation: %d bytes", len(got))
	}
}
