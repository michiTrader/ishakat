package tui

import "time"

// msgs.go concentra TODOS los tea.Msg propios de este paquete (§6.2): si hay
// que tocar dos archivos para agregar un mensaje nuevo, el diseño está mal.

// streamTickMsg drena el StreamBuf del turno vivo (§7.3). Se re-emite solo
// mientras hay un turno en curso: en reposo no hay ningún ticker de fondo.
type streamTickMsg struct{}

// animTickMsg avanza un fotograma del spinner / degradado en movimiento.
// Corre a ui.animations.fps y, como streamTickMsg, solo se re-emite en
// ModeBusy: la app en reposo consume 0% de CPU (§14).
type animTickMsg struct{ t time.Time }

// quitConfirmMsg se dispara si el segundo ctrl+c no llega dentro de la
// ventana de gracia: cancela el estado de "un ctrl+c ya armado" (§7.4).
type quitConfirmMsg struct{}

// modelChosenMsg is the model picker's only output (§9.4/Step 10): the
// reference the user picked, or that /model resolved unambiguously without
// even opening the overlay. Routing the choice through a message instead of
// having the picker mutate Root directly keeps picker.go ignorant of
// anything but the catalog it was handed — the same separation slash.Kind
// already buys internal/slash from internal/tui.
type modelChosenMsg struct{ Ref string }

// There is deliberately no blink message here.
//
// There used to be one, re-armed every 500 ms from Init for the lifetime of the
// process, and it flipped a boolean that no renderer ever read. Two things were
// wrong with that. The smaller one is the dead field. The larger one is that
// §14 asks for zero CPU activity at idle, and a program that wakes up twice a
// second forever does not have it — on the target platform that is a phone
// battery paying for a variable nobody looks at.
//
// Nothing replaces it because nothing has to: input.go sets
// SetVirtualCursor(false), so the text cursor is the terminal's own, and every
// terminal blinks its own cursor without being asked. Drawing a blink ourselves
// would mean fighting the hardware for it.
//
// The rule this file now keeps: a message type may only exist here if a timer
// that emits it can be shown to stop. streamTickMsg stops when the turn ends,
// animTickMsg when the mode leaves ModeBusy, quitConfirmMsg after one shot.
