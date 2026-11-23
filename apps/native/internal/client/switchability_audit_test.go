package client

import (
	"context"
	"errors"
	"testing"

	"liberty.local/liberty/internal/domain"
)

type switchabilityCapabilityReply struct {
	capabilities domain.RemoteCapabilities
	err          error
}

type switchabilityCapabilityService struct {
	replies chan switchabilityCapabilityReply
}

func (service *switchabilityCapabilityService) Capabilities(context.Context) (domain.RemoteCapabilities, error) {
	reply := <-service.replies
	return reply.capabilities, reply.err
}

func TestRemoteSwitchabilityAuditRecoversVisibleCapabilityState(t *testing.T) {
	service := &switchabilityCapabilityService{replies: make(chan switchabilityCapabilityReply, 2)}
	dispatches := make(chan func(), 2)
	changes := 0
	controller := NewRemote(service, func(callback func()) { dispatches <- callback }, func() { changes++ })
	defer controller.Close()

	controller.Check()
	if !controller.Checking || changes != 1 {
		t.Fatalf("remote check did not expose its in-flight state: checking=%v changes=%d", controller.Checking, changes)
	}
	service.replies <- switchabilityCapabilityReply{err: errors.New("synthetic remote outage")}
	(<-dispatches)()
	if controller.Checking || controller.Error == nil || controller.Capabilities != nil || changes != 2 {
		t.Fatalf("failed remote check did not expose an unavailable state: checking=%v error=%v capabilities=%v changes=%d", controller.Checking, controller.Error, controller.Capabilities, changes)
	}

	controller.Check()
	if !controller.Checking || changes != 3 {
		t.Fatalf("remote retry did not expose a new in-flight state: checking=%v changes=%d", controller.Checking, changes)
	}
	service.replies <- switchabilityCapabilityReply{capabilities: domain.RemoteCapabilities{ServiceVersion: "audit"}}
	(<-dispatches)()
	if controller.Checking || controller.Error != nil || controller.Capabilities == nil || controller.Capabilities.ServiceVersion != "audit" || changes != 4 {
		t.Fatalf("successful remote retry did not restore the visible ready state: checking=%v error=%v capabilities=%+v changes=%d", controller.Checking, controller.Error, controller.Capabilities, changes)
	}
}

type switchabilityDashboardService struct {
	overviews chan switchabilityDashboardReply
}

type switchabilityDashboardReply struct {
	overview domain.DashboardOverview
	err      error
}

func (service *switchabilityDashboardService) Overview(context.Context, string) (domain.DashboardOverview, error) {
	reply := <-service.overviews
	return reply.overview, reply.err
}

func TestDashboardSwitchabilityAuditKeepsSnapshotVisibleAcrossRefreshFailure(t *testing.T) {
	service := &switchabilityDashboardService{overviews: make(chan switchabilityDashboardReply, 3)}
	dispatches := make(chan func(), 3)
	controller := NewDashboard(service, func(callback func()) { dispatches <- callback }, func() {})
	defer controller.Close()

	controller.Refresh("7d")
	service.overviews <- switchabilityDashboardReply{overview: domain.DashboardOverview{Source: "local", Range: "7d"}}
	(<-dispatches)()
	if controller.Overview == nil || controller.Overview.Source != "local" || controller.Error != nil || controller.Loading {
		t.Fatalf("initial dashboard snapshot did not become visible: overview=%+v error=%v loading=%v", controller.Overview, controller.Error, controller.Loading)
	}

	controller.Refresh("7d")
	service.overviews <- switchabilityDashboardReply{err: errors.New("synthetic dashboard outage")}
	(<-dispatches)()
	if controller.Overview == nil || controller.Overview.Source != "local" || controller.Error == nil || controller.Loading {
		t.Fatalf("failed dashboard refresh lost the last visible snapshot: overview=%+v error=%v loading=%v", controller.Overview, controller.Error, controller.Loading)
	}

	controller.Refresh("7d")
	service.overviews <- switchabilityDashboardReply{overview: domain.DashboardOverview{Source: "remote", Range: "7d"}}
	(<-dispatches)()
	if controller.Overview == nil || controller.Overview.Source != "remote" || controller.Error != nil || controller.Loading {
		t.Fatalf("dashboard recovery did not replace the visible snapshot: overview=%+v error=%v loading=%v", controller.Overview, controller.Error, controller.Loading)
	}
}
