// Command starmusician-gui is the Fyne GUI entry point for StarMusician. It
// wires nothing itself — see ui.Build — it just creates the Fyne app/window
// and runs it.
package main

import (
	"fyne.io/fyne/v2/app"

	"github.com/philomelch/StarMusician/ui"
)

func main() {
	a := app.New()
	w := a.NewWindow("StarMusician")
	ui.Build(w)
	w.ShowAndRun()
}
