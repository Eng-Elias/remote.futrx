# Kimi runtime review — 2026-09-06

Reviewed Remote at `7036dfdf` and the installed/pinned `@moonshot-ai/kimi-code`
0.40.1. Repository issue searches for `kimi` and `307` found feature requests
but no issue describing the reported 307 failure. PR #89 previously changed
Kimi's mode/effort advertisement; PR #122 reverted those changes.

## Findings and changes

| Finding | Evidence | Result |
| --- | --- | --- |
| Failure diagnostics were hidden | Kimi writes failures to stderr; shared `RunProcess` captures it, but `ProcessError.Error()` returns only the process exit error. Kimi returned that error directly to the prompt service. | The runner retains the last 64 KiB of stderr so long progress logs cannot hide the final error. Kimi emits one `run.failed` event with the exit error and bounded CLI diagnostics. ANSI colors, preceding progress, and the local log-file hint are removed. |
| Plan always failed in prompt mode | Running the pinned CLI with `-p ... --plan` exits 1 with `Cannot combine --prompt with --plan.` | Capabilities expose Default only. Previously saved unsupported modes are rejected before launch with a Default-mode hint. |
| Thinking selection was ignored | Discovery advertised efforts, but command construction never forwarded the selection. The CLI has no general prompt effort flag; its operational environment override depends on provider traits and existing thinking state. | Hide the ineffective per-run control and retain Kimi's configured default. No shared config is rewritten for individual chats. |
| Missing sessions could not recover | The pinned CLI exits 1 with `error: failed to run prompt: Session "<id>" not found.`; Kimi did not return Remote's recovery sentinel. | An exact lookup failure for the requested session, before assistant/tool output, returns `ErrSessionNotFound`. Remote's existing visible-history recovery handles it. Other failures are not automatically replayed. |
| A resume hint could report success before a failing exit | The parser treats `session.resume_hint` as completion before the process finishes. The CLI can emit a hint for a blocked/paused goal with a nonzero exit status. | Hold completion until successful exit. Failed/cancelled processes do not complete; clean exits without a completion record report an incomplete response. |

## The 307 report

The installed CLI was exercised against a loopback HTTP server with an isolated
home and a dummy API key, using the actual Remote provider adapter:

- A `307` with a valid relative `Location` was followed as a POST. Kimi returned
  the expected assistant response and exited 0.
- A `307` without `Location` exited 1 and wrote
  `error: failed to run prompt: provider.api_error: 307 status code (no body)`
  to stderr. Remote now preserves that diagnostic in the chat error.

An HTTP 307 response is distinct from the CLI's process exit status. These
tests reproduce one way to obtain the reported HTTP error; they do not identify
the endpoint or proxy responsible for the user's incident. Its exact error and
request context were unavailable during this review. No redirect destination,
base URL, credentials, or automatic retry policy was changed speculatively.

## Validation

Run from `backend/`:

```sh
go test -race ./internal/integration/agents/kimi ./internal/integration/agents/runtime ./internal/service/prompt
REMOTE_KIMI_SMOKE=1 go test -v ./internal/integration/agents/kimi -run TestInstalledCLIRedirects -count=1
```

The smoke check uses the installed CLI with a fresh home, a dummy key, and a
loopback provider. It requires no live provider account. Regression tests cover
CLI diagnostics after long progress logs, failed exits after a resume hint, successful completion,
cancellation, incomplete streams, unsupported modes, and missing-session
recovery boundaries.

The frontend production build, full backend suite, backend build, and focused
`go vet` checks also pass. The base branch contained stray `B N` tokens in
`backend/internal/service/auth/service_test.go:85`; removing that syntax typo
is isolated in commit `d70ee4ef` so the full backend suite can run.

## Remaining integration limits

- The prompt JSON stream supplies no usage or thinking deltas. Counts remain
  unknown; removing the Thinking selector does not disable model reasoning.
- Forked chats still start fresh. The installed CLI now advertises a `fork`
  command, but the Remote adapter does not use it.
- Per-run browser MCP is unavailable. Selected skills are supplied as
  instructions to read their paths.
- Authentication status checks for credential-file presence rather than
  validating the grant with the provider. Model configuration, token validity,
  endpoint availability, and account entitlement can still fail at runtime;
  the resulting diagnostics are now visible.
- Validation here covers the host adapter and existing container-command
  tests, not a deployed LXC run against a live Kimi account.
