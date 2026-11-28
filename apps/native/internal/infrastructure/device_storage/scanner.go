package device_storage

import (
	"context"
	"errors"
	"io/fs"
	"math"
	"os"
	"path/filepath"
	"strings"

	"liberty.local/liberty/internal/application"
)

var ErrMeasure = errors.New("device_storage_measure_failed")

type Scanner struct{}

func (Scanner) Measure(ctx context.Context, directory string) (application.DeviceStorageReport, error) {
	var report application.DeviceStorageReport
	if err := ctx.Err(); err != nil {
		return report, err
	}
	if !filepath.IsAbs(directory) {
		return report, ErrMeasure
	}
	canonical, err := filepath.EvalSymlinks(directory)
	if err != nil {
		return report, ErrMeasure
	}
	info, err := os.Stat(canonical)
	if err != nil || !info.IsDir() {
		return report, ErrMeasure
	}
	report.Directory = canonical
	err = filepath.WalkDir(canonical, func(name string, entry fs.DirEntry, walkErr error) error {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return ctxErr
		}
		if walkErr != nil {
			return ErrMeasure
		}
		if name == canonical || entry.IsDir() || entry.Type()&fs.ModeType != 0 {
			return nil
		}
		file, err := entry.Info()
		if err != nil || file.Size() < 0 {
			return ErrMeasure
		}
		size := uint64(file.Size())
		if report.Total > math.MaxUint64-size {
			return ErrMeasure
		}
		report.Total += size
		relative, err := filepath.Rel(canonical, name)
		if err != nil {
			return ErrMeasure
		}
		category := storageCategory(filepath.ToSlash(relative))
		var subtotal *uint64
		switch category {
		case "database":
			subtotal = &report.Database
		case "jobs":
			subtotal = &report.Jobs
		case "runtime":
			subtotal = &report.Runtime
		default:
			subtotal = &report.Other
		}
		if *subtotal > math.MaxUint64-size {
			return ErrMeasure
		}
		*subtotal += size
		return nil
	})
	if err != nil {
		if ctx.Err() != nil {
			return application.DeviceStorageReport{}, ctx.Err()
		}
		return application.DeviceStorageReport{}, ErrMeasure
	}
	return report, nil
}

func storageCategory(name string) string {
	if !strings.Contains(name, "/") {
		switch name {
		case "liberty.sqlite3", "liberty.sqlite3-wal", "liberty.sqlite3-shm", "liberty.sqlite3-journal":
			return "database"
		}
	}
	first, _, _ := strings.Cut(name, "/")
	switch first {
	case "jobs":
		return "jobs"
	case "runtime":
		return "runtime"
	default:
		return "other"
	}
}
