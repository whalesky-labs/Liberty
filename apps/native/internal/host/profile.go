// Package host is the native composition root. It owns profile-level services
// and closes background operations before releasing the shared database lock.
package host

import (
	"context"
	"path/filepath"
	"runtime"
	"sync"
	"time"

	"liberty.local/liberty/internal/application"
	"liberty.local/liberty/internal/domain"
	"liberty.local/liberty/internal/infrastructure/ai"
	"liberty.local/liberty/internal/infrastructure/credentials"
	"liberty.local/liberty/internal/infrastructure/device_storage"
	"liberty.local/liberty/internal/infrastructure/diagnosticlog"
	"liberty.local/liberty/internal/infrastructure/execution"
	"liberty.local/liberty/internal/infrastructure/legacyjobs"
	"liberty.local/liberty/internal/infrastructure/liveaudio"
	"liberty.local/liberty/internal/infrastructure/managed"
	"liberty.local/liberty/internal/infrastructure/media"
	"liberty.local/liberty/internal/infrastructure/playback"
	"liberty.local/liberty/internal/infrastructure/profile"
	"liberty.local/liberty/internal/infrastructure/remote"
	"liberty.local/liberty/internal/infrastructure/repositories/jobdb"
	"liberty.local/liberty/internal/infrastructure/repositories/legacydb"
	"liberty.local/liberty/internal/infrastructure/repositories/schema"
	"liberty.local/liberty/internal/infrastructure/runner"
	"liberty.local/liberty/internal/infrastructure/system"
)

type Profile struct {
	Reader        *legacydb.Reader
	Meetings      *application.Meetings
	ModeMeetings  *application.ModeMeetings
	Remote        *application.RemoteMeetings
	Diagnostics   *application.Diagnostics
	DeviceStorage *application.DeviceStorage
	Runtime       *application.RuntimeManager
	Submission    *application.Submission
	Deleter       *application.Deleter
	Selections    *media.Selections
	Resources     *application.Resources
	SpeakerWriter application.SpeakerWriter
	Playback      application.SegmentPlayer
	LegacyImport  application.LegacyImportResult
	WordExport    *application.WordExport
	Summary       *application.SummaryService
	Live          *application.LiveService
	Work          *application.Work
	Pet           *application.Pet
	store         *jobdb.Store
	files         *runner.Files
	scheduler     *application.Scheduler
	recovery      *application.Recovery
	diagnosticLog *diagnosticlog.Log
	once          sync.Once
}

const Version = "2.0.0-alpha.3"

func (p *Profile) RecordPetDiagnostic(event diagnosticlog.Event, instance diagnosticlog.Instance) {
	if p.diagnosticLog != nil {
		p.diagnosticLog.Record(event, instance)
	}
}

// Open accepts only an explicit existing v11 profile.
func Open(ctx context.Context, database string) (*Profile, error) {
	store, err := jobdb.Open(ctx, database)
	if err != nil {
		return nil, err
	}
	return assemble(ctx, database, store)
}

// OpenPrepared is the migration/new-profile entry after launch policy has
// selected a directory. It never selects a default profile itself.
func OpenPrepared(ctx context.Context, database string, create bool) (*Profile, schema.Result, error) {
	store, result, err := jobdb.OpenPrepared(ctx, database, credentials.NewSystem(), create)
	if err != nil {
		return nil, result, err
	}
	p, err := assemble(ctx, database, store)
	return p, result, err
}

func OpenHandover(ctx context.Context, database string, prepare bool) (*Profile, schema.Result, error) {
	return OpenHandoverWithRuntimeProfile(ctx, database, prepare, "production")
}

func OpenHandoverWithRuntimeProfile(ctx context.Context, database string, prepare bool, runtimeProfile string) (*Profile, schema.Result, error) {
	if err := managed.ValidateRuntimeProfile(runtimeProfile); err != nil {
		return nil, schema.Result{}, err
	}
	directory, err := profile.ResolveDirectory(filepath.Dir(database))
	if err != nil {
		return nil, schema.Result{}, err
	}
	if runtimeProfile != "production" {
		production, err := profile.DefaultDirectory(false)
		if err != nil {
			return nil, schema.Result{}, err
		}
		directory, err = profile.IsolatedDirectory(directory, production)
		if err != nil {
			return nil, schema.Result{}, err
		}
	}
	database = filepath.Join(directory, filepath.Base(database))
	store, result, err := jobdb.OpenHandover(ctx, database, credentials.NewSystem(), prepare)
	if err != nil {
		return nil, result, err
	}
	p, err := assembleWithRuntimeProfile(ctx, database, store, runtimeProfile)
	return p, result, err
}

func RestoreProfile(ctx context.Context, database, backupName string) (schema.RestoreResult, error) {
	var result schema.RestoreResult
	if !filepath.IsAbs(database) {
		return result, schema.ErrRestore
	}
	owner, err := profile.Acquire(filepath.Dir(database))
	if err != nil {
		return result, err
	}
	defer owner.Close()
	database = filepath.Join(owner.Directory(), filepath.Base(database))
	return schema.RestoreLocked(ctx, database, backupName, credentials.NewSystem())
}

func assemble(ctx context.Context, database string, store *jobdb.Store) (*Profile, error) {
	return assembleWithRuntimeProfile(ctx, database, store, "production")
}

func assembleWithRuntimeProfile(ctx context.Context, database string, store *jobdb.Store, runtimeProfile string) (*Profile, error) {
	return assembleWithRuntimePlatform(ctx, database, store, runtimeProfile, managed.CurrentPlatform())
}

