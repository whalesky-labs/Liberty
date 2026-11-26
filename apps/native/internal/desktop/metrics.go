package desktop

import (
	"context"

	"github.com/egoist/mygo"
	"liberty.local/liberty/internal/client"
	"liberty.local/liberty/internal/domain"
)

func (w *Workspace) AttachMetrics(sample func(context.Context) (domain.ProcessMetrics, error)) {
	controller := client.NewMetrics(sample, mygo.RunOnMain, func() {
		if !w.closed {
			w.main.Invalidate()
		}
	})
	w.metrics = controller
	w.main.OnClosed(controller.Close)
	w.main.OnHide(func() { controller.SetActive(false) })
	w.main.OnMinimize(func() { controller.SetActive(false) })
	w.main.OnShow(func() { controller.SetActive(true) })
	w.main.OnRestore(func() { controller.SetActive(true) })
	controller.Start()
	controller.SetActive(w.main.IsVisible() && !w.main.IsMinimized())
}
