package host

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"liberty.local/liberty/internal/application"
	"liberty.local/liberty/internal/domain"
	"liberty.local/liberty/internal/infrastructure/execution"
	"liberty.local/liberty/internal/infrastructure/profile"
	"liberty.local/liberty/internal/infrastructure/repositories/jobdb"
	"liberty.local/liberty/internal/infrastructure/runner"
)

const hostCrashJob = "job-1700000000000-913"

type hostCrashRuntime struct {
	script   string
	previous *uint32
	launches atomic.Int32
}

func (runtime *hostCrashRuntime) Resolve(context.Context) (execution.Runtime, error) {
	if runtime.previous != nil {
		if _, err := runner.CaptureIdentity(*runtime.previous); !errors.Is(err, runner.ErrProcessGone) {
			return execution.Runtime{}, domain.ErrTerminationUnverified
		}
	}
	runtime.launches.Add(1)
	return execution.Runtime{Python: "/bin/bash", Script: runtime.script, Backend: "funasr", Device: "cpu", Threads: 1}, nil
}

func hostCrashInstallRunner(t *testing.T, owner *Profile, runtime *hostCrashRuntime) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if owner.scheduler.Shutdown(ctx) != nil {
		t.Fatal("synthetic host could not drain its unused scheduler")
	}
	owner.scheduler = application.NewScheduler(application.NewExecutor(owner.store, &execution.Runner{Files: owner.files, Runtime: runtime}, time.Now, owner.Pet), 1)
	owner.Deleter = application.NewDeleter(owner.store, owner.files, execution.Processes{}, owner.scheduler, owner.Summary)
	owner.recovery = application.NewRecovery(owner.store, owner.Deleter, execution.Processes{}, owner.scheduler)
}

func hostCrashAwait(t *testing.T, ctx context.Context, condition func() bool) {
	t.Helper()
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	for {
		if condition() {
			return
		}
		select {
		case <-ctx.Done():
			t.Fatal("synthetic host crash handshake timed out")
		case <-ticker.C:
		}
	}
}

