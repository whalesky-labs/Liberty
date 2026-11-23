package diagnosticlog

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"liberty.local/liberty/internal/domain"
)

func logEntries(test *testing.T, log *Log) ([]byte, []entry) {
	test.Helper()
	data, err := log.DiagnosticLog(context.Background())
	if err != nil || len(data) == 0 || len(data) > MaxBytes {
		test.Fatal("diagnostic snapshot unavailable or unbounded")
	}
	lines := bytes.Split(bytes.TrimSuffix(data, []byte{'\n'}), []byte{'\n'})
	if len(lines) > maxEntries {
		test.Fatal("diagnostic retention exceeded its entry limit")
	}
	values := make([]entry, len(lines))
	for index, line := range lines {
		if json.Unmarshal(line, &values[index]) != nil || !validEntry(values[index]) {
			test.Fatal("diagnostic snapshot contains untyped content")
		}
	}
	return data, values
}

func TestLogPersistsTypedPetEventsAcrossSessions(t *testing.T) {
	directory := t.TempDir()
	log := Open(directory)
	log.Record(WindowShown, Primary)
	log.Record(DragStarted, Extra)
	log.Record(DragFinished, Extra)
	data, _ := logEntries(t, log)
	if !bytes.Contains(data, []byte(`"event":"session_started"`)) || !bytes.Contains(data, []byte(`"event":"drag_finished","instance":"extra"`)) {
		t.Fatal("active companion events did not reach the profile source")
	}
	log.Close()
	stored, err := os.ReadFile(filepath.Join(directory, Filename))
	if err != nil || !bytes.Contains(stored, []byte(`"event":"drag_finished","instance":"extra"`)) || !bytes.Contains(stored, []byte(`"event":"session_ended"`)) {
		t.Fatal("close did not persist the final diagnostic history")
	}
	log.Close()
	log = Open(directory)
	defer log.Close()
	reopened, _ := logEntries(t, log)
	if !bytes.Contains(reopened, []byte(`"event":"session_ended"`)) || !bytes.Contains(reopened, []byte(`"event":"drag_finished","instance":"extra"`)) {
		t.Fatal("reopening lost the previous session's pet events")
	}
	if bytes.Contains(reopened, []byte(`"event":"history_discarded"`)) {
		t.Fatal("valid diagnostic history was discarded")
	}
}

func TestLogRejectsUntrustedHistoryWithoutExportingItsContents(t *testing.T) {
	for name, history := range map[string]string{
		"arbitrary-text":   "synthetic-private-content\n",
		"unknown-field":    "{\"time\":\"2026-11-19T00:00:00Z\",\"event\":\"window_shown\",\"instance\":\"primary\",\"token\":\"synthetic-private-content\"}\n",
		"unknown-event":    "{\"time\":\"2026-11-19T00:00:00Z\",\"event\":\"synthetic-private-content\",\"instance\":\"primary\"}\n",
		"unknown-instance": "{\"time\":\"2026-11-19T00:00:00Z\",\"event\":\"window_shown\",\"instance\":\"synthetic-private-content\"}\n",
		"invalid-time":     "{\"time\":\"synthetic-private-content\",\"event\":\"window_shown\",\"instance\":\"primary\"}\n",
		"wrong-instance":   "{\"time\":\"2026-11-19T00:00:00Z\",\"event\":\"session_started\",\"instance\":\"extra\"}\n",
		"truncated":        "{\"time\":\"synthetic-private-content",
		"oversized":        strings.Repeat("synthetic-private-content", MaxBytes/10),
	} {
		t.Run(name, func(t *testing.T) {
			directory := t.TempDir()
			if os.WriteFile(filepath.Join(directory, Filename), []byte(history), 0600) != nil {
				t.Fatal("history fixture failed")
			}
			log := Open(directory)
			defer log.Close()
			data, _ := logEntries(t, log)
			if bytes.Contains(data, []byte("synthetic-private-content")) || !bytes.Contains(data, []byte(`"event":"history_discarded"`)) {
				t.Fatal("untrusted history reached the export source")
			}
			log.Close()
			stored, err := os.ReadFile(filepath.Join(directory, Filename))
			if err != nil || bytes.Contains(stored, []byte("synthetic-private-content")) || len(stored) > MaxBytes || !bytes.Contains(stored, []byte(`"event":"history_discarded"`)) {
				t.Fatal("safe bounded history was not published")
			}
		})
	}
}