func assembleWithRuntimePlatform(ctx context.Context, database string, store *jobdb.Store, runtimeProfile, runtimePlatform string) (*Profile, error) {
	var err error
	p := &Profile{store: store}
	p.Pet = application.NewPet(store)
	p.SpeakerWriter = store
	p.Resources = application.NewResources(store, credentials.NewSystem(), func(settings domain.Settings) {
		if p.scheduler != nil {
			budget := domain.ResolveASRResourceBudget(settings.Concurrency, settings.Threads, uint32(runtime.NumCPU()), system.MemoryBytes())
			p.scheduler.SetLimit(budget.MaxConcurrency)
		}
	})
	success := false
	defer func() {
		if !success {
			p.Close()
		}
	}()
	legacy, err := legacyjobs.Open(filepath.Dir(database))
	if err != nil {
		return nil, err
	}
	p.LegacyImport, err = store.ImportLegacy(ctx, legacy)
	closeErr := legacy.Close()
	if err != nil {
		return nil, err
	}
	if closeErr != nil {
		return nil, legacyjobs.ErrImport
	}
	p.Reader, err = legacydb.Open(ctx, database)
	if err != nil {
		return nil, err
	}
	directory := filepath.Dir(database)
	p.DeviceStorage = application.NewDeviceStorage(directory, device_storage.Scanner{})
	p.files, err = runner.OpenFiles(directory)
	if err != nil {
		return nil, err
	}
	installer, err := managed.NewInstallerWithProfile(directory, runtimePlatform, runtimeProfile)
	if err != nil {
		return nil, err
	}
	p.Runtime = application.NewRuntimeManager(store, installer)
	settings, err := store.RuntimeSettings(ctx)
	if err != nil {
		return nil, err
	}
	budget := domain.ResolveASRResourceBudget(settings.Concurrency, settings.Threads, uint32(runtime.NumCPU()), system.MemoryBytes())
	resolver := &execution.ManagedResolver{Manager: p.Runtime, ModelPaths: installer, Settings: store, Resources: installer.Resources, Backend: installer.Platform.Backend, MemoryBytes: system.MemoryBytes}
	p.Playback = &playback.Player{Inputs: store, Runtime: resolver, Directory: directory}
	executor := application.NewExecutor(store, &execution.Runner{Files: p.files, Runtime: resolver}, time.Now, p.Pet)
	p.scheduler = application.NewScheduler(executor, budget.MaxConcurrency)
	localMeetings := application.NewMeetings(&execution.LiveReader{Reader: p.Reader, Runs: store, Files: p.files})
	p.Meetings = localMeetings
	p.Summary = application.NewSummaryService(p.Meetings, store, p.Resources, ai.Client{}, p.Pet)
	p.Deleter = application.NewDeleter(store, p.files, execution.Processes{}, p.scheduler, p.Summary)
	p.recovery = application.NewRecovery(store, p.Deleter, execution.Processes{}, p.scheduler)
	p.Selections = media.NewSelections()
	p.Submission = application.NewSubmission(store, p.Selections, p.Runtime, p.files, p.scheduler, filepath.Join(installer.Resources, "runner.py"), p.Pet)
	p.Remote = application.NewRemoteMeetings(p.Resources, remote.Client{})
	p.ModeMeetings = application.NewModeMeetings(p.Resources, localMeetings, p.Remote, p.Submission, p.Deleter, p.SpeakerWriter)
	p.diagnosticLog = diagnosticlog.Open(directory)
	p.Diagnostics = application.NewDiagnostics(Version, managed.CurrentPlatform(), store, p.Runtime, p.diagnosticLog)
	p.WordExport = application.NewWordExport(p.Meetings, store)
	p.Live = application.NewLiveService(store, &liveaudio.Audio{Runtime: resolver, Directory: directory}, &execution.Streaming{Runtime: resolver, Models: installer, Settings: store, Directory: directory}, &execution.LocalTranslator{Runtime: resolver, Installer: installer, Directory: directory}, p.scheduler)
	p.Work = application.NewWork(store, time.Now)
	success = true
	return p, nil
}
func (p *Profile) Start(ctx context.Context) error {
	if err := p.Pet.Start(ctx); err != nil {
		return err
	}
	if err := p.Resources.RecoverCredentials(ctx); err != nil {
		return err
	}
	if err := p.recovery.Start(ctx); err != nil {
		return err
	}
	if err := p.Summary.Recover(ctx); err != nil {
		return err
	}
	if err := p.Live.Recover(ctx); err != nil {
		return err
	}
	_, _ = p.Pet.Workflow(ctx, "daily_open", "Liberty app opened")
	return nil
}
func (p *Profile) Close() {
	p.once.Do(func() {
		if player, ok := p.Playback.(interface{ Close() }); ok {
			player.Close()
		}
		if p.Live != nil {
			p.Live.Close()
		}
		if p.Summary != nil {
			p.Summary.Close()
		}
		if p.Remote != nil {
			p.Remote.Close()
		}
		if p.Resources != nil {
			p.Resources.Close()
		}
		// Keep ownership until every runner/installer has reaped its children and
		// committed final state. No second profile writer can race shutdown.
		if p.scheduler != nil {
			_ = p.scheduler.Shutdown(context.Background())
		}
		if p.Runtime != nil {
			p.Runtime.Close()
		}
		if p.files != nil {
			_ = p.files.Close()
		}
		if p.Reader != nil {
			_ = p.Reader.Close()
		}
		if p.diagnosticLog != nil {
			p.diagnosticLog.Close()
		}
		if p.store != nil {
			_ = p.store.Close()
		}
	})
}
