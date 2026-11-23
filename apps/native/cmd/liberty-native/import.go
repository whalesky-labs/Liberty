package main

import (
	"context"
	"fmt"
	"io"
	"strings"

	"liberty.local/liberty/internal/infrastructure/profile"
	"liberty.local/liberty/internal/infrastructure/profiletransfer"
	"liberty.local/liberty/internal/infrastructure/repositories/schema"
)

func isProfileImportCommand(args []string) bool {
	for _, argument := range args {
		if argument == "--import-profile" || argument == "-import-profile" || strings.HasPrefix(argument, "--import-profile=") || strings.HasPrefix(argument, "-import-profile=") {
			return true
		}
	}
	return false
}

func importProfileSnapshot(ctx context.Context, options launchOptions, defaultDirectory func(bool) (string, error), vault schema.Credentials, output io.Writer) error {
	production, err := defaultDirectory(false)
	if err != nil {
		return err
	}
	if err := profile.RequireIsolatedDirectory(options.profileDirectory, production); err != nil {
		return err
	}
	result, err := profiletransfer.Import(ctx, options.importProfile, options.profileDirectory, vault)
	if err != nil {
		return err
	}
	_, err = fmt.Fprintf(output, "独立 Native 资料快照已就绪；旧资料未改动。\nNative profile snapshot ready: schema %d, %d isolated credentials, already imported: %t. Source profile unchanged.\n", result.Version, result.CredentialCount, result.AlreadyImported)
	return err
}
