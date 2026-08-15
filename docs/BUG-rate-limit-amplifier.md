# Bug: the agentic loop amplifies denials into provider rate limits

**Status:** fixed (fixes 1, 2, 3, 5; fix 4 deferred to steps 27–29 by design)
· **Severity:** high — it takes the user's provider account offline
· **Fix:** Phase 2.5 step 26 (§21.9) · **Depends on:** nothing

> **Outcome.** All four closing criteria pass. Two of the prescriptions below
> turned out to be wrong when checked against the running code, and the
> "What was actually built" section at the end records what replaced them
> and why. The diagnosis in this report was correct; two of its remedies
> were not.

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

## What was actually built

Each fix was verified against the running code before being written, and two
of the prescriptions above did not survive that check. Both are recorded here
rather than quietly corrected, because in both cases the report's *diagnosis*
was right and only its *remedy* was wrong.

| # | Status | Where it landed |
|---|---|---|
| 1 | done | `deniedHint` in `engine/types.go`, `refusal`/`deniedError` in `permissions/guard.go`, the branch in `agentloop.go`, the discrimination in `app/tools.go` |
| 2 | done, **diagnosis corrected** | `jitterUp` in `engine/retry.go`, `open`'s notify callback, `AgentOptions.OnWait`, `roundWait` in `app/agentturn.go` |
| 3 | done, **remedy replaced** | futility tracking in `agentloop.go` |
| 4 | deferred | needs the risk classes (steps 28–29); unchanged |
| 5 | done, off by default | `AgentOptions.MinInterval`, `tools.min_interval_ms` |

### Fix 2: the loop already honoured `Retry-After`

The report says the hint "is produced and never consumed by the loop". That
is not true. `RunAgentTurn` calls `Engine.open` on every iteration and `open`
owns the retry policy, so the wait was already applied between iterations —
measured with a throwaway probe before any code was written.

The real defects were narrower, and both were still amplifying:

- **The wait was undershot.** `retryAfter` applied ±20% jitter to *every*
  wait, including one the server specified, so `Retry-After: 22` came back at
  ~17.6 s — inside a window the provider had explicitly closed, which earns a
  second 429 on an account that is already limited. A wait the engine invents
  (backoff) is a guess and is still spread both ways; a wait the *server*
  named is now a floor, jittered upward only.
- **The wait was silent.** A legitimate 22-second pause is indistinguishable
  from a hang on a phone, and a user who believes it hung kills it and retries
  — more load on the limited account.

### Fix 3: the prescribed normalizer cannot do this job

The report says to reuse `internal/evolve/ledger.go`'s pattern normalizer.
Probed against this report's own criterion 3, it does not group those
commands at all: `shapeKey` keys on the first token plus the token count, so
`ls`, `ls -la` and `find .` produce three different keys. Implementing the
instruction literally would not have satisfied the criterion beside it.

The deeper problem is that *no* argument normalizer can. Detection must fire
on `ls` → `ls -la` but not on `grep foo` → `grep bar` (pinned by an existing
test), and those two are the same shape: one tool, different arguments.

What separates them is not the arguments but the **results**. A variant hunt
is a run of attempts that fail *identically* — the world is not responding to
the variation, so the next variant will not help either. Three consecutive
tool calls returning the same error text end the turn. The check sits where
every outcome converges, so no path bypasses it, and any success resets the
run. That keeps it from touching error-is-data (§12bis): a model working
through *different* failures is making progress and is left alone.

This also closes a hole the report did not mention — byte-exact detection
only ever compared a batch's first call, so an exactly repeated *multi-call*
batch escaped entirely.

### Criteria, as verified

| # | Criterion | How it is asserted |
|---|---|---|
| 1 | a denial costs one request | `TestRunAgentTurnRefusalEndsTheTurn`, plus both e2e paths counting real HTTP requests |
| 2 | a 429 waits then resumes | `TestRunAgentTurnWaitsOutARateLimitAndResumes` (scaled in time), `TestRetryAfterNeverRetriesEarlierThanTheServerSaid` (500 randomized draws) |
| 3 | `ls` → `ls -la` → `find .` stops | `TestRunAgentTurnVariantHuntStops` — 6 provider requests before, 3 after |
| 4 | 1–3 hold with `min_interval_ms = 0` | `TestClosingCriterion4`, and zero is the default |

Every test above was mutation-verified: the defect was reintroduced and the
test observed to fail. The overreach guards (`different failures keep going`,
`a success resets the run`) were verified the same way in the other
direction, so fix 3 cannot be widened without a test objecting.
