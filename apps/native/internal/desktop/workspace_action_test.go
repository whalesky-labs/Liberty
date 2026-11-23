package desktop

import (
	"testing"

	"liberty.local/liberty/internal/domain"
)

func TestWorkspaceJobCompletionKeepsOtherSourceDetail(t *testing.T) {
	for _, source := range []string{"local", "remote"} {
		for _, deleted := range []bool{false, true} {
			workspace, _, callbacks := newWorkspaceJobs(t, "en-US")
			other := "remote"
			if source == "remote" {
				other = "local"
			}
			workspace.view.Source = other
			workspace.controller.Refresh(domain.QueueQuery{Source: other})
			awaitWorkspaceJobs(t, callbacks, func() bool { return !workspace.controller.LoadingJobs })
			workspace.controller.Select("same-id")
			awaitWorkspaceJobs(t, callbacks, func() bool { return !workspace.controller.LoadingResult })
			workspace.openDetailFromSource("same-id", source)
			awaitWorkspaceJobs(t, callbacks, func() bool { return workspace.detail.Client.Result != nil })
			detail, result := workspace.detail, workspace.detail.Client.Result
			workspace.completeJobAction(other, "same-id", deleted)
			if workspace.detail != detail || workspace.detail.Client.Result != result || workspace.detail.Client.LoadingResult || workspace.tab != "/jobs/same-id" {
				t.Fatal("another source's completion changed the open detail", source, other, deleted)
			}
			if deleted && workspace.controller.SelectedID != "" {
				t.Fatal("matching queue deletion did not clear selection", other)
			}
			workspace.completeJobAction(source, "same-id", true)
			if workspace.detail != nil || workspace.tab != "/jobs" {
				t.Fatal("matching detail deletion did not close the detail", source)
			}
		}
	}
}
