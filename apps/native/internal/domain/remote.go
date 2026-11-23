package domain

import (
	"errors"
	"strings"
)

// Remote errors intentionally carry no URL, credential or server response text.
var ErrRemote = errors.New("remote_service_unavailable")
var ErrRemoteUnsupported = errors.New("remote_operation_unsupported")

type RemoteCapabilities struct {
	Protocol         string           `json:"protocol"`
	ProtocolVersion  uint32           `json:"protocolVersion"`
	JobSchemaVersion uint32           `json:"jobSchemaVersion"`
	ServiceVersion   string           `json:"serviceVersion"`
	Operations       []string         `json:"operations"`
	JobCreate        *RemoteJobCreate `json:"jobCreate,omitempty"`
	Exports          *RemoteExports   `json:"exports,omitempty"`
}
type RemoteJobCreate struct {
	UploadMode      string   `json:"uploadMode"`
	MaxFiles        uint64   `json:"maxFiles"`
	MaxBytesPerFile uint64   `json:"maxBytesPerFile"`
	Extensions      []string `json:"extensions"`
}
type RemoteExports struct {
	Formats []string `json:"formats"`
}

func (c RemoteCapabilities) Supports(operation string) bool {
	for _, candidate := range c.Operations {
		if operation == candidate {
			return true
		}
	}
	return false
}
func (c *RemoteCapabilities) Normalize() error {
	if c.Protocol != "liberty-meeting" || c.ProtocolVersion != 1 || c.JobSchemaVersion != 1 || strings.TrimSpace(c.ServiceVersion) == "" || c.Operations == nil {
		return ErrRemote
	}
	c.ServiceVersion = strings.TrimSpace(c.ServiceVersion)
	seen := map[string]bool{}
	for _, operation := range c.Operations {
		switch operation {
		case "jobs.list", "jobs.read", "jobs.result.read", "jobs.create", "jobs.retry", "jobs.delete", "transcript.speakers.rename", "summary.runs.read", "summary.runs.write", "exports.generate":
		default:
			return ErrRemote
		}
		if seen[operation] {
			return ErrRemote
		}
		seen[operation] = true
	}
	if create := c.JobCreate; create != nil {
		if create.UploadMode != "multipart" && create.UploadMode != "chunked" || create.MaxFiles == 0 || create.MaxBytesPerFile == 0 || len(create.Extensions) == 0 {
			return ErrRemote
		}
		for i, extension := range create.Extensions {
			extension = strings.TrimSpace(extension)
			if extension == "" {
				return ErrRemote
			}
			create.Extensions[i] = strings.ToLower(extension)
		}
	}
	if c.Supports("jobs.create") && c.JobCreate == nil {
		return ErrRemote
	}
	if c.Exports != nil {
		if c.Exports.Formats == nil {
			return ErrRemote
		}
		formats := []string{}
		for _, f := range c.Exports.Formats {
			if f = strings.TrimSpace(f); f != "" {
				formats = append(formats, strings.ToLower(f))
			}
		}
		c.Exports.Formats = formats
	}
	return nil
}

func RemoteIdentifier(value string) (string, error) {
	value = strings.TrimSpace(value)
	if value == "" || len(value) > 256 {
		return "", ErrRemote
	}
	return value, nil
}
