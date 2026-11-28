// Liberty's native migration client. Preview builds require an explicit profile;
// candidate builds use a separate profile unless explicitly overridden.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"time"

	"github.com/egoist/mygo"
	_ "github.com/egoist/mygo/locales/zh-Hans"
	"liberty.local/liberty/internal/application"
	"liberty.local/liberty/internal/client"
	"liberty.local/liberty/internal/desktop"
	"liberty.local/liberty/internal/host"
	"liberty.local/liberty/internal/infrastructure/credentials"
	"liberty.local/liberty/internal/infrastructure/managed"
	"liberty.local/liberty/internal/infrastructure/profile"
	"liberty.local/liberty/internal/infrastructure/repositories/legacydb"
	"liberty.local/liberty/internal/infrastructure/system"
)

func main() {
	if err := run(); err != nil {
		// Repository errors are stable codes, never raw SQL errors or user paths.
		fmt.Fprintln(os.Stderr, err)
		if isProfileImportCommand(os.Args[1:]) || !nativeGUIAvailable(runtime.GOOS, runtime.GOARCH) {
			fmt.Fprintln(os.Stderr, startupFailureMessage(err))
		}
		if launchChannel == "candidate" && !eventLoopStarted && !isProfileImportCommand(os.Args[1:]) && nativeGUIAvailable(runtime.GOOS, runtime.GOARCH) {
			showStartupFailure(err)
		}
		os.Exit(1)
	}
}

func run() error {
	return runWithProfileResolver(profile.DefaultDirectory)
}

func runWithProfileResolver(resolve func(bool) (string, error)) error {
	options, err := parseOptions(os.Args[1:], launchChannel, os.Stdout)
	if errors.Is(err, flag.ErrHelp) {
		return nil
	}
	if err != nil {
		return err
	}
	if options.version {
		fmt.Fprintf(os.Stdout, "Liberty Native %s (%s)\n", host.Version, launchChannel)
		return nil
	}
	if err := managed.ValidateRuntimeProfile(options.runtimeProfile); err != nil {
		return err
	}
	if options.importProfile != "" {
		return importProfileSnapshot(context.Background(), options, resolve, credentials.NewSystem(), os.Stdout)
	}
	if options.restoreBackup == "" && !nativeGUIAvailable(runtime.GOOS, runtime.GOARCH) {
		return errNativeGUIUnsupported
	}
	if err := options.resolveProfile(resolve); err != nil {
		return err
	}
	if options.restoreBackup != "" {
		result, err := host.RestoreProfile(context.Background(), options.database, options.restoreBackup)
		if err != nil {
			return err
		}
		if launchChannel == "candidate" && nativeGUIAvailable(runtime.GOOS, runtime.GOARCH) {
			showRestoreComplete(result.RestoredVersion, filepath.Base(result.SafetyBackup))
		}
		return nil
	}
	var writableProfile *host.Profile
	var reader *legacydb.Reader
	if options.writable {
		writableProfile, _, err = host.OpenHandoverWithRuntimeProfile(context.Background(), options.database, options.prepare, options.runtimeProfile)
		if err != nil {
			return err
		}
		defer writableProfile.Close()
		reader = writableProfile.Reader
		if err := writableProfile.Start(context.Background()); err != nil {
			return err
		}
	} else {
		reader, err = legacydb.Open(context.Background(), options.database)
		if err != nil {
			return err
		}
		defer reader.Close()
	}
	preferences, err := reader.UIPreferences(context.Background())
	if err != nil {
		return err
	}
	processingMode := "local"
	if writableProfile != nil {
		settings, settingsErr := writableProfile.Resources.Settings(context.Background())
		if settingsErr != nil {
			return settingsErr
		}
		processingMode = settings.ProcessingMode
	}
	startup := client.StartupPreferences{Locale: options.locale, ThemeMode: options.theme}
	preferences = startup.Apply(preferences)
	if launchChannel == "candidate" {
		mygo.App.SetName("Liberty Native Candidate")
	}
	mygo.App.SetVersion(host.Version)
	mygo.App.WhenReady(func() {
		switch preferences.ThemeMode {
		case "light":
			mygo.Theme.SetSource(mygo.ThemeLight)
		case "dark":
			mygo.Theme.SetSource(mygo.ThemeDark)
		default:
			mygo.Theme.SetSource(mygo.ThemeSystem)
		}
		var meetings client.Meetings = application.NewMeetings(reader)
		if writableProfile != nil {
			meetings = writableProfile.ModeMeetings
		}
		dashboardReader := application.NewDashboard(reader, time.Now)
		if writableProfile != nil {
			dashboardReader = application.NewDashboard(application.NewModeDashboard(reader, writableProfile.ModeMeetings), time.Now)
		}
		workspace := desktop.OpenWorkspace(meetings, dashboardReader, preferences, processingMode)
		workspace.AttachMetrics((&system.MetricsSampler{}).Sample)
		if writableProfile != nil {
			workspace.AttachRuntime(writableProfile.Runtime)
			workspace.AttachMode(writableProfile.ModeMeetings)
			workspace.AttachJobs(writableProfile.Submission, writableProfile.Deleter, writableProfile.Selections, writableProfile.ModeMeetings)
			workspace.AttachResources(writableProfile.Resources)
			workspace.AttachSettings(writableProfile.Resources, startup)
			workspace.AttachDeviceStorage(writableProfile.DeviceStorage)
			workspace.AttachResultEditing(writableProfile.ModeMeetings)
			workspace.AttachPlayback(writableProfile.Playback)
			workspace.AttachWordExport(writableProfile.WordExport)
			workspace.AttachSummary(writableProfile.Summary)
			workspace.AttachLive(writableProfile.Live)
			workspace.AttachWork(writableProfile.Work)
			workspace.AttachPet(writableProfile.Pet, writableProfile.RecordPetDiagnostic)
			workspace.AttachRemoteDiagnostics(writableProfile.Remote, writableProfile.Diagnostics, launchChannel)
		}
		workspace.AttachUpdates(host.NewUpdates())
		workspace.RefreshApplicationMenu()
	})
	mygo.App.OnWindowAllClosed(func() { mygo.App.Quit() })
	offQuit := mygo.App.OnQuit(func() {
		if writableProfile != nil {
			writableProfile.Close()
		} else {
			_ = reader.Close()
		}
	})
	defer offQuit()
	eventLoopStarted = true
	return mygo.App.Run()
}
