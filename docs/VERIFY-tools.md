# Verifying the tool layer end to end

This is the manual check that Step 16 actually works, from the outside, on a
real terminal. It exists because Step 16 once shipped with a green test suite
and a feature that could not create a file: every link had a unit test against
a fake on its own side of the seam, and nothing asserted that the links were
connected. The automated equivalent of this document now lives in
`internal/app/toolchain_e2e_test.go`; what you cannot automate is the part
where a human looks at an approval dialog and presses a key, which is exactly
what this page is for.

Read §"If nothing happens" before concluding the feature is broken — the most
common outcome is a model that cannot call tools, which looks identical to a
bug from the outside.

## 0. Preconditions

You need a model that supports tool calling. This is not optional and it is
the single most common reason the check appears to fail: a model with no
tool-calling support is sent no tools, so it answers by *explaining* the shell
command you could run instead. That output is indistinguishable from a broken
tool layer, and it is the trap the original bug report fell into.

Ask the catalog rather than guessing:

```sh
ishakat models --json | grep -i '"tools"'      # models that can call tools
ishakat models                                  # look for the `tools` tag
```

If your current model is not in that list, pick one that is (`/model` inside
the interface, or `-m <ref>`). As of this writing `gemini-3.1-flash-lite` — the
model in the original report — is a poor choice for this check.

## 1. Configuration

The defaults already do the right thing: `enabled = true`, `read = "allow"`,
`write = "ask"`, `max_calls_per_turn = 25`, `max_output_bytes = 32768`. A
minimal `[tools]` block is enough, and you do **not** need to set the caps by
hand:

```toml
[tools]
enabled = true

[tools.permissions]
read  = "allow"
write = "ask"          # "ask" is the point of this exercise: it opens the dialog
shell = "ask"
allow_session = true
```

Note the table name if you are writing a provider by hand: it is
`[[provider]]`, singular, not `[[providers]]`. A plural key parses as valid
TOML that nothing reads, so you get "no provider is enabled yet" while staring
at a provider block that looks correct.

Confirm the config was actually read, rather than assuming:

```sh
ishakat doctor
```

## 2. The check

In the interactive interface, with a tool-capable model selected:

```
Crea un archivo llamado step16-approval.txt con el texto: Step 16 approval works.
```

Expected, in this order:

1. An **approval dialog** opens, naming `write_file` and showing the path and
   content. Because `write_file` is a medium-tier tool and `allow_session =
   true`, it offers three rows: allow once, allow for the session, deny.
2. You approve.
3. The transcript shows a **tool activity line** naming `write_file` and its
   target. This line is why "wrote the file" and "explained how to write the
   file" are no longer indistinguishable on screen.
4. The model answers that the file was created.
5. `ls` shows the file, and its content is `Step 16 approval works.`

The last step is the only one that counts. Steps 1–4 are the interface
reporting success; step 5 is success.

## 3. Also worth checking

- **Deny.** Same prompt, press deny. No file appears, the turn still ends
  normally, and the model answers acknowledging it was not allowed — a denial
  is data the model reads, not an error that kills the turn.
- **Read needs no dialog.** Ask it to read a file. With `read = "allow"` no
  dialog should open at all; a tool layer that asked about every read would be
  unusable.
- **Session grant.** Approve "for the session", then ask for a second write.
  The second one should not ask again. Then confirm a `bash` request *does*
  still ask: a high-tier tool never receives a session grant, and that
  carve-out is in code precisely so it cannot be configured away.

## 4. If nothing happens

Symptom: the model replies with prose describing an `echo … > file` command,
no dialog opens, and no file appears.

That is one failure, not three, and the dialog is almost certainly innocent.
The dialog cannot open unless a tool was called; a tool cannot be called
unless the model was offered one; the model is offered nothing unless the
`tools` array was serialized onto the request. So work backwards along that
chain rather than starting at the UI:

1. **Is the model tool-capable?** See §0. This is the usual answer.
2. **Did ishakat warn you?** A tool-enabled config pointed at a model the
   catalog says cannot call tools prints a warning on startup instead of
   failing silently. Check stderr before the interface drew.
3. **Is `enabled = true` in the config that was actually loaded?** `ishakat
   doctor` reports the path; a project-level file can override a user-level
   one.
4. **Does headless behave the same?** `ishakat -p "…"` runs the same engine.
   If headless calls tools and the interface does not, the fault is in the
   wiring between them, which is the shape of the original bug: both engine
   builders in `internal/app/engine.go` hard-coded `provider.Caps{}`, so the
   OpenAI dialect stripped the `tools` array from every interactive request
   while headless — which passed `Caps{Tools: true}` explicitly — worked fine.

`internal/app/caps.go` is now the single place that decides this, and
`internal/app/caps_test.go` asserts on the serialized request body, so a
regression fails there rather than in a user's terminal.
