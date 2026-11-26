package host

import (
	"context"
	"path/filepath"
	"testing"

	"liberty.local/liberty/internal/domain"
)

func TestComposedMeetingsExposeRevisionAndHiddenPetReadsStayReadOnly(t *testing.T) {
	ctx := context.Background()
	p, _, err := OpenPrepared(ctx, filepath.Join(t.TempDir(), "liberty.sqlite3"), true)
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	// First reads may provision the profile; steady companion polls must not
	// turn every completed result window's revision check into a full read.
	if _, err := p.Pet.Profile(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := p.Pet.Settings(ctx); err != nil {
		t.Fatal(err)
	}
	read := func() uint64 {
		t.Helper()
		revision, err := p.ModeMeetings.ResultRevisionForSource(ctx, "local", "job-1700000000000-0")
		if err != nil || revision == 0 {
			t.Fatal("host composition lost the revision port", err)
		}
		return revision
	}
	before := read()
	for range 3 {
		if _, err := p.Pet.Profile(ctx); err != nil {
			t.Fatal(err)
		}
		if _, err := p.Pet.Settings(ctx); err != nil {
			t.Fatal(err)
		}
		if read() != before {
			t.Fatal("hidden pet poll wrote to the database")
		}
	}
	if _, err := p.Resources.SaveMember(ctx, domain.MeetingMember{Name: "Synthetic revision"}); err != nil {
		t.Fatal(err)
	}
	if read() <= before {
		t.Fatal("host commit did not invalidate result revisions")
	}
	if page, err := p.ModeMeetings.Queue(ctx, domain.QueueQuery{Source: "local"}); err != nil || page.Source != "local" || page.Total != 0 || page.Page != 1 {
		t.Fatal("composed paged queue", page, err)
	}
}
