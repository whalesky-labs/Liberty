package desktop

import (
	"context"
	"github.com/egoist/mygo"
	"liberty.local/liberty/internal/application"
	"liberty.local/liberty/internal/client"
	"liberty.local/liberty/internal/domain"
	"time"
)

type resultCapabilityService struct{ grant *application.JobWindow }

func (s resultCapabilityService) Capabilities(ctx context.Context) (domain.RemoteCapabilities, error) {
	return s.grant.RemoteCapabilities(ctx)
}

// The result window keeps a source-bound grant and its own cancellation. Main
// mode changes cannot replace this service with the local/current-mode reader.
func startResultCapabilities(grant *application.JobWindow, view *ResultView, window *mygo.Window) func() {
	var controller *client.RemoteController
	controller = client.NewRemote(resultCapabilityService{grant}, mygo.RunOnMain, func() {
		view.RemoteCapabilities = controller.Capabilities
		window.Invalidate()
	})
	controller.Check()
	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		ticker := time.NewTicker(10 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				mygo.RunOnMain(controller.Refresh)
			}
		}
	}()
	return func() { cancel(); controller.Close() }
}
