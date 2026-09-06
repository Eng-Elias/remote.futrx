package kimi

import (
	"fmt"
	"regexp"
	"strings"
)

var ansiEscapeRE = regexp.MustCompile(`\x1b\[[0-9;]*m`)

// Kimi reports startup and turn failures on stderr, outside its JSON stream.
// Keep the final error and its continuation lines, excluding progress before
// it and the local log-file hint. Bound what is persisted in the transcript.
func processDiagnostic(stderr string) string {
	lines := strings.Split(ansiEscapeRE.ReplaceAllString(stderr, ""), "\n")
	start := 0
	for i, line := range lines {
		if strings.HasPrefix(strings.ToLower(strings.TrimSpace(line)), "error:") {
			start = i
		}
	}
	var diagnostic []string
	for _, line := range lines[start:] {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "See log:") {
			continue
		}
		diagnostic = append(diagnostic, line)
	}
	text := strings.Join(diagnostic, "\n")
	const maxRunes = 2000
	if runes := []rune(text); len(runes) > maxRunes {
		text = string(runes[:maxRunes]) + "..."
	}
	return text
}

func isMissingSession(diagnostic, sessionID string) bool {
	if sessionID == "" {
		return false
	}
	// Match the CLI's lookup failure for this exact session, not arbitrary
	// tool output containing "not found", which must never trigger a replay.
	want := fmt.Sprintf("error: failed to run prompt: Session %q not found.", sessionID)
	return diagnostic == want
}
