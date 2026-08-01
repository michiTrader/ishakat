# ishakat

CLI de terminal para conversar con modelos de IA. Go + Bubble Tea v2.
Objetivo central: cambio de modelo en caliente sin perder contexto, y que
funcione impecable en Termux (40 columnas, un solo binario, sin runtime).

**Antes de tocar código, lee `docs/PLAN.md` completo.** Es la fuente única de
verdad: contratos, esquema de configuración, wireframes, fases y el orden
exacto de implementación con criterios de cierre.

Reglas: un paso a la vez, en orden. No agregues dependencias sin justificarlas
contra el presupuesto de la §6.4. Respeta las listas de "fuera de alcance".
Actualiza la bitácora de la §17 al cerrar cada paso.

## Language policy

**EVERYTHING new is written in English. No exceptions, no Spanish anywhere
in new work.** This covers, without exception: code comments (including doc
comments / godoc), identifiers (variables, functions, types, constants,
packages, file names), commit messages, PR titles/descriptions, CLI
user-facing strings and error messages, test names and test table
descriptions, and any new or extended documentation (including new sections
added to `docs/PLAN.md` and new entries in the §17 changelog).

Before writing a single line, re-read this rule. If a sentence, comment, or
identifier you are about to write would naturally come out in Spanish,
translate it to English before it goes in the file — never write it in
Spanish "to fix later".

Existing Spanish content (this file's older sections, `docs/PLAN.md`'s body
written before this policy, and code written before this policy) stays as-is
for now — it will be migrated later in a dedicated pass. Do not translate old
files as a side effect of an unrelated change; only write new content in
English. If you must touch a few lines inside an otherwise-Spanish file, it
is fine for the file to end up mixed language until the full migration
happens — but the lines *you* add or edit must be English.

## Commit policy — MANDATORY, no exceptions

**Commit after every file you create or modify. Never leave uncommitted work
in the tree for more than one file/tool-call.** The sandbox can reset or be
lost at any time; anything not committed (and pushed — see below) is gone
for good, and this has already happened once on this project. Concretely:

1. After every `Write`/`Edit`/`MultiEdit` call (or a very small, tightly
   coupled group of files that form one atomic change — e.g. a source file
   and the fixture it depends on), immediately run `git add` + `git commit`
   with a descriptive, English, conventional-commit-style message.
2. Do **not** batch an entire feature/step into one big commit at the end.
   If something goes wrong mid-task, the last good commit is the only thing
   that survives.
3. **Push to the remote branch regularly during the session, not only at the
   very end.** A commit that only exists in the local sandbox working copy
   is exactly as fragile as an uncommitted change — push after every commit,
   or at the very least after every small batch of commits, so the remote
   branch is always close to current.
4. Open the pull request early (as soon as there is a first meaningful
   commit) and keep updating it — don't wait until the whole step is done to
   open it.
5. Before ending a turn/session, double check `git status` is clean and
   `git log` vs `git log origin/<branch>` shows nothing unpushed.
