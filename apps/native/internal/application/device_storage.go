package application

import (
	"context"
	"errors"
	"math"
	"path/filepath"
)

var ErrDeviceStorage = errors.New("device_storage_unavailable")

type DeviceStorageReport struct {
	Directory string
	Total     uint64
	Database  uint64
	Jobs      uint64
	Runtime   uint64
	Other     uint64
}

type DeviceStorageScanner interface {
	Measure(context.Context, string) (DeviceStorageReport, error)
}

type DeviceStorage struct {
	directory string
	scanner   DeviceStorageScanner
}

func NewDeviceStorage(directory string, scanner DeviceStorageScanner) *DeviceStorage {
	return &DeviceStorage{directory: directory, scanner: scanner}
}

func (s *DeviceStorage) Usage(ctx context.Context) (DeviceStorageReport, error) {
	if s == nil || s.scanner == nil || !filepath.IsAbs(s.directory) {
		return DeviceStorageReport{}, ErrDeviceStorage
	}
	if err := ctx.Err(); err != nil {
		return DeviceStorageReport{}, err
	}
	report, err := s.scanner.Measure(ctx, s.directory)
	if err != nil {
		if ctx.Err() != nil {
			return DeviceStorageReport{}, ctx.Err()
		}
		return DeviceStorageReport{}, ErrDeviceStorage
	}
	parts := [...]uint64{report.Database, report.Jobs, report.Runtime, report.Other}
	var total uint64
	for _, part := range parts {
		if total > math.MaxUint64-part {
			return DeviceStorageReport{}, ErrDeviceStorage
		}
		total += part
	}
	if report.Total != total {
		return DeviceStorageReport{}, ErrDeviceStorage
	}
	return report, nil
}
