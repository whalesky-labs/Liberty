package client

import (
	"context"
	"errors"
	"testing"

	"liberty.local/liberty/internal/application"
)

type deviceStorageReply struct {
	report application.DeviceStorageReport
	err    error
}

type deviceStorageCall struct{ reply chan deviceStorageReply }

type deviceStorageService struct{ calls chan deviceStorageCall }

func (s deviceStorageService) Usage(context.Context) (application.DeviceStorageReport, error) {
	call := deviceStorageCall{reply: make(chan deviceStorageReply, 1)}
	s.calls <- call
	reply := <-call.reply
	return reply.report, reply.err
}

func TestDeviceStorageControllerLoadsRefreshesAndCloses(t *testing.T) {
	service := deviceStorageService{calls: make(chan deviceStorageCall, 3)}
	callbacks := make(chan func(), 4)
	controller := NewDeviceStorage(service, func(callback func()) { callbacks <- callback }, func() {})
	defer controller.Close()

	controller.EnsureLoaded()
	first := receive(t, service.calls)
	first.reply <- deviceStorageReply{report: application.DeviceStorageReport{Total: 42}}
	receive(t, callbacks)()
	if !controller.Loaded || controller.Busy || controller.Error != nil || controller.Report == nil || controller.Report.Total != 42 {
		t.Fatal("storage report was not published")
	}
	controller.EnsureLoaded()
	if len(service.calls) != 0 {
		t.Fatal("loaded storage report was measured again without a refresh")
	}

	controller.Refresh()
	second := receive(t, service.calls)
	second.reply <- deviceStorageReply{err: errors.New("synthetic filesystem failure")}
	receive(t, callbacks)()
	if !controller.Loaded || controller.Busy || controller.Error == nil || controller.Report.Total != 42 {
		t.Fatal("a failed refresh discarded the last valid storage report")
	}

	controller.Refresh()
	third := receive(t, service.calls)
	controller.Close()
	if third.reply == nil {
		t.Fatal("storage query did not start")
	}
	third.reply <- deviceStorageReply{report: application.DeviceStorageReport{Total: 80}}
	if callback := receive(t, callbacks); callback != nil {
		callback()
	}
	if controller.Report.Total != 42 {
		t.Fatal("closed storage controller accepted a late report")
	}
}
