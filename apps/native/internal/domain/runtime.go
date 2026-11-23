package domain

import (
	"errors"
	"strings"
)

var (
	ErrRuntime            = errors.New("runtime_not_ready")
	ErrRuntimeComponent   = errors.New("runtime_component_invalid")
	ErrRuntimeSource      = errors.New("runtime_source_invalid")
	ErrRuntimeStale       = errors.New("runtime_operation_stale")
	ErrRuntimeUnsupported = errors.New("runtime_platform_unsupported")
)

type RuntimeArtifact struct {
	GenerationID string `json:"generationId"`
	Version      string `json:"artifactVersion"`
	Path         string `json:"resolvedPath"`
}
type RuntimeOperation struct {
	Kind       string  `json:"kind"`
	Generation int64   `json:"generation"`
	Phase      string  `json:"phase"`
	Progress   *uint32 `json:"progress"`
	LastError  *string `json:"lastError"`
}
type RuntimeComponent struct {
	Component    string           `json:"component"`
	Source       *string          `json:"source"`
	Availability string           `json:"availability"`
	Artifact     *RuntimeArtifact `json:"activeArtifact"`
	Operation    RuntimeOperation `json:"operation"`
	UpdatedAt    string           `json:"updatedAt"`
}

func NewRuntimeComponent(component, source, now string) RuntimeComponent {
	var selected *string
	if component != "model" {
		selected = &source
	}
	return RuntimeComponent{Component: component, Source: selected, Availability: "unavailable", Operation: RuntimeOperation{Kind: "idle", Phase: "idle"}, UpdatedAt: now}
}
func RuntimeKey(component, source string) error {
	if component != "python" && component != "ffmpeg" && component != "model" {
		return ErrRuntimeComponent
	}
	if source != "managed" && source != "system" || (component == "model" && source != "managed") {
		return ErrRuntimeSource
	}
	return nil
}
func (s RuntimeComponent) SourceName() string {
	if s.Source == nil {
		return "managed"
	}
	return *s.Source
}
func (s RuntimeComponent) Ready() bool { return s.Availability == "ready" && s.Artifact != nil }
func (o RuntimeOperation) Active() bool {
	switch o.Kind {
	case "detecting", "waiting_for_python", "downloading", "installing", "validating":
		return true
	}
	return false
}

type RuntimeSettings struct {
	PythonSource, FFmpegSource         string
	PythonPath, FFmpegPath, RunnerPath string
	DownloadSource, Device             string
	Concurrency, Threads, BatchSeconds uint32
}

func DefaultRuntimeSettings() RuntimeSettings {
	return RuntimeSettings{PythonSource: "managed", FFmpegSource: "managed", Device: "auto", Concurrency: 2, BatchSeconds: 300}
}
func (s RuntimeSettings) Source(component string) string {
	switch component {
	case "python":
		return s.PythonSource
	case "ffmpeg":
		return s.FFmpegSource
	default:
		return "managed"
	}
}

type RuntimeOverview struct {
	PlatformID     string           `json:"platformId"`
	RuntimeVersion string           `json:"runtimeVersion"`
	PythonVersion  string           `json:"pythonVersion"`
	Status         string           `json:"status"`
	PythonPath     *string          `json:"pythonExecutablePath"`
	ModelsRoot     *string          `json:"modelsRoot"`
	InstallRoot    *string          `json:"installRoot"`
	FFmpegPath     *string          `json:"ffmpegPath"`
	LastError      *string          `json:"lastError"`
	InstalledAt    *string          `json:"installedAt"`
	UpdatedAt      string           `json:"updatedAt"`
	LastLogPath    *string          `json:"lastLogPath"`
	Python         RuntimeComponent `json:"python"`
	FFmpeg         RuntimeComponent `json:"ffmpeg"`
	Models         RuntimeComponent `json:"models"`
	ShellReady     bool             `json:"shellReady"`
}

type RuntimeSourceOption struct{ ID, Name string }
type RuntimePanel struct {
	Overview       RuntimeOverview
	DownloadSource string
	Sources        []RuntimeSourceOption
	Logs           map[string]string
}

func (panel RuntimePanel) InstallLog() string {
	var sections []string
	for _, component := range []string{"python", "ffmpeg", "model"} {
		if content := panel.Logs[component]; strings.TrimSpace(content) != "" {
			sections = append(sections, "["+component+"]\n"+content)
		}
	}
	return strings.Join(sections, "\n")
}

func (s *RuntimeOverview) Aggregate() {
	s.ShellReady = s.Python.Ready() && s.FFmpeg.Ready() && s.Models.Ready()
	s.PythonPath, s.FFmpegPath, s.ModelsRoot = nil, nil, nil
	if s.Python.Ready() {
		value := s.Python.Artifact.Path
		s.PythonPath = &value
	}
	if s.FFmpeg.Ready() {
		value := s.FFmpeg.Artifact.Path
		s.FFmpegPath = &value
	}
	if s.Models.Ready() {
		value := s.Models.Artifact.Path
		s.ModelsRoot = &value
	}
	unsupported, active, failed := false, false, false
	s.LastError = nil
	for _, component := range []RuntimeComponent{s.Python, s.FFmpeg, s.Models} {
		unsupported = unsupported || component.Availability == "unsupported"
		active = active || component.Operation.Active()
		failed = failed || component.Operation.Kind == "failed"
		if s.LastError == nil {
			s.LastError = component.Operation.LastError
		}
	}
	switch {
	case unsupported:
		s.Status = "unsupported"
	case s.ShellReady:
		s.Status = "ready"
	case active:
		s.Status = "installing"
	case failed:
		s.Status = "failed"
	default:
		s.Status = "missing"
	}
}
