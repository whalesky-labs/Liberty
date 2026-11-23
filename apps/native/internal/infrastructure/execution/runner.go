// Package execution adapts the isolated process/files implementations to the
// application use case. It never resolves default profile paths or credentials.
package execution

import (
	"context"
	"liberty.local/liberty/internal/application"
	"liberty.local/liberty/internal/domain"
	"liberty.local/liberty/internal/infrastructure/runner"
)

type Runtime struct {
	Python, Script, FFmpeg, Models string
	ModelPaths                     runner.ModelPaths
	Backend, Device                string
	Threads, BatchSeconds          uint32
}
type RuntimeResolver interface {
	Resolve(context.Context) (Runtime, error)
}
type Runner struct {
	Files   *runner.Files
	Runtime RuntimeResolver
}

func (r *Runner) Run(ctx context.Context, input domain.JobInput, l domain.RunLease, callbacks application.ExecutionCallbacks) (domain.RunnerResult, error) {
	runtime, err := r.Runtime.Resolve(ctx)
	if err != nil {
		return domain.RunnerResult{}, err
	}
	if err := r.Files.CreateJob(l.JobID); err != nil {
		return domain.RunnerResult{}, err
	}
	directory, err := r.Files.CreateAttempt(l)
	if err != nil {
		return domain.RunnerResult{}, err
	}
	return runner.Execute(ctx, runner.CommandSpec{Python: runtime.Python, Script: runtime.Script, Input: input.SourcePath, Directory: directory, FFmpeg: runtime.FFmpeg, Models: runtime.Models, ModelPaths: runtime.ModelPaths,
		Language: input.Job.Lang, Speaker: input.Job.EnableSpeaker, Hotwords: input.Job.Hotwords, Device: runtime.Device, Backend: runtime.Backend, Threads: runtime.Threads, BatchSeconds: runtime.BatchSeconds}, runner.Hooks{
		Started: func(pid uint32) error {
			identity, err := runner.CaptureIdentity(pid)
			if err != nil {
				return err
			}
			if callbacks.Started != nil {
				return callbacks.Started(pid, identity)
			}
			return nil
		},
		Heartbeat: callbacks.Heartbeat,
		Event: func(event runner.Event) error {
			if callbacks.Log != nil {
				return callbacks.Log(event.Code + ": " + event.Message)
			}
			return nil
		},
		Diagnostic: callbacks.Log,
	})
}
