package runner

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestMain(m *testing.M) {
	if mode := os.Getenv("LIBERTY_RUNNER_TEST_CHILD"); mode != "" {
		if mode == "descendant" {
			time.Sleep(30 * time.Second)
			os.Exit(0)
		}
		dir := ""
		for i, arg := range os.Args {
			if arg == "--job-dir" && i+1 < len(os.Args) {
				dir = os.Args[i+1]
			}
		}
		switch mode {
		case "command":
			fmt.Fprintln(os.Stdout, "api_key=hidden_command /synthetic/private.wav")
			fmt.Fprint(os.Stderr, "Bearer hidden_command")
		case "command_wait":
			fmt.Fprintln(os.Stdout, "started")
			time.Sleep(30 * time.Second)
		case "valid":
			fmt.Fprintln(os.Stdout, `{"protocolVersion":2,"type":"diagnostic","level":"info","code":"ready","message":"api_key=hidden_value /synthetic/private.wav"}`)
			fmt.Fprintln(os.Stderr, "Bearer hidden_bearer /synthetic/private.wav")
			data := []byte(`{"protocolVersion":2,"asrBackend":"funasr","diarizationRequested":false,"diarizationStatus":"disabled","warnings":[],"durationMinutes":1,"transcriptSegments":[{"id":"s1","startMs":0,"endMs":1000,"text":"Synthetic"}],"speakerSegments":[]}`)
			if os.WriteFile(filepath.Join(dir, "result.json"), data, 0600) != nil {
				os.Exit(2)
			}
		case "v1_failure_alias", "v1_exact":
			deadline := time.Now().Add(5 * time.Second)
			for {
				_, err := os.Stat(filepath.Join(dir, "synthetic-permit"))
				if err == nil {
					break
				}
				if !errors.Is(err, os.ErrNotExist) || time.Now().After(deadline) {
					os.Exit(2)
				}
				time.Sleep(time.Millisecond)
			}
			data := `{"transcriptSegments":[{"id":"s1","startMs":0,"endMs":1000,"text":"Exact V1 transcript","TEXT":"alias transcript"}],"failureReason":null}`
			if mode == "v1_failure_alias" {
				data = `{"transcriptSegments":[{"id":"s1","startMs":0,"endMs":1000,"text":"must not persist"}],"failureReason":"synthetic actual failure","FAILUREREASON":""}`
			}
			if os.WriteFile(filepath.Join(dir, "result.json"), []byte(data), 0600) != nil {
				os.Exit(2)
			}
		case "v1_progress_alias":
			data := `{"stage":"transcribing","STAGE":"completed","statusMessage":"Exact progress","STATUSMESSAGE":"alias progress","progressPercent":12,"PROGRESSPERCENT":100}`
			if os.WriteFile(filepath.Join(dir, "progress.json"), []byte(data), 0600) != nil {
				os.Exit(2)
			}
			time.Sleep(30 * time.Second)
		case "malformed":
			fmt.Fprintln(os.Stdout, "ordinary output must not be a protocol event")
			time.Sleep(30 * time.Second)
		case "oversize":
			fmt.Fprint(os.Stdout, strings.Repeat("x", MaxEventBytes+100))
			time.Sleep(30 * time.Second)
		case "failure":
			os.Exit(3)
		case "missing":
		case "sleep":
			time.Sleep(30 * time.Second)
		case "tree":
			child := exec.Command(os.Args[0])
			child.Env = append(os.Environ(), "LIBERTY_RUNNER_TEST_CHILD=descendant")
			if child.Start() != nil {
				os.Exit(2)
			}
			fmt.Fprintf(os.Stdout, "{\"protocolVersion\":2,\"type\":\"diagnostic\",\"level\":\"info\",\"code\":\"child_started\",\"message\":\"%d\"}\n", child.Process.Pid)
			time.Sleep(30 * time.Second)
		}
		os.Exit(0)
	}
	os.Exit(m.Run())
}
func testSpec(t *testing.T, mode string) CommandSpec {
	t.Helper()
	t.Setenv("LIBERTY_RUNNER_TEST_CHILD", mode)
	dir := t.TempDir()
	script, input := filepath.Join(dir, "runner-fixture"), filepath.Join(dir, "input-fixture")
	if os.WriteFile(script, nil, 0600) != nil || os.WriteFile(input, nil, 0600) != nil {
		t.Fatal("fixture write")
	}
	binary, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	return CommandSpec{Python: binary, Script: script, Input: input, Directory: dir, Language: "zh", Backend: "funasr", Threads: 2, BatchSeconds: 300}
}
func TestExecuteConsumesResultAndRedactsDiagnostics(t *testing.T) {
	spec := testSpec(t, "valid")
	messages := []string{}
	started := false
	result, err := Execute(context.Background(), spec, Hooks{Started: func(pid uint32) error { started = pid > 0; return nil }, Event: func(event Event) error { messages = append(messages, event.Message); return nil }, Diagnostic: func(line string) error { messages = append(messages, line); return nil }})
	if err != nil {
		t.Fatal(err)
	}
	if !started || len(result.Transcript) != 1 || result.Transcript[0].Text != "Synthetic" || len(messages) != 2 {
		t.Fatal("missing process/result/output")
	}
	for _, message := range messages {
		if strings.Contains(message, "hidden") || strings.Contains(message, "private.wav") {
			t.Fatal("sensitive diagnostic leaked")
		}
	}
}
func TestExecuteFailsClosedAndAlwaysReaps(t *testing.T) {
	for _, test := range []struct {
		mode string
		want error
	}{{"malformed", ErrProtocol}, {"oversize", ErrProtocol}, {"failure", ErrProcess}, {"missing", ErrOutput}} {
		t.Run(test.mode, func(t *testing.T) {
			spec := testSpec(t, test.mode)
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			_, err := Execute(ctx, spec, Hooks{})
			if !errors.Is(err, test.want) {
				t.Fatalf("got %v want %v", err, test.want)
			}
		})
	}
}
func TestExecuteCancelsAndCallbackFailureStopsProcess(t *testing.T) {
	spec := testSpec(t, "sleep")
	ctx, cancel := context.WithCancel(context.Background())
	_, err := Execute(ctx, spec, Hooks{Started: func(uint32) error { cancel(); return nil }})
	if !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	injected := errors.New("synthetic_callback_failure")
	_, err = Execute(context.Background(), spec, Hooks{Started: func(uint32) error { return injected }})
	if !errors.Is(err, injected) {
		t.Fatal("callback error lost")
	}
}
func TestLineWriterBoundsStreamsAndFlushesLastLine(t *testing.T) {
	lines := []streamLine{}
	w := lineWriter{stderr: true, emit: func(line streamLine) error { lines = append(lines, line); return nil }}
	if _, err := w.Write(bytes.Repeat([]byte("x"), MaxEventBytes*4)); err != nil {
		t.Fatal(err)
	}
	if len(w.data) > MaxEventBytes {
		t.Fatal("unbounded buffer")
	}
	if err := w.flush(); err != nil {
		t.Fatal(err)
	}
	if len(lines) != 1 || !strings.HasSuffix(string(lines[0].data), "[truncated]") {
		t.Fatal("truncation not projected")
	}
}
func TestCommandUsesArgumentBoundariesAndOfflineCaches(t *testing.T) {
	s := testSpec(t, "valid")
	s.Hotwords = []string{"a;echo injected", "b c"}
	s.Models = filepath.Join(s.Directory, "models")
	s.ModelPaths = modelPathFixture(t, s.Models)
	s.Device = "invalid"
	cmd, err := s.command(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if cmd.Args[len(cmd.Args)-1] != "a;echo injected,b c" {
		t.Fatal("argument splitting")
	}
	for _, want := range []string{"OMP_NUM_THREADS=2", "FUNASR_DEVICE=auto", "HF_HUB_OFFLINE=1", "MODELSCOPE_OFFLINE=1", "TRANSFORMERS_OFFLINE=1"} {
		found := false
		for _, entry := range cmd.Env {
			if entry == want {
				found = true
			}
		}
		if !found {
			t.Fatal("missing " + want)
		}
	}
}
