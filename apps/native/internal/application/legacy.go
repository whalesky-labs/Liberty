package application

import (
	"context"
	"liberty.local/liberty/internal/domain"
)

// LegacyJob is internal migration data. Private paths never become UI DTOs.
type LegacyJob struct {
	Result     domain.Result
	Sources    []LegacySourceFile
	PythonPath *string
	RunnerPath *string
}
type LegacySourceFile struct {
	Metadata domain.SourceFile
	Path     *string
}
type LegacyImportResult struct {
	Imported, Skipped int
	AlreadyImported   bool
}
type LegacyJobSource interface {
	Each(context.Context, func(LegacyJob) error) (skipped int, err error)
}
