// Package profiletransfer imports a consistent SQLite snapshot into a separate
// Native profile. It never replaces, migrates, locks or resets the source.
package profiletransfer

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"liberty.local/liberty/internal/infrastructure/profile"
	"liberty.local/liberty/internal/infrastructure/repositories/schema"
)

var ErrImport = schema.ErrProfileTransfer

const stateName = ".native-profile-import.json"
const stageName = ".native-profile-import-stage"

type Result struct {
	Version         int
	CredentialCount int
	AlreadyImported bool
}

type importState struct {
	Format     int      `json:"format"`
	Source     string   `json:"source"`
	Phase      string   `json:"phase"`
	References []string `json:"references"`
	Live       []string `json:"live"`
	Version    int      `json:"version"`
}

func writeState(directory string, state importState) error {
	data, err := json.Marshal(state)
	if err != nil {
		return ErrImport
	}
	file, err := os.CreateTemp(directory, ".native-import-state-*")
	if err != nil {
		return ErrImport
	}
	defer os.Remove(file.Name())
	_, writeErr := file.Write(data)
	syncErr := file.Sync()
	closeErr := file.Close()
	if writeErr != nil || syncErr != nil || closeErr != nil || os.Rename(file.Name(), filepath.Join(directory, stateName)) != nil {
		return ErrImport
	}
	if parent, err := os.Open(directory); err == nil {
		_ = parent.Sync()
		_ = parent.Close()
	}
	return nil
}

func readState(directory, source string) (importState, error) {
	var state importState
	name := filepath.Join(directory, stateName)
	info, err := os.Lstat(name)
	if err != nil || !info.Mode().IsRegular() || info.Size() > 1<<20 {
		return state, ErrImport
	}
	file, err := os.Open(name)
	if err != nil {
		return state, ErrImport
	}
	opened, statErr := file.Stat()
	data, readErr := io.ReadAll(io.LimitReader(file, 1<<20+1))
	closeErr := file.Close()
	if statErr != nil || !os.SameFile(info, opened) || readErr != nil || closeErr != nil || len(data) > 1<<20 || json.Unmarshal(data, &state) != nil || state.Format != 1 || state.Source != source || !slices.Contains([]string{"copying", "ready", "complete"}, state.Phase) {
		return importState{}, ErrImport
	}
	seen := make(map[string]bool)
	for _, reference := range state.References {
		if !strings.HasPrefix(reference, "liberty:native-import:") || len(reference) != len("liberty:native-import:")+48 || seen[reference] {
			return importState{}, ErrImport
		}
		if _, err := hex.DecodeString(strings.TrimPrefix(reference, "liberty:native-import:")); err != nil {
			return importState{}, ErrImport
		}
		seen[reference] = true
	}
	for _, reference := range state.Live {
		if !seen[reference] {
			return importState{}, ErrImport
		}
	}
	return state, nil
}

func compensate(ctx context.Context, directory string, state *importState, vault schema.Credentials, keepLive bool) error {
	for _, reference := range slices.Clone(state.References) {
		if keepLive && slices.Contains(state.Live, reference) {
			continue
		}
		if vault == nil || vault.Delete(ctx, reference) != nil {
			return ErrImport
		}
		secret, err := vault.Get(ctx, reference)
		if err != nil || !secret.Empty() {
			return ErrImport
		}
		state.References = slices.DeleteFunc(state.References, func(value string) bool { return value == reference })
		state.Live = slices.DeleteFunc(state.Live, func(value string) bool { return value == reference })
		if writeState(directory, *state) != nil {
			return ErrImport
		}
	}
	return nil
}