func TestHostCrashRecoverySubprocess(t *testing.T) {
	directory, nonce := os.Getenv("LIBERTY_HOST_CRASH_DIRECTORY"), os.Getenv("LIBERTY_HOST_CRASH_NONCE")
	if directory == "" || nonce == "" {
		t.Skip("parent-owned synthetic subprocess only")
	}
	guard, err := os.ReadFile(filepath.Join(directory, "guard"))
	if !filepath.IsAbs(directory) || len(nonce) != 32 || err != nil || string(guard) != nonce {
		t.Fatal("synthetic host guard rejected")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	database := filepath.Join(directory, "liberty.sqlite3")
	store, _, err := jobdb.OpenPrepared(ctx, database, nil, true)
	if err != nil {
		t.Fatal("synthetic host database preparation failed")
	}
	owner, err := assemble(ctx, database, store)
	if err != nil {
		store.Close()
		t.Fatal("synthetic host assembly failed")
	}
	defer owner.Close()
	hostCrashInstallRunner(t, owner, &hostCrashRuntime{script: filepath.Join(directory, "waiting-"+nonce+".sh")})
	if owner.store.Create(ctx, domain.JobInput{Job: domain.JobSummary{ID: hostCrashJob, Title: "Synthetic interrupted meeting", CreatedAt: "2026-11-20T00:00:00Z", Lang: "zh", SourceFiles: []domain.SourceFile{{ID: "synthetic-crash-source", Name: "synthetic.wav", Kind: "audio", SizeLabel: "1 B"}}}, SourcePath: filepath.Join(directory, "synthetic.wav")}) != nil || owner.Start(ctx) != nil {
		t.Fatal("synthetic host could not launch its persisted job")
	}
	hostCrashAwait(t, ctx, func() bool {
		records, err := store.Recoverable(ctx)
		if err != nil || len(records) != 1 || records[0].PID == nil || records[0].ProcessIdentity == nil {
			return false
		}
		encoded, err := owner.files.ReadAttemptFile(records[0].RunLease, "result.json", runner.MaxResultBytes)
		if err != nil {
			return false
		}
		// The shell creates result.json before cat finishes writing it. Killing
		// the host at that point could interrupt the fixture's stale result.
		// Publish readiness only after the complete result satisfies the real
		// protocol, so recovery must preserve a fully written obsolete artifact.
		_, err = runner.ParseResult(encoded, false, "funasr")
		return err == nil
	})
	if os.WriteFile(filepath.Join(directory, "ready"), []byte(nonce), 0600) != nil {
		t.Fatal("synthetic host handshake publication failed")
	}
	<-ctx.Done()
	t.Fatal("synthetic host was not killed within its deadline")
}

func TestHostSIGKILLRecoversOrphanRunnerAndFencesStaleResult(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
	defer cancel()
	directory, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal("synthetic host directory unavailable")
	}
	var random [16]byte
	if _, err := rand.Read(random[:]); err != nil {
		t.Fatal("synthetic host nonce unavailable")
	}
	nonce := hex.EncodeToString(random[:])
	waitingScript := filepath.Join(directory, "waiting-"+nonce+".sh")
	freshScript := filepath.Join(directory, "fresh-"+nonce+".sh")
	result := func(text string) []byte {
		encoded, err := json.Marshal(map[string]any{
			"protocolVersion": 2, "asrBackend": "funasr", "diarizationRequested": false,
			"diarizationStatus": "disabled", "warnings": []any{}, "durationMinutes": 1,
			"transcriptSegments": []map[string]any{{"id": "synthetic-segment", "startMs": 0, "endMs": 1000, "text": text}},
			"speakerSegments":    []any{},
		})
		if err != nil {
			t.Fatal("synthetic result encoding failed")
		}
		if _, err := runner.ParseResult(encoded, false, "funasr"); err != nil {
			t.Fatal("synthetic result does not satisfy the real Runner protocol")
		}
		return encoded
	}
	staleResult, freshResult := result("Synthetic stale result"), result("Synthetic recovered result")
	for path, data := range map[string][]byte{
		filepath.Join(directory, "guard"):         []byte(nonce),
		filepath.Join(directory, "synthetic.wav"): []byte("synthetic media"),
		waitingScript: []byte("printf '%s' \"$$\" > \"$0.pid\"\ncat > result.json <<'RESULT'\n" + string(staleResult) + "\nRESULT\n/bin/sleep 20\nexit 0\n"),
		freshScript:   []byte("cat > result.json <<'RESULT'\n" + string(freshResult) + "\nRESULT\n/bin/sleep 0.2\n"),
	} {
		if os.WriteFile(path, data, 0600) != nil {
			t.Fatal("synthetic host fixture publication failed")
		}
	}
	executable, err := os.Executable()
	if err != nil {
		t.Fatal("synthetic host test executable unavailable")
	}
	child := exec.Command(executable, "-test.run=^TestHostCrashRecoverySubprocess$|^"+nonce+"$", "-test.timeout=25s")
	child.Env = append(os.Environ(), "LIBERTY_HOST_CRASH_DIRECTORY="+directory, "LIBERTY_HOST_CRASH_NONCE="+nonce)
	if child.Start() != nil {
		t.Fatal("synthetic host subprocess could not start")
	}
	done := make(chan error, 1)
	go func() { done <- child.Wait() }()
	waited := false
	var childIdentity string
	t.Cleanup(func() {
		if !waited {
			identity, err := runner.CaptureIdentity(uint32(child.Process.Pid))
			if err == nil && strings.Contains(identity, nonce) && strings.Contains(identity, "TestHostCrashRecoverySubprocess") && (childIdentity == "" || identity == childIdentity) {
				if child.Process.Kill() != nil {
					t.Error("synthetic host cleanup could not signal its verified subprocess")
				}
			} else if !errors.Is(err, runner.ErrProcessGone) {
				t.Error("synthetic host cleanup refused an unverified subprocess")
			}
			select {
			case <-done:
			case <-time.After(4 * time.Second):
				t.Error("synthetic host cleanup did not reap its subprocess")
			}
		}
		pidBytes, err := os.ReadFile(waitingScript + ".pid")
		if errors.Is(err, os.ErrNotExist) {
			return
		}
		pidValue, parseErr := strconv.ParseUint(string(pidBytes), 10, 32)
		if err != nil || parseErr != nil || pidValue == 0 {
			t.Error("synthetic runner cleanup could not read its owner marker")
			return
		}
		pid := uint32(pidValue)
		identity, err := runner.CaptureIdentity(pid)
		if errors.Is(err, runner.ErrProcessGone) {
			return
		}
		if err != nil || !strings.Contains(identity, waitingScript) || !strings.Contains(identity, nonce) {
			t.Error("synthetic runner cleanup refused an unverified process")
			return
		}
		if runner.TerminatePersisted(&pid, &identity) != nil {
			t.Error("synthetic runner cleanup could not terminate its verified process tree")
		}
		if _, err := runner.CaptureIdentity(pid); !errors.Is(err, runner.ErrProcessGone) {
			t.Error("synthetic runner remained live after cleanup")
		}
	})
	childIdentity, err = runner.CaptureIdentity(uint32(child.Process.Pid))
	if err != nil || !strings.Contains(childIdentity, nonce) || !strings.Contains(childIdentity, "TestHostCrashRecoverySubprocess") {
		t.Fatal("synthetic host identity could not be verified")
	}
	hostCrashAwait(t, ctx, func() bool {
		select {
		case <-done:
			waited = true
			t.Fatal("synthetic host exited before its crash boundary")
		default:
		}
		ready, err := os.ReadFile(filepath.Join(directory, "ready"))
		return err == nil && string(ready) == nonce
	})
	database := filepath.Join(directory, "liberty.sqlite3")
	view := recoverySQL(t, database, "ro")
	var previous domain.RunRecord
	previous.JobID = hostCrashJob
	if view.QueryRowContext(ctx, "SELECT attempt_id,lease_token,status,pid,process_identity,output_dir FROM job_runs WHERE job_id=?", hostCrashJob).Scan(&previous.AttemptID, &previous.Token, &previous.Status, &previous.PID, &previous.ProcessIdentity, &previous.OutputDir) != nil || previous.Status != "running" || previous.PID == nil || previous.ProcessIdentity == nil {
		t.Fatal("synthetic runner attachment was not durably committed")
	}
	view.Close()
	files, err := runner.OpenFiles(directory)
	if err != nil {
		t.Fatal("synthetic attempt files unavailable before crash")
	}
	beforeCrash, err := files.ReadAttemptFile(previous.RunLease, "result.json", runner.MaxResultBytes)
	files.Close()
	if err != nil || strings.TrimSpace(string(beforeCrash)) != string(staleResult) {
		t.Fatal("synthetic crash handshake did not preserve the complete stale result")
	}
	current, err := runner.CaptureIdentity(*previous.PID)
	if err != nil || current != *previous.ProcessIdentity || !strings.Contains(current, waitingScript) {
		t.Fatal("synthetic runner persisted identity does not match its live process")
	}
	owner, err := profile.Acquire(directory)
	if owner != nil {
		owner.Close()
	}
	if !errors.Is(err, profile.ErrInUse) {
		t.Fatal("synthetic host did not own its profile lock")
	}
	lockBefore, err := os.Stat(filepath.Join(directory, ".liberty-profile.lock"))
	if err != nil {
		t.Fatal("synthetic profile lock identity unavailable")
	}
	current, err = runner.CaptureIdentity(uint32(child.Process.Pid))
	if err != nil || current != childIdentity || child.Process.Kill() != nil {
		t.Fatal("synthetic host SIGKILL identity verification failed")
	}
	select {
	case waitErr := <-done:
		waited = true
		status, ok := child.ProcessState.Sys().(syscall.WaitStatus)
		if waitErr == nil || !ok || !status.Signaled() || status.Signal() != syscall.SIGKILL {
			t.Fatal("synthetic host was not reaped after SIGKILL")
		}
	case <-time.After(4 * time.Second):
		t.Fatal("synthetic host SIGKILL did not complete within its deadline")
	}
	if _, err := runner.CaptureIdentity(uint32(child.Process.Pid)); !errors.Is(err, runner.ErrProcessGone) {
		t.Fatal("synthetic host process remained live after SIGKILL")
	}
	current, err = runner.CaptureIdentity(*previous.PID)
	if err != nil || current != *previous.ProcessIdentity {
		t.Fatal("SIGKILL fixture did not leave the separately owned runner alive")
	}
	owner, err = profile.Acquire(directory)
	if err != nil {
		t.Fatal("kernel did not release the crashed host profile lock")
	}
	if owner.Close() != nil {
		t.Fatal("synthetic profile lock could not be released")
	}
	lockAfter, err := os.Stat(filepath.Join(directory, ".liberty-profile.lock"))
	if err != nil || !os.SameFile(lockBefore, lockAfter) {
		t.Fatal("crash recovery replaced the persistent lock file")
	}
	restarted, err := Open(ctx, database)
	if err != nil {
		t.Fatal("synthetic host could not reopen committed crash state")
	}
	defer restarted.Close()
	freshRuntime := &hostCrashRuntime{script: freshScript, previous: previous.PID}
	hostCrashInstallRunner(t, restarted, freshRuntime)
	if restarted.Start(ctx) != nil || restarted.Start(ctx) != nil {
		t.Fatal("synthetic host startup recovery failed")
	}
	hostCrashAwait(t, ctx, func() bool {
		result, err := restarted.Meetings.Result(ctx, hostCrashJob)
		if err == nil && result.Job.OverallStatus == "failed" {
			t.Fatal("synthetic replacement attempt failed instead of publishing its result")
		}
		return err == nil && result.Job.OverallStatus == "completed"
	})
	if restarted.scheduler.Shutdown(ctx) != nil || freshRuntime.launches.Load() != 1 {
		t.Fatal("startup did not schedule exactly one replacement attempt")
	}
	var stale domain.RunnerResult
	if json.Unmarshal(staleResult, &stale) != nil || !errors.Is(restarted.store.Complete(ctx, domain.Completion{Lease: previous.RunLease, Result: stale, FinishedAtMS: time.Now().UnixMilli()}), domain.ErrStaleAttempt) {
		t.Fatal("crash recovery accepted publication from the obsolete lease")
	}
	assertResult := func(owner *Profile) {
		t.Helper()
		result, err := owner.Meetings.Result(ctx, hostCrashJob)
		if err != nil || result.Job.OverallStatus != "completed" || len(result.Transcript) != 1 || result.Transcript[0].Text != "Synthetic recovered result" {
			t.Fatal("replacement result was not durably published")
		}
		view := recoverySQL(t, database, "ro")
		defer view.Close()
		var attempt, token, starts, completions int64
		if view.QueryRowContext(ctx, `SELECT attempt_id,lease_token,(SELECT COUNT(*) FROM job_events WHERE job_id=? AND event_type='started'),(SELECT COUNT(*) FROM job_events WHERE job_id=? AND event_type='completed') FROM job_runs WHERE job_id=? AND status='completed' AND pid IS NULL`, hostCrashJob, hostCrashJob, hostCrashJob).Scan(&attempt, &token, &starts, &completions) != nil || attempt != previous.AttemptID+1 || token != previous.Token+2 || starts != 2 || completions != 1 {
			t.Fatal("crash recovery did not preserve exact attempt, fence and event counts")
		}
	}
	assertResult(restarted)
	oldBytes, err := restarted.files.ReadAttemptFile(previous.RunLease, "result.json", runner.MaxResultBytes)
	if err != nil || strings.TrimSpace(string(oldBytes)) != string(staleResult) {
		t.Fatal("recovery reused or removed the obsolete attempt artifact")
	}
	restarted.Close()
	reopened, err := Open(ctx, database)
	if err != nil {
		t.Fatal("completed crash recovery did not survive another host reopen")
	}
	defer reopened.Close()
	if reopened.Start(ctx) != nil || reopened.Start(ctx) != nil {
		t.Fatal("completed crash recovery could not restart idempotently")
	}
	assertResult(reopened)
	if _, err := runner.CaptureIdentity(*previous.PID); !errors.Is(err, runner.ErrProcessGone) {
		t.Fatal("startup did not terminate the orphan runner")
	}
}
