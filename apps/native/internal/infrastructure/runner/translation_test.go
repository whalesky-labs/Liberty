package runner

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"liberty.local/liberty/internal/domain"
)

func runTranslationFixture(mode string) {
	if mode == "translation_no_ready" {
		time.Sleep(30 * time.Second)
		return
	}
	fmt.Fprintln(os.Stdout, `{"protocol":"liberty-local-translation","version":1,"type":"ready"}`)
	decoder := json.NewDecoder(os.Stdin)
	for {
		var input struct{ Sequence uint64 }
		if decoder.Decode(&input) != nil {
			return
		}
		if mode == "translation_stall" {
			time.Sleep(30 * time.Second)
			return
		}
		if mode == "translation_wrong_sequence" {
			input.Sequence++
		}
		fmt.Fprintf(os.Stdout, `{"protocol":"liberty-local-translation","version":1,"type":"translation","sequence":%d,"text":"Synthetic translation."}`+"\n", input.Sequence)
	}
}

func translationFixture(t *testing.T, mode string) TranslationCommand {
	t.Helper()
	spec := testSpec(t, mode)
	return TranslationCommand{Python: spec.Python, Script: spec.Script, Engine: spec.Directory, Model: spec.Directory, Directory: spec.Directory}
}

func TestTranslationProtocolRejectsAmbiguityAndUnexpectedFields(t *testing.T) {
	valid := `{"protocol":"liberty-local-translation","version":1,"type":"translation","sequence":1,"text":"Synthetic."}`
	if _, err := parseTranslationEvent([]byte(valid)); err != nil {
		t.Fatal(err)
	}
	for _, raw := range []string{
		strings.Replace(valid, `"version":1`, `"version":1,"version":1`, 1),
		strings.Replace(valid, `"version":1`, `"Version":1`, 1),
		strings.Replace(valid, `"version":1`, `"version":true`, 1),
		strings.Replace(valid, `"sequence":1`, `"sequence":null`, 1),
		strings.Replace(valid, `"translation"`, `"ready"`, 1),
		strings.Replace(valid, `"Synthetic."`, `" "`, 1),
		strings.Replace(valid, `"Synthetic."`, `"`+strings.Repeat("中", 3000)+`"`, 1),
		strings.Replace(valid, `"version":1`, `"version":1,"code":"local_translation_failed"`, 1),
		valid + valid,
		`{"protocol":"liberty-local-translation","version":1,"type":"error"}`,
	} {
		if _, err := parseTranslationEvent([]byte(raw)); err == nil {
			t.Fatal("invalid translation event accepted")
		}
	}
}

func TestTranslationWorkerReusesProcessAndAlwaysReaps(t *testing.T) {
	command := translationFixture(t, "translation_valid")
	for range 3 {
		worker, err := command.Start(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		for range 4 {
			text, err := worker.Translate(context.Background(), "合成测试。")
			if err != nil || text != "Synthetic translation." {
				t.Fatal("translation failed", err)
			}
		}
		worker.Close()
		worker.Close()
		if worker.cmd.ProcessState == nil {
			t.Fatal("worker was not reaped")
		}
		if _, err := worker.Translate(context.Background(), "合成测试。"); !errors.Is(err, domain.ErrLiveClosed) {
			t.Fatal("closed worker reused")
		}
	}
}

func TestTranslationCancellationAndSequenceFailureReleaseWorker(t *testing.T) {
	for _, mode := range []string{"translation_no_ready", "translation_stall", "translation_wrong_sequence"} {
		t.Run(mode, func(t *testing.T) {
			command := translationFixture(t, mode)
			ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
			defer cancel()
			worker, err := command.Start(ctx)
			if mode == "translation_no_ready" {
				if !errors.Is(err, context.DeadlineExceeded) {
					t.Fatal("startup timeout lost", err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			defer worker.Close()
			if _, err = worker.Translate(ctx, "合成测试。"); err == nil {
				t.Fatal("invalid worker result accepted")
			}
			if worker.cmd.ProcessState == nil {
				t.Fatal("failed worker not reaped")
			}
		})
	}
}
