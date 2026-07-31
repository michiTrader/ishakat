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

**All new work is in English, no exceptions.** Code comments, identifiers
(variables, functions, types, packages), commit messages, CLI user-facing
strings, and any new documentation must be written in English from now on.

Existing Spanish content (this file, `docs/PLAN.md`, and code written before
this policy) stays as-is for now — it will be migrated later. Do not
translate old files as a side effect of an unrelated change; only write new
content in English. If you must touch a few lines inside an otherwise-Spanish
file, it is fine for the file to end up mixed language until the full
migration happens.

## Commit policy

**Commit after every file you create or modify — do not batch a whole
feature into one uncommitted working tree.** Work is lost if the sandbox
resets before a commit happens. Practically: after `Write`/`Edit` on a file
(or a small tightly-coupled group of files for one atomic fix), immediately
`git add` + `git commit` with a descriptive message before moving to the next
file. Push and open/update the PR regularly as well, not only at the very
end of the session.
