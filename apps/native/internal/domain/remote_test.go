package domain

import "testing"

func TestRemoteCapabilitiesValidateOriginalContract(t *testing.T) {
	base := func() RemoteCapabilities {
		return RemoteCapabilities{Protocol: "liberty-meeting", ProtocolVersion: 1, JobSchemaVersion: 1, ServiceVersion: " 1 ", Operations: []string{}}
	}
	for _, mutate := range []func(*RemoteCapabilities){func(c *RemoteCapabilities) { c.Protocol = "other" }, func(c *RemoteCapabilities) { c.ProtocolVersion = 2 }, func(c *RemoteCapabilities) { c.Operations = []string{"unknown"} }, func(c *RemoteCapabilities) { c.Operations = []string{"jobs.list", "jobs.list"} }, func(c *RemoteCapabilities) { c.Operations = []string{"jobs.create"} }, func(c *RemoteCapabilities) { c.JobCreate = &RemoteJobCreate{UploadMode: "multipart"} }, func(c *RemoteCapabilities) { c.Exports = &RemoteExports{} }} {
		c := base()
		mutate(&c)
		if c.Normalize() == nil {
			t.Fatal("accepted invalid capabilities")
		}
	}
	c := base()
	c.Operations = []string{"jobs.create", "exports.generate"}
	c.JobCreate = &RemoteJobCreate{UploadMode: "chunked", MaxFiles: 1, MaxBytesPerFile: 1024, Extensions: []string{" WAV "}}
	c.Exports = &RemoteExports{Formats: []string{" DOCX ", " "}}
	if c.Normalize() != nil || c.ServiceVersion != "1" || c.JobCreate.Extensions[0] != "wav" || len(c.Exports.Formats) != 1 || c.Exports.Formats[0] != "docx" {
		t.Fatal("normalization mismatch")
	}
}