func TestLogLinkedDestinationCannotReadOrOverwriteAnotherFile(t *testing.T) {
	directory := t.TempDir()
	outside := filepath.Join(t.TempDir(), "preserved.log")
	original := []byte("synthetic-private-content")
	if os.WriteFile(outside, original, 0600) != nil {
		t.Fatal("outside fixture failed")
	}
	if err := os.Symlink(outside, filepath.Join(directory, Filename)); err != nil {
		t.Skip("symlink creation unavailable")
	}
	log := Open(directory)
	defer log.Close()
	log.Record(DragFinished, Primary)
	awaitLogEvent(t, log, PersistenceFailed)
	data, _ := logEntries(t, log)
	if bytes.Contains(data, original) || !bytes.Contains(data, []byte(`"event":"persistence_failed"`)) {
		t.Fatal("linked diagnostic destination leaked content or disabled the safe source")
	}
	log.Close()
	after, err := os.ReadFile(outside)
	if err != nil || !bytes.Equal(after, original) {
		t.Fatal("linked destination changed another file")
	}
}

func TestLogRetentionAndSnapshotsRemainBoundedAndIndependent(t *testing.T) {
	log := openWithWriter(t.TempDir(), func(string, []byte) error { return nil })
	defer log.Close()
	for range maxEntries * 3 {
		log.Record(PointerPressed, Primary)
	}
	log.Record(DragFinished, Extra)
	data, values := logEntries(t, log)
	if len(values) != maxEntries || values[len(values)-1].Event != "drag_finished" {
		t.Fatal("retention did not keep the newest events")
	}
	for index := range data {
		data[index] = 'x'
	}
	_, values = logEntries(t, log)
	if values[len(values)-1].Instance != "extra" {
		t.Fatal("caller modified the diagnostic source through a returned snapshot")
	}
	before, _ := log.DiagnosticLog(context.Background())
	for _, invalid := range []struct {
		event    Event
		instance Instance
	}{{0, Primary}, {255, Primary}, {Tapped, 255}, {SessionStarted, Extra}, {Tapped, Application}} {
		log.Record(invalid.event, invalid.instance)
	}
	after, _ := log.DiagnosticLog(context.Background())
	if !bytes.Equal(before, after) {
		t.Fatal("invalid event or instance entered the source")
	}
}

func TestLogCancellationAndClosedSourceCannotProduceNewSnapshots(t *testing.T) {
	log := Open(t.TempDir())
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if data, err := log.DiagnosticLog(ctx); !errors.Is(err, context.Canceled) || data != nil {
		t.Fatal("cancelled diagnostic read succeeded")
	}
	log.Close()
	before, err := os.ReadFile(log.target)
	if err != nil {
		t.Fatal("closed log history unavailable")
	}
	log.Record(Tapped, Primary)
	log.Close()
	if data, err := log.DiagnosticLog(context.Background()); !errors.Is(err, domain.ErrDiagnosticLog) || data != nil {
		t.Fatal("closed diagnostic source was still readable")
	}
	after, err := os.ReadFile(log.target)
	if err != nil || !bytes.Equal(before, after) {
		t.Fatal("closed source accepted another event")
	}
}

func TestLogOldProfileCloseCannotDetachCurrentProfile(t *testing.T) {
	previous := Open(t.TempDir())
	current := Open(t.TempDir())
	defer current.Close()
	previous.Close()
	current.Record(Tapped, Extra)
	data, _ := logEntries(t, current)
	if !bytes.Contains(data, []byte(`"event":"tapped","instance":"extra"`)) {
		t.Fatal("closing an older source detached the current profile")
	}
	prior, err := os.ReadFile(previous.target)
	if err != nil || bytes.Contains(prior, []byte(`"event":"tapped"`)) {
		t.Fatal("current event crossed profile sources")
	}
}

func TestLogConcurrentRecordingSnapshotAndClose(t *testing.T) {
	log := openWithWriter(t.TempDir(), func(string, []byte) error { return nil })
	var workers sync.WaitGroup
	for range 8 {
		workers.Go(func() {
			for range 100 {
				log.Record(DragFinished, Primary)
				data, err := log.DiagnosticLog(context.Background())
				if err != nil && !errors.Is(err, domain.ErrDiagnosticLog) || len(data) > MaxBytes {
					t.Error("concurrent snapshot failed its source contract")
					return
				}
			}
		})
	}
	workers.Go(log.Close)
	workers.Wait()
	log.Close()
}

func TestLogSlowWriterDoesNotBlockRecordOrSnapshot(t *testing.T) {
	var block atomic.Bool
	entered, release := make(chan struct{}), make(chan struct{})
	var enteredOnce, releaseOnce sync.Once
	unblock := func() { releaseOnce.Do(func() { close(release) }) }
	log := openWithWriter(t.TempDir(), func(string, []byte) error {
		if block.Load() {
			enteredOnce.Do(func() { close(entered) })
			<-release
		}
		return nil
	})
	t.Cleanup(func() { unblock(); log.Close() })
	block.Store(true)
	first := make(chan struct{})
	go func() { log.Record(PointerPressed, Primary); close(first) }()
	select {
	case <-entered:
	case <-time.After(3 * time.Second):
		t.Fatal("writer did not reach its controlled blocking point")
	}
	second, snapshot := make(chan struct{}), make(chan struct{})
	go func() { log.Record(DragFinished, Primary); close(second) }()
	go func() {
		data, err := log.DiagnosticLog(context.Background())
		if err != nil || len(data) == 0 {
			t.Error("live snapshot failed while disk writer was blocked")
		}
		close(snapshot)
	}()
	for name, completed := range map[string]<-chan struct{}{"initial-record": first, "record": second, "snapshot": snapshot} {
		select {
		case <-completed:
		case <-time.After(250 * time.Millisecond):
			t.Error(name + " waited for blocked disk writer")
		}
	}
	unblock()
	<-first
	<-second
	<-snapshot
}

