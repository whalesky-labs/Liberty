package desktop

import (
	"context"
	"github.com/egoist/mygo"
	"liberty.local/liberty/internal/domain"
	"math"
	"time"
)

func (w *Workspace) AttachMetrics(sample func(context.Context) (domain.ProcessMetrics, error)) {
	ctx, cancel := context.WithCancel(context.Background())
	w.main.OnClosed(cancel)
	go func() {
		ticker := time.NewTicker(10 * time.Second)
		defer ticker.Stop()
		for {
			if w.main.IsVisible() && !w.main.IsMinimized() {
				limited, stop := context.WithTimeout(ctx, 3*time.Second)
				metrics, err := sample(limited)
				stop()
				mygo.RunOnMain(func() {
					if w.closed {
						return
					}
					if err == nil {
						w.metrics = &metrics
					} else {
						w.metrics = nil
					}
					bounds := w.main.ContentBounds()
					scale := max(1, mygo.Screen.DisplayMatching(bounds).ScaleFactor)
					w.graphicsMB = max(1, int(math.Round(float64(bounds.Width)*float64(bounds.Height)*4*scale*scale/(1024*1024))))
					w.main.Invalidate()
				})
			}
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
			}
		}
	}()
}
