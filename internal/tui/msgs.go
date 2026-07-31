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

// echoDoneMsg es el maniquí del Paso 3: como no hay engine todavía, el input
// hace eco de lo escrito después de un retraso corto para poder ver el
// streaming simulado y las transiciones de modo sin red real.
type echoDoneMsg struct{ text string }

// quitConfirmMsg se dispara si el segundo ctrl+c no llega dentro de la
// ventana de gracia: cancela el estado de "un ctrl+c ya armado" (§7.4).
type quitConfirmMsg struct{}

// blinkMsg alterna la visibilidad del cursor de texto cuando no hay streaming
// vivo, para que el input parpadee sin necesitar el reloj de animaciones.
type blinkMsg struct{}