func awaitLogEvent(test *testing.T, log *Log, event Event) {
	test.Helper()
	timeout := time.NewTimer(3 * time.Second)
	defer timeout.Stop()
	ticks := time.NewTicker(time.Millisecond)
	defer ticks.Stop()
	for {
		data, err := log.DiagnosticLog(context.Background())
		if err == nil && bytes.Contains(data, []byte(`"event":"`+eventNames[event]+`"`)) {
			return
		}
		select {
		case <-timeout.C:
			test.Fatal("worker did not record its expected diagnostic event")
		case <-ticks.C:
		}
	}
}

func TestLogCloseDrainsMergedTailAndJoinsSingleBlockedWorker(t *testing.T) {
	entered, release := make(chan struct{}), make(chan struct{})
	var unblockOnce sync.Once
	unblock := func() { unblockOnce.Do(func() { close(release) }) }
	var snapshots [][]byte
	log := openWithWriter(t.TempDir(), func(_ string, data []byte) error {
		snapshots = append(snapshots, append([]byte(nil), data...))
		if len(snapshots) == 1 {
			close(entered)
			<-release
		}
		return nil
	})
	t.Cleanup(func() { unblock(); log.Close() })
	select {
	case <-entered:
	case <-time.After(3 * time.Second):
		t.Fatal("initial writer did not start")
	}
	for range 200 {
		log.Record(Tapped, Primary)
	}
	log.Record(PositionSaved, Primary)
	closed := make(chan struct{}, 8)
	for range 8 {
		go func() { log.Close(); closed <- struct{}{} }()
	}
	select {
	case <-closed:
		t.Fatal("close returned while its disk writer was blocked")
	case <-time.After(25 * time.Millisecond):
	}
	unblock()
	for range 8 {
		select {
		case <-closed:
		case <-time.After(3 * time.Second):
			t.Fatal("concurrent close did not join the writer")
		}
	}
	if len(snapshots) != 2 {
		t.Fatal("pending events were not merged into a single final publication")
	}
	last := snapshots[1]
	if bytes.Count(last, []byte(`"event":"tapped"`)) != 200 || bytes.Count(last, []byte(`"event":"session_ended"`)) != 1 || !bytes.Contains(last, []byte(`"event":"position_saved"`)) {
		t.Fatal("concurrent close lost accepted tail events or repeated the end marker")
	}
	lines := bytes.Split(bytes.TrimSpace(last), []byte{'\n'})
	if !bytes.Contains(lines[len(lines)-1], []byte(`"event":"session_ended"`)) {
		t.Fatal("session end was not published after accepted events")
	}
	select {
	case <-log.done:
	default:
		t.Fatal("close returned with an old persistence worker alive")
	}
	log.Record(DragFinished, Extra)
	log.Close()
	if len(snapshots) != 2 {
		t.Fatal("closed source accepted late events or left a disk writer alive")
	}
}

func TestLogWriterFailureIsBoundedAndCanRecover(t *testing.T) {
	var failing atomic.Bool
	failing.Store(true)
	var writes atomic.Int32
	log := openWithWriter(t.TempDir(), func(string, []byte) error {
		writes.Add(1)
		if failing.Load() {
			return errors.New("synthetic writer failure")
		}
		return nil
	})
	defer log.Close()
	awaitLogEvent(t, log, PersistenceFailed)
	failing.Store(false)
	log.Record(PositionSaved, Primary)
	log.Close()
	if writes.Load() < 2 || writes.Load() > 4 {
		t.Fatal("failure caused an unbounded retry loop or skipped recovery")
	}
}

func TestLogReopenedSourceCannotBeOverwrittenByClosedRecorder(t *testing.T) {
	directory := t.TempDir()
	previous := Open(directory)
	late := previous.Record
	late(DragFinished, Extra)
	previous.Close()
	current := Open(directory)
	current.Record(WindowShown, Primary)
	late(Tapped, Extra)
	previous.Close()
	current.Close()
	data, err := os.ReadFile(filepath.Join(directory, Filename))
	if err != nil || bytes.Contains(data, []byte(`"event":"tapped"`)) || !bytes.Contains(data, []byte(`"event":"window_shown"`)) || bytes.Count(data, []byte(`"event":"session_started"`)) != 2 {
		t.Fatal("closed source wrote into or overwrote a reopened profile session")
	}
}
