# Design · The sub-agent surface: "extensions", or stay tool-shaped?

**Status: design only. Nothing here is implemented; nothing here changes any
existing contract.** Per `docs/ROADMAP-ux-2026-08-20.md`'s own text for W6:

> **F6** sub-agents: audit what `internal/tools/dispatch.go` already does
> against §21.11's display promises, and decide whether the user-facing surface
> is "extensions" or stays tool-shaped. This is a design question, so it gets a
> companion document rather than a wave item.

This document is that audit and that decision. It is deliberately *not* a wave
item: it changes no code, and its own recommendation (§4) is to change none in
the near term either.

Companion reading: `docs/PLAN.md` §21.11 (the sub-agent design and its own
ASCII mockup), §19.1 (the eight core tools, `dispatch` being the eighth), §3
(the CERRADA architecture decision: "Sub-agents (dispatch, Step 22) are
goroutines with isolated context, not a scheduler").

---

## 1. What §21.11 promised, verbatim

`docs/PLAN.md` §21.11, "Sub-agents: transparent, goal-stated, never widening",
makes four concrete promises:

1. **Deployment point is fixed:** "Sub-agents are deployed at a turn boundary,
   before executing a step — never while the model is mid-response. Fan-out
   needs a plan to fan out from." This is stated as happening at the `plan →
   exec` transition of §21.1.
2. **The swarm rule:** "fan out to read, single-file to write." Read-only
   exploration runs in parallel because nothing collides; anything that
   writes runs serially, one agent; verification runs after.
3. **Every sub-agent states its goal in one sentence.** "If the goal cannot be
   written in one sentence, the sub-agent should not exist."
4. **A visible swarm display**, illustrated with a mockup showing multiple
   concurrently-running named agents (`scout-loop`, `scout-tuning`,
   `bench-base`), each with a model, an elapsed time, a one-line goal, and a
   final constraint line ("no browser · no network") showing the mission
   restriction inherited from the parent.

## 2. What `internal/tools/dispatch.go` and its callers actually do

Read in full for this audit: `internal/tools/dispatch.go`,
`internal/app/dispatch.go`, the tool-call execution loop in
`internal/engine/agentloop.go` (lines ~560-750), and every render site that
mentions `dispatch` in `internal/tui` (`toolactivity.go`, `footer.go`).

**1. Deployment point: not implemented, and cannot be, without a planner.**
`dispatch` is one ordinary tool call among eight (§19.1). The reactive loop in
`agentloop.go`'s `runAgentTurn` has no concept of a "turn boundary" separate
from "the model asked for tools this iteration" — there is no `plan → exec`
phase machine anywhere in `internal/engine` or `internal/app` for `dispatch`
to gate itself on. `docs/PLAN.md`'s own §21.1 phase concept (`auto·exec`
rendered in the status line) exists for permission/autonomy display only
(§21.5's risk classes), not for constraining *when* a tool may be called. A
model can call `dispatch` mid-response, in the middle of an unrelated batch of
tool calls, exactly like any other tool. **The promise is aspirational, not
built.**

**2. The swarm rule ("fan out to read, single-file to write"): not
implemented — there is no fan-out at all.** The tool-call execution loop
(`agentloop.go` lines 574-747) is a plain `for i, tc := range toolCalls`
— every call in a batch, `dispatch` included, runs synchronously, one after
another, on the engine's own goroutine. There is no `sync.WaitGroup`, no
worker pool, no per-call goroutine anywhere in this loop (confirmed by
`grep`: the only goroutine-related comments in the file are about the
*outer* `RunAgentTurn` running on its own goroutine relative to its caller,
not about parallel tool execution within one turn). `internal/app/dispatch.go`
comment explicitly names why: `internal/app/dispatch.go:1-9`, cross-referencing
`internal/tools/dispatch.go`'s own header, states plainly that a sub-agent's
turn runs via `newSubAgentRunner`'s closure, itself invoked once per
`dispatch` tool call, sequentially, from within the same loop as every other
tool. A model that emits three `dispatch` calls in one batch (which the
provider dialect *can* express — see the parallel-call test in
`internal/engine/agentloop_test.go` around line 452, which is real and
general-purpose, but nothing about `dispatch` specifically triggers a
different code path) gets three **serial** sub-agent runs, one full
`RunAgentTurn` after another, not three concurrent ones. **There is no
concurrency to distinguish "read" from "write" sub-agents by**, so the
promise's entire premise (parallel reads, serial writes) has no mechanism to
attach to.

**3. Goal-in-one-sentence: implemented, and enforced only by convention.**
`dispatchArgs{Task string}` (`internal/tools/dispatch.go`) is the tool's only
argument — genuinely "one instruction, nothing else," matching the promise's
letter. `internal/tui/toolactivity.go`'s `toolTarget` renders that task
(truncated to 60 runes) next to the `dispatch` call in the transcript summary,
so the goal *is* visible to the user exactly as §21.11 asks. But nothing
enforces "one sentence" — the parameter schema's `Description` field asks the
model nicely ("must be self-contained"), and a model that writes three
paragraphs into `task` gets them silently truncated to 60 runes by
`toolTarget`, not refused. This part of the promise is **honestly delivered
for the common case, with no hard enforcement** — the closest thing this
document found to a gap worth flagging on its own, though a low-priority one:
the failure mode is a truncated summary line, not a broken sub-agent.

**4. The visible swarm display: not implemented, because there is nothing
concurrent to display.** `internal/tui/footer.go` line 43 says so directly, in
a comment already in the codebase before this audit: "full §21.11 fan-out
display remain unbuilt, so there is no real [swarm status line]." What *is*
implemented is `toolactivity.go`'s one-line-per-call summary (name, target,
success/failure mark) plus, specifically for `dispatch`, an extra line showing
the mission constraint inherited from the parent's `permissions.Guard`
(`missionConstraintLine`, wired since the 2026-08-16/17 mission work, §21.6).
That inherited-constraint line is a real, tested, working piece of the
mockup's own "no browser · no network" text — but it renders after a
**single, already-completed** `dispatch` call in the ordinary post-turn
transcript, never as a live, multi-agent, in-progress status block the way
the mockup draws it (spinner glyphs, per-agent elapsed time, several agents
visible at once while still running).

**What the recursion cap does deliver, exactly as promised:** `internal/app/
dispatch.go`'s `newSubAgentRunner` builds the sub-agent's own `AgentOptions`
via `buildAgentOptions(..., nil, ...)` — the trailing `nil` is the inner
`dispatchRunner` argument, which is what caps recursion at exactly one level:
a sub-agent's own tool registry never contains a `dispatch` entry for it to
call, so it cannot itself spawn a further sub-agent. `internal/tools/
dispatch.go`'s own header comment states this as a closed architecture
decision (§3, "not a scheduler"), and this audit confirms the code matches
that decision precisely — no gap here.

**What mission inheritance delivers, exactly as promised:** a sub-agent
receives the exact same `*permissions.Guard` pointer as its parent
(`newSubAgentRunner`'s `guard` parameter, passed straight into the inner
`buildAgentOptions` call), so any `MissionRule`s the parent's Guard carries
(§21.6's "no Playwright" compiled rules) apply identically inside the
sub-agent — enforced in Go, not by asking the sub-agent's own model nicely.
`docs/PLAN.md`'s own §21.11 text calls this "the mission constraint of §21.6
being visibly enforced on the children," and this audit confirms both halves:
enforced (real, tested — `TestGuardMissionRuleInheritedBySubAgentGuard`) and
visible (the `missionConstraintLine` render described above). **No gap here
either** — this is the one part of the whole promise that is fully built,
end to end, code and display together.

### Summary table

| §21.11 promise | Status |
|---|---|
| Deployed only at a `plan → exec` turn boundary | **Not built.** No phase machine exists to gate on; `dispatch` is an ordinary tool a model can call anytime. |
| Fan out to read, serial for write | **Not built.** No concurrency exists in the tool-call loop at all; every `dispatch` call, of any kind, runs serially. |
| One-sentence goal, visible to the user | **Built**, enforced by convention + truncation, not refusal. |
| Live multi-agent swarm status display | **Not built.** Only a post-hoc, single-call summary line exists. |
| Cannot request a capability the parent lacks | **Built** (mission rules inherited by Guard pointer identity, rendered on the transcript line). |
| Recursion capped at one level | **Built** (`nil` inner `dispatchRunner`). |

**The honest one-line finding:** *two of six promises are real and tested;
one is a partial, convention-only version of what was promised; three
describe a concurrent, phase-aware, live-rendering sub-agent system that does
not exist in any form — not stubbed, not partially wired, simply not
attempted, because building it requires a concurrent multi-sub-agent engine
mechanism `docs/PLAN.md`'s own §11 Step 31 entry already names as the actual
blocker (see that section's own "⬜ Still missing: explicit `/plan` and the
full §21.11 parallel fan-out/swarm display — the engine has no concurrent
multi-sub-agent concept yet — only one serial `dispatch` call per turn").*
This is not new information this audit discovered — the codebase already
says so, in two places — but F6 asked for it to be checked and stated in one
place rather than left implicit across a comment and a Bitácora entry.

## 3. The naming question: "extensions", or stay tool-shaped?

F6's second half, quoting the roadmap: "decide whether the user-facing surface
is 'extensions' or stays tool-shaped."

**What "tool-shaped" means today, concretely:** `dispatch` has no dedicated
UI. It is not a slash command (`grep` across `internal/slash/slash.go` and
every `internal/tui/*cmd.go` file confirms no `/dispatch`, `/agent`,
`/subagent` or similar command exists), not a settings panel entry, not a
picker. The only user-facing surface is: (a) the model decides to call it,
exactly like `bash` or `write_file`; (b) the transcript summary line
(`toolactivity.go`) shows it happened, with its goal and any inherited
mission constraint; (c) `internal/tui/toolscmd.go`'s `/tools` command lists
it in the same table as every other core tool, alongside its own
danger/state/use-count. A user who wants a sub-agent asks the model in
ordinary prose ("go check every TODO in the repo") and the model may or may
not decide `dispatch` is the right tool for that — the same relationship the
user has with `bash` or `fetch`. There is no concept the user manipulates
directly called an "agent" or an "extension"; there is a tool named
`dispatch`, exactly one of eight, that happens to run a second, isolated
model turn underneath.

**What renaming the surface to "extensions" would actually have to mean,**
if taken as more than cosmetic: a real "extensions" surface — in the sense
Pi's own `agents/*.md` frontmatter (§21.2's own research: `tools: read, grep,
find, ls`, `model: claude-haiku-4-5`) or Claude Code's sub-agent files use —
implies **user-authored, named, reusable, pre-configured** sub-agent
definitions the user can list, edit, and invoke by name (`/agent scout-loop`,
or a picker), each with its own declared tool subset and model. That is a
materially different feature from what `dispatch` is today: a single,
anonymous, task-string-only tool the *model* decides to call with no
user-visible identity beyond the transcript line rendered after the fact.
Renaming the tool's *label* to "extension" without building that authoring/
naming/reuse layer would be worse than leaving it alone — it would promise a
concept (a named, reusable, user-configurable thing) the implementation does
not deliver, the exact "pending item marked as done" failure mode
`docs/ROADMAP-ux-2026-08-20.md` §0 itself warns against for this whole
report.

**Conversely, nothing about staying "tool-shaped" blocks ever building that
richer layer later.** `dispatch`'s current shape (`SubAgentRunner` injected as
a plain function value, `AgentOptions` rebuilt fresh per call in
`newSubAgentRunner`) already has the exact seam a future named-sub-agent
config would plug into: `buildAgentOptions`'s `cfgTools`/`caps` parameters are
already how a sub-agent's tool subset is decided, just uniformly today (every
sub-agent gets the same subset as the parent, minus `dispatch` itself) rather
than per-named-agent. Nothing in this audit found an architectural obstacle to
adding named, pre-configured sub-agent profiles as a later, separate
increment — it would extend this seam, not replace it.

## 4. Recommendation

**Keep the surface tool-shaped. Do not rename `dispatch` to "extension" or
introduce "extensions" as a user-facing noun at this time.**

Reasons, in order of weight:

1. **Renaming without rebuilding is dishonest labeling.** §3 above shows the
   gap between what "extensions" implies (named, authored, reusable,
   user-configurable) and what exists (one anonymous tool, model-invoked,
   task-string-only). A label change with no capability change is the
   "pending item marked as done" anti-pattern the roadmap's own opening
   section warns against — it would make the surface *look* more finished
   while making it *no more capable*, which is a worse outcome than leaving
   the honest, narrower name in place.
2. **The concurrent multi-sub-agent engine is the real blocker, and it does
   not exist.** Three of §21.11's six promises (turn-boundary gating,
   parallel fan-out, live swarm display) all require the same missing
   mechanism `docs/PLAN.md` §11's own Step 31 entry already names as blocked.
   A naming decision cannot substitute for that engine work, and making the
   naming decision now, before that engine exists, risks choosing a name
   that fits a system not yet built rather than the one that is.
3. **"Tool-shaped" is not a downgrade — it is the honest description of a
   real, working, well-tested feature.** Table §2 shows two of six promises
   fully delivered (recursion cap, mission inheritance) and correctly
   described by the current "one core tool among eight" framing. `dispatch`
   is not broken or half-built in the sense of shipping something unreliable
   — it reliably does exactly what a tool-shaped `dispatch` should do. The
   gap is between what §21.11's *mockup* promised (a richer, concurrent,
   visibly-orchestrated system) and what shipped (the tool-shaped subset that
   was actually buildable without the missing engine mechanism), not between
   "tool" and "broken."
4. **No user-facing harm from waiting.** Nothing in the 2026-08-20 UX report
   (`docs/ROADMAP-ux-2026-08-20.md` §1) that generated F6 names a concrete
   user complaint about `dispatch`'s current naming or shape — F6's own
   wording is "decide whether," not "users are confused by the current
   name." There is no urgency pushing this decision one way, which weighs
   further against making an irreversible naming change now.

**What would change this recommendation:** if/when the concurrent
multi-sub-agent engine (Step 31's blocker) is actually built — giving
`dispatch` real parallel fan-out, a phase-aware deployment gate, and a live
swarm display — that is the point to revisit this question, because at that
point the feature really would have grown past "one tool among eight" into
something with its own identity worth a dedicated noun. Building the engine
first and naming the result afterward is the correct order; naming first and
hoping the engine catches up is not.

## 5. What this document does not decide

This document takes no position on:

- Whether the concurrent multi-sub-agent engine (Step 31's blocker) should
  ever be built, or when. That is a separate, larger design question outside
  F6's scope, already tracked as a known blocker in `docs/PLAN.md` §11.
- Whether `dispatch`'s single-sentence-goal convention should gain hard
  validation (reject a multi-paragraph `task` outright) rather than silent
  truncation. Flagged in §2 as a minor, low-priority gap; not decided here
  because it is a small, independent implementation choice that does not
  depend on the naming question this document was asked to resolve.
- Any change to `internal/tools/dispatch.go`, `internal/app/dispatch.go`, or
  any TUI rendering file. Per this document's own opening line and the
  roadmap's own framing of F6 as a design question rather than a wave item,
  none of the above changes any existing contract or code path.