// Import takes one committed snapshot; it does not continuously mirror a
// legacy client. Later legacy writes remain in the legacy profile. Importing
// again never resets an already published Native database, even if its source
// or Native business data have since changed. A different source is rejected.
func Import(ctx context.Context, sourceDirectory, destination string, vault schema.Credentials) (Result, error) {
	var result Result
	source, err := profile.ResolveDirectory(sourceDirectory)
	if err != nil {
		return result, ErrImport
	}
	target, err := profile.IsolatedDirectory(destination, source)
	if err != nil {
		return result, err
	}
	sourceID := sha256.Sum256([]byte(filepath.Join(source, profile.DatabaseName)))
	sourceKey := hex.EncodeToString(sourceID[:])
	created := false
	if err := os.Mkdir(target, 0700); err == nil {
		created = true
	} else if !os.IsExist(err) {
		return result, ErrImport
	}
	owner, err := profile.Acquire(target)
	if err != nil {
		return result, err
	}
	defer owner.Close()
	if owner.Directory() != target {
		return result, ErrImport
	}
	state := importState{Format: 1, Source: sourceKey, Phase: "copying"}
	if created {
		if writeState(target, state) != nil {
			return result, ErrImport
		}
	} else {
		state, err = readState(target, sourceKey)
		if err != nil {
			return result, ErrImport
		}
	}
	database := filepath.Join(target, profile.DatabaseName)
	stage := filepath.Join(target, stageName)
	stagedDatabase := filepath.Join(stage, profile.DatabaseName)
	if final, err := os.Lstat(database); err == nil {
		if !final.Mode().IsRegular() || state.Phase == "copying" {
			return result, ErrImport
		}
		if state.Phase == "ready" {
			staged, err := os.Lstat(stagedDatabase)
			if err != nil || !staged.Mode().IsRegular() || !os.SameFile(final, staged) {
				return result, ErrImport
			}
		}
		if schema.VerifyTransferredProfile(ctx, database, vault) != nil {
			return result, ErrImport
		}
		state.Phase = "complete"
		if writeState(target, state) != nil {
			return result, ErrImport
		}
		if err := os.RemoveAll(stage); err != nil {
			return result, ErrImport
		}
		return Result{Version: state.Version, CredentialCount: len(state.Live), AlreadyImported: true}, nil
	} else if !os.IsNotExist(err) || state.Phase == "complete" {
		return result, ErrImport
	}
	// A failed write may have succeeded in the OS vault. Its durable list is
	// compensated before a retry; only import-owned new accounts are touched.
	if compensate(ctx, target, &state, vault, false) != nil {
		return result, ErrImport
	}
	if info, err := os.Lstat(stage); err == nil && !info.IsDir() {
		return result, ErrImport
	} else if err != nil && !os.IsNotExist(err) {
		return result, ErrImport
	}
	if os.RemoveAll(stage) != nil || os.Mkdir(stage, 0700) != nil {
		return result, ErrImport
	}
	state.Phase, state.Live, state.Version = "copying", nil, 0
	if writeState(target, state) != nil {
		return result, ErrImport
	}
	file, err := os.OpenFile(stagedDatabase, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return result, ErrImport
	}
	if file.Close() != nil {
		return result, ErrImport
	}
	if _, err := schema.CopyProfileSnapshot(ctx, filepath.Join(source, profile.DatabaseName), stagedDatabase); err != nil {
		return result, err
	}
	live, err := schema.PrepareTransferredProfile(ctx, stagedDatabase, vault, func(reference string) error {
		if !slices.Contains(state.References, reference) {
			state.References = append(state.References, reference)
		}
		return writeState(target, state)
	})
	if err != nil {
		// Keep the snapshot and intent list if cleanup cannot be confirmed.
		_ = compensate(context.WithoutCancel(ctx), target, &state, vault, false)
		return result, ErrImport
	}
	state.Live = live
	if compensate(ctx, target, &state, vault, true) != nil || schema.VerifyTransferredProfile(ctx, stagedDatabase, vault) != nil {
		return result, ErrImport
	}
	file, err = os.OpenFile(stagedDatabase, os.O_RDWR, 0)
	if err != nil {
		return result, ErrImport
	}
	syncErr, closeErr := file.Sync(), file.Close()
	if syncErr != nil || closeErr != nil {
		return result, ErrImport
	}
	state.Phase, state.Version = "ready", schema.CurrentVersion
	if writeState(target, state) != nil {
		return result, ErrImport
	}
	// A hard-link publication is atomic and cannot replace an existing file.
	// An interrupted publication is recognizable by SameFile on the retry.
	if os.Link(stagedDatabase, database) != nil {
		return result, ErrImport
	}
	state.Phase = "complete"
	if writeState(target, state) != nil {
		return result, ErrImport
	}
	if os.RemoveAll(stage) != nil {
		return result, ErrImport
	}
	return Result{Version: state.Version, CredentialCount: len(live)}, nil
}
