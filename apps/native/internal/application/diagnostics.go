package application

import (
	"context"
	"liberty.local/liberty/internal/domain"
)

const diagnosticLogLimit = 4 << 20

type DiagnosticDatabase interface {
	SchemaVersion(context.Context) (int64, error)
}
type DiagnosticRuntime interface {
	Status(context.Context) (domain.RuntimeOverview, error)
}

// DiagnosticLog sources must emit only typed, redacted native diagnostic
// events. Business ledgers, arbitrary files and process logs are not sources.
type DiagnosticLog interface {
	DiagnosticLog(context.Context) ([]byte, error)
}
type Diagnostics struct {
	version, platform string
	database          DiagnosticDatabase
	runtime           DiagnosticRuntime
	log               DiagnosticLog
}

func NewDiagnostics(version, platform string, database DiagnosticDatabase, runtime DiagnosticRuntime, log DiagnosticLog) *Diagnostics {
	return &Diagnostics{version, platform, database, runtime, log}
}
func (d *Diagnostics) Report(ctx context.Context) (domain.DiagnosticReport, error) {
	if err := ctx.Err(); err != nil {
		return domain.DiagnosticReport{}, err
	}
	version, err := d.database.SchemaVersion(ctx)
	if err != nil {
		return domain.DiagnosticReport{}, domain.ErrDiagnostics
	}
	report := domain.DiagnosticReport{AppVersion: d.version, SchemaVersion: version, RuntimeStatus: "unknown", SupportedPlatforms: domain.DiagnosticPlatforms(), SecurityBaseline: domain.SecurityBaseline{NativeUI: true, ScopedCapabilities: true, CredentialStoreRequired: true}}
	for _, platform := range report.SupportedPlatforms {
		if platform.ID == d.platform {
			report.CurrentPlatform = &platform
			break
		}
	}
	status, err := d.runtime.Status(ctx)
	if err == nil {
		switch status.Status {
		case "unsupported", "ready", "installing", "failed", "missing":
			report.RuntimeStatus = status.Status
		}
	}
	if d.log != nil {
		data, err := d.log.DiagnosticLog(ctx)
		report.DesktopPetLogAvailable = err == nil && len(data) > 0 && len(data) <= diagnosticLogLimit
	}
	if ctx.Err() != nil {
		return domain.DiagnosticReport{}, ctx.Err()
	}
	return report, nil
}
func (d *Diagnostics) ExportPetLog(ctx context.Context, publish func([]byte) error) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if d.log == nil || publish == nil {
		return domain.ErrDiagnosticLog
	}
	data, err := d.log.DiagnosticLog(ctx)
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if err != nil || len(data) == 0 || len(data) > diagnosticLogLimit {
		return domain.ErrDiagnosticLog
	}
	return publish(data)
}
