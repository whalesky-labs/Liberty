package main

import (
	"errors"
	"flag"
	"io"
	"path/filepath"
	"strings"

	"liberty.local/liberty/internal/infrastructure/profile"
)

var launchChannel = "preview"

type launchOptions struct {
	database         string
	profileDirectory string
	restoreBackup    string
	importProfile    string
	runtimeProfile   string
	defaultProfile   bool
	locale           string
	theme            string
	writable         bool
	prepare          bool
	version          bool
}

func parseOptions(args []string, channel string, output io.Writer) (launchOptions, error) {
	var options launchOptions
	flags := flag.NewFlagSet("liberty-native", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	flags.StringVar(&options.database, "database", "", "Explicit Liberty SQLite snapshot (always read only)")
	flags.StringVar(&options.profileDirectory, "profile-directory", "", "Explicit absolute profile directory; --prepare-database permits creation")
	flags.StringVar(&options.restoreBackup, "restore-backup", "", "Restore a verified same-profile backup filename")
	flags.StringVar(&options.importProfile, "import-profile", "", "Import a consistent snapshot from this absolute source directory into a new --profile-directory, then exit")
	flags.StringVar(&options.runtimeProfile, "runtime-profile", "production", "Pinned runtime profile: production or security-candidate; candidate requires an explicit isolated writable profile")
	flags.BoolVar(&options.defaultProfile, "default-profile", false, "View the existing production profile read only; import into a separate directory for Native writes or recovery")
	flags.StringVar(&options.locale, "locale", "", "Override saved language: zh-CN or en-US")
	flags.StringVar(&options.theme, "theme", "", "Override saved theme: auto, light or dark")
	flags.BoolVar(&options.writable, "writable", false, "Enable native jobs and profile writes")
	flags.BoolVar(&options.prepare, "prepare-database", false, "Initialize or migrate the selected profile; requires --writable")
	flags.BoolVar(&options.version, "version", false, "Print version and channel without accessing a profile")
	if err := flags.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			flags.SetOutput(output)
			flags.PrintDefaults()
			return options, err
		}
		return options, errors.New("invalid_launch_arguments")
	}
	if flags.NArg() != 0 || (channel != "preview" && channel != "candidate") {
		return options, errors.New("invalid_launch_arguments")
	}
	if options.locale != "" && options.locale != "zh-CN" && options.locale != "en-US" {
		return options, errors.New("unsupported_locale")
	}
	if options.theme != "" && options.theme != "auto" && options.theme != "light" && options.theme != "dark" {
		return options, errors.New("unsupported_theme")
	}
	if options.runtimeProfile != "production" && options.runtimeProfile != "security-candidate" {
		return options, errors.New("unsupported_runtime_profile")
	}
	if options.runtimeProfile != "production" && (options.profileDirectory == "" || !options.writable || options.defaultProfile || options.database != "" || options.restoreBackup != "" || options.version) {
		return options, errors.New("candidate_runtime_requires_explicit_writable_profile")
	}
	selections := 0
	invalidSelection := false
	modeSpecified := false
	flags.Visit(func(selected *flag.Flag) {
		switch selected.Name {
		case "database", "profile-directory", "restore-backup", "import-profile":
			invalidSelection = invalidSelection || selected.Value.String() == ""
		case "writable", "prepare-database":
			modeSpecified = true
		}
	})
	if invalidSelection {
		return options, errors.New("invalid_launch_arguments")
	}
	if options.importProfile != "" {
		if options.profileDirectory == "" || options.database != "" || options.defaultProfile || options.writable || options.prepare || options.restoreBackup != "" || options.version || options.locale != "" || options.theme != "" || options.runtimeProfile != "production" {
			return options, errors.New("import_requires_new_isolated_profile_directory")
		}
		if !filepath.IsAbs(options.importProfile) || !filepath.IsAbs(options.profileDirectory) {
			return options, profile.ErrLocation
		}
		return options, nil
	}
	if options.database != "" {
		selections++
	}
	if options.profileDirectory != "" {
		selections++
	}
	if options.defaultProfile {
		selections++
	}
	if selections > 1 {
		return options, errors.New("conflicting_profile_arguments")
	}
	if options.version {
		if selections != 0 || options.restoreBackup != "" || options.writable || options.prepare {
			return options, errors.New("version_cannot_be_combined_with_profile_options")
		}
		return options, nil
	}
	if options.restoreBackup != "" && options.database != "" {
		return options, errors.New("restore_requires_profile_directory")
	}
	if options.restoreBackup != "" && (options.writable || options.prepare) {
		return options, errors.New("restore_cannot_be_combined_with_write_mode")
	}
	if options.restoreBackup != "" && (strings.ContainsAny(options.restoreBackup, "/\\:\x00") || !strings.HasPrefix(options.restoreBackup, profile.DatabaseName+".pre-") || !strings.HasSuffix(options.restoreBackup, ".bak")) {
		return options, errors.New("invalid_launch_arguments")
	}
	if selections == 0 {
		if channel != "candidate" {
			return options, errors.New("database_argument_required")
		}
		if options.restoreBackup != "" {
			if modeSpecified {
				return options, errors.New("restore_cannot_be_combined_with_write_mode")
			}
		} else {
			if modeSpecified {
				return options, errors.New("explicit_mode_requires_profile")
			}
			options.writable, options.prepare = true, true
		}
	}
	if options.prepare && !options.writable {
		return options, errors.New("database_preparation_requires_writable_mode")
	}
	if options.database != "" && options.writable {
		return options, errors.New("explicit_database_is_read_only")
	}
	if options.defaultProfile && (options.writable || options.restoreBackup != "") {
		return options, profile.ErrProductionMutation
	}
	if (options.database != "" && !filepath.IsAbs(options.database)) || (options.profileDirectory != "" && !filepath.IsAbs(options.profileDirectory)) {
		return options, profile.ErrLocation
	}
	return options, nil
}

func (options *launchOptions) resolveProfile(defaultDirectory func(bool) (string, error)) error {
	if options.database != "" {
		return nil
	}
	directory := options.profileDirectory
	if directory == "" {
		selected, err := defaultDirectory(!options.defaultProfile)
		if err != nil {
			return err
		}
		directory = selected
	}
	if !options.defaultProfile && (options.writable || options.restoreBackup != "") {
		production, err := defaultDirectory(false)
		if err != nil {
			return err
		}
		directory, err = profile.IsolatedDirectory(directory, production)
		if err != nil {
			return err
		}
	} else {
		var err error
		directory, err = profile.ResolveDirectory(directory)
		if err != nil {
			return err
		}
	}
	if err := profile.PrepareDirectory(directory, options.prepare && options.writable); err != nil {
		return err
	}
	resolvedAfterPreparation, err := profile.ResolveDirectory(directory)
	if err != nil || !strings.EqualFold(resolvedAfterPreparation, directory) {
		return profile.ErrLocation
	}
	if !options.defaultProfile && (options.writable || options.restoreBackup != "") {
		production, err := defaultDirectory(false)
		if err != nil {
			return err
		}
		if _, err := profile.IsolatedDirectory(directory, production); err != nil {
			return err
		}
	}
	options.profileDirectory = directory
	options.database = filepath.Join(directory, profile.DatabaseName)
	return nil
}
