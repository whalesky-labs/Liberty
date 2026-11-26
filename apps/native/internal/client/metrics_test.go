package client

import (
	"context"
	"errors"
	"liberty.local/liberty/internal/domain"
	"testing"
	"time"
)

type metricReply struct {
	value domain.ProcessMetrics
	err   error
}

func TestMetricsControllerSerialSamplingVisibilityAndClose(t *testing.T) {
	queries := make(chan context.Context, 4)
	replies := make(chan metricReply, 4)
	callbacks := make(chan func(), 8)
	changes := 0
	controller := NewMetrics(func(ctx context.Context) (domain.ProcessMetrics, error) {
		queries <- ctx
		r := <-replies
		return r.value, r.err
	}, func(f func()) { callbacks <- f }, func() { changes++ })
	defer controller.Close()
	receive := func() {
		t.Helper()
		select {
		case <-queries:
		case <-time.After(time.Second):
			t.Fatal("sample not requested")
		}
	}
	complete := func(r metricReply) {
		t.Helper()
		replies <- r
		select {
		case f := <-callbacks:
			f()
		case <-time.After(time.Second):
			t.Fatal("sample not dispatched")
		}
	}
	controller.SetActive(true)
	receive()
	controller.Refresh()
	select {
	case <-queries:
		t.Fatal("overlapping sample")
	default:
	}
	complete(metricReply{value: domain.ProcessMetrics{MemoryBytes: 123, SampledAt: time.Now()}})
	if controller.Value == nil || controller.Value.MemoryBytes != 123 {
		t.Fatal("fresh sample not published")
	}
	controller.Refresh()
	receive()
	controller.Value.SampledAt = time.Now().Add(-4 * time.Second)
	controller.Refresh()
	if controller.Value != nil {
		t.Fatal("stalled sampler retained expired data")
	}
	complete(metricReply{err: errors.New("unavailable")})
	if controller.Value != nil {
		t.Fatal("failure retained stale data")
	}
	controller.Refresh()
	receive()
	controller.SetActive(false)
	complete(metricReply{value: domain.ProcessMetrics{MemoryBytes: 456}})
	if controller.Value != nil {
		t.Fatal("hidden window accepted late sample")
	}
	controller.Refresh()
	select {
	case <-queries:
		t.Fatal("hidden window sampled")
	default:
	}
	controller.SetActive(true)
	receive()
	beforeClose := changes
	controller.Close()
	complete(metricReply{value: domain.ProcessMetrics{MemoryBytes: 789}})
	if controller.Value != nil || changes != beforeClose {
		t.Fatal("closed window accepted callback")
	}
}
