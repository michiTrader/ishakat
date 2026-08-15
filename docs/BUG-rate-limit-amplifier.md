# Bug: the agentic loop amplifies denials into provider rate limits

**Status:** open · **Severity:** high — it takes the user's provider account
offline · **Fix:** Phase 2.5 step 26 (§21.9) · **Depends on:** nothing

This is a standalone report because step 26 ships before the rest of §21 and
should be reviewable without reading the whole contract.

## Symptom

Reported from a real Termux session. Ishakat asked for permission on roughly 34
of ~40 tool calls. The user approved them quickly, because that was the only way
to make progress. Shortly afterwards the provider rate-limited the account and
the session became unusable.

The user's own conclusion was that ishakat "asks too much". That is true and is
addressed elsewhere in §21 — but it is only the trigger. **The loop turns each
denial into additional provider traffic**, and that mechanism is what escalates
an annoyance into an outage.

## The chain, hop by hop

**1. A denial is returned as tool-result data.**
`internal/app/tools.go`, in `ToolRunnerWithGuard`:

```go
if err := guard.Authorize(ctx, name, args); err != nil {
    return engine.ToolResult{Text: err.Error(), IsError: true}, nil
}
```

The doc comment above it states the intent explicitly — *"the model receives the
reason and can choose a non-destructive alternative on its next turn"* — and
that intent is the bug. Returning `nil` as the error means "this tool ran and
produced a result", so nothing upstream knows a human said no.

**2. The loop iterates, and each iteration is a request.**
`internal/engine/agentloop.go` sees a normal tool result, appends it to the
history and goes around again. One denial therefore costs one *extra* provider
request carrying the entire, now larger, history.

**3. Loop detection does not catch the retry.**
The model does exactly what step 1's comment invites it to do: it tries a
variant. `ls` → `ls -la` → `find .` are three different attempts at the same
intent, and the guard rejects all three. Detection misses them because it
compares exact bytes, and only the batch's first call against the previous
iteration's last:

```go
if i == 0 && tc.name == lastToolName && bytesEqual(tc.args, lastToolArgs) { /* stop */ }
```

**4. Fast approval removes the only pacing there was.**
Each new variant raises a new dialog. A user who wants to get work done approves
in well under a second, and every approval immediately resumes the loop. Human
hesitation was the de-facto rate limiter, and it disappears exactly when the
dialogs become frequent enough to be annoying.

**5. Nothing reads `Retry-After` where it matters.**
`retryAfter` in `internal/engine/retry.go` covers **the handshake only** — never
mid-stream, never between loop iterations. `provider.Error.Retry()` already
returns `(wait, retryable)` parsed from the response. The hint is produced and
never consumed by the loop.

## Why "ask less" is not the fix

Reducing prompt frequency (§21.3–§21.5) makes the trigger rarer. It does not
change the fact that a single denial still costs an extra request, that variants
still evade detection, or that a 429 is still ignored inside the loop. **The
amplifier must be fixed on its own**, which is why step 26 is sequenced before
every other part of §21 and depends on none of them.

## Fixes, in order

Ordered deliberately. Pacing is last because a sleep that hides an amplification
defect is worse than no fix: it makes the defect harder to observe and it will
come back at scale.

| # | Fix | Where |
|---|---|---|
| 1 | A human denial **ends the turn** (`AgentResult.Stopped`), it is not tool data | `internal/app/tools.go`, `internal/engine/agentloop.go` |
| 2 | Honour `Retry-After` **inside** the loop, surfaced as `auto·wait 22s` | `agentloop.go`, using the existing `retry.go` / `provider.Error.Retry()` |
| 3 | Normalized loop detection across the whole batch, not byte-exact, not just `i == 0` | `agentloop.go`, reusing `internal/evolve/ledger.go`'s pattern normalizer |
| 4 | One dialog per batch, grouped by risk class | `agentloop.go` + `internal/ask` (needs §21.5, §21.10) |
| 5 | *(optional, last)* a floor of ~250 ms between iterations | `agentloop.go` |

Fixes 1–3 and 5 are step 26 and need nothing from the rest of §21. Fix 4 waits
for the risk classes.

The governing rule:

> **A denial is a decision, not a hint. When the human says no, the turn ends.
> Ishakat does not spend API calls looking for a way around a "no". If the user
> wants an alternative, saying "find another way" starts a new turn —
> explicitly, and at human speed.**

## Closing criteria

1. A turn in which the human denies one tool call issues **exactly one**
   provider request, not N. Asserted with a counting fake provider.
2. A 429 carrying `Retry-After: 22` waits ~22 s and then resumes, rather than
   retrying immediately or failing the turn.
3. `ls` → `ls -la` → `find .` is detected as one loop and stops the turn.
4. Regression coverage that fix 5's sleep is *not* what makes criteria 1–3 pass:
   they must hold with `min_interval_ms = 0`.
