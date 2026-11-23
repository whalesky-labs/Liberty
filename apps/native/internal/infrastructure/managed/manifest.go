// Package managed installs the pinned Liberty runtime in an explicitly owned
// profile. The embedded sources are generated from the original desktop assets.
package managed

import (
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/url"
	"path"
	"regexp"
	"runtime"
	"strings"
)

var (
	ErrManifest  = errors.New("runtime_manifest_invalid")
	ErrAsset     = errors.New("runtime_asset_invalid")
	ErrDownload  = errors.New("runtime_download_failed")
	ErrChecksum  = errors.New("runtime_checksum_mismatch")
	ErrArchive   = errors.New("runtime_archive_unsafe")
	ErrResources = errors.New("runtime_resources_invalid")
)

type Manifest struct {
	RuntimeVersion    string                      `json:"runtimeVersion"`
	PythonVersion     string                      `json:"pythonVersion"`
	ModelSetVersion   string                      `json:"modelSetVersion"`
	ModelProfile      string                      `json:"modelProfile"`
	Sources           []Source                    `json:"downloadSources"`
	Platforms         []Platform                  `json:"platforms"`
	CandidateProfiles map[string]CandidateProfile `json:"candidateProfiles,omitempty"`
	ProductionInputs  *ProductionInputs           `json:"productionInputs,omitempty"`
}
type ProductionInputs struct {
	Builder RuntimeInputFile   `json:"builder"`
	Files   []RuntimeInputFile `json:"files"`
}
type CandidateProfile struct {
	RuntimeVersion string              `json:"runtimeVersion"`
	PythonVersion  string              `json:"pythonVersion"`
	Platforms      []CandidatePlatform `json:"platforms"`
	Files          []RuntimeInputFile  `json:"files"`
}
type CandidatePlatform struct {
	ID     string `json:"platformId"`
	Python *Asset `json:"pythonBundle"`
}
type RuntimeInputFile struct {
	Name   string `json:"fileName"`
	Bytes  int64  `json:"bytes"`
	SHA256 string `json:"sha256"`
}
type Source struct {
	ID            string `json:"sourceId"`
	Name          string `json:"nameZh"`
	PipIndex      string `json:"pipIndexUrl"`
	ModelEndpoint string `json:"modelEndpoint"`
}
type Asset struct {
	Name   string            `json:"fileName"`
	URLs   map[string]string `json:"urls"`
	SHA256 string            `json:"sha256"`
}
type Platform struct {
	ID                string   `json:"platformId"`
	Backend           string   `json:"asrBackend"`
	UnsupportedReason string   `json:"unsupportedReason"`
	Python            *Asset   `json:"pythonBundle"`
	PythonCandidates  []string `json:"pythonExecutableCandidates"`
	FFmpeg            *Asset   `json:"ffmpegBundle"`
	FFmpegCandidates  []string `json:"ffmpegExecutableCandidates"`
}
type ModelPin struct {
	Role     string `json:"role"`
	ID       string `json:"modelId"`
	Revision string `json:"revision"`
}

const (
	ProductionRuntimeProfile = "production"
	SecurityCandidateProfile = "security-candidate"
)

var revisionPattern = regexp.MustCompile(`^[a-fA-F0-9]{40}$`)
var pythonReleasePattern = regexp.MustCompile(`^(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)$`)

func runtimeProfileID(profileID string) (string, error) {
	switch profileID {
	case "", ProductionRuntimeProfile:
		return ProductionRuntimeProfile, nil
	case SecurityCandidateProfile:
		return profileID, nil
	default:
		return "", ErrManifest
	}
}

func runtimeInputNames() []string {
	return []string{
		"requirements-lock-darwin-aarch64.txt", "requirements-lock-darwin-x64.txt", "requirements-lock-windows-x64.txt",
		"funasr-1.3.9+liberty.local2-py3-none-any.whl", "METADATA.patch", "recipe.json", "provenance.json", "source.cdx.json",
	}
}

func validHash(value string) bool {
	decoded, err := hex.DecodeString(value)
	return err == nil && len(decoded) == 32
}
func httpsURL(value string) bool {
	u, err := url.Parse(value)
	return err == nil && u.Scheme == "https" && u.Hostname() != "" && u.User == nil && u.Fragment == ""
}
func immutableURL(value string) bool {
	lower := strings.ToLower(value)
	return httpsURL(value) && !strings.Contains(lower, "/latest/") && !strings.Contains(lower, "/getrelease/")
}
func LoadManifest() (Manifest, error) {
	return parseManifest([]byte(resources["runtime-manifest.json"]))
}
func parseManifest(data []byte) (Manifest, error) {
	var m Manifest
	if json.Unmarshal(data, &m) != nil || m.RuntimeVersion == "" || m.PythonVersion == "" || m.ModelSetVersion == "" || m.ModelProfile == "" || len(m.Platforms) == 0 {
		return m, ErrManifest
	}
	seen := map[string]bool{}
	for _, p := range m.Platforms {
		if seen[p.ID] || !safeName(p.ID) {
			return m, ErrManifest
		}
		seen[p.ID] = true
		if p.UnsupportedReason == "" && (p.Python == nil || p.FFmpeg == nil) {
			return m, ErrManifest
		}
		for _, a := range []*Asset{p.Python, p.FFmpeg} {
			if a == nil {
				continue
			}
			if !safeName(a.Name) || !validHash(a.SHA256) || len(a.URLs) == 0 {
				return m, ErrManifest
			}
			for _, u := range a.URLs {
				if !immutableURL(u) {
					return m, ErrManifest
				}
			}
		}
		for _, candidate := range append(append([]string{}, p.PythonCandidates...), p.FFmpegCandidates...) {
			if _, err := safeRelative(candidate); err != nil {
				return m, ErrManifest
			}
		}
	}
	seen = map[string]bool{}
	for _, s := range m.Sources {
		if !safeName(s.ID) || seen[s.ID] || !httpsURL(s.PipIndex) || !httpsURL(s.ModelEndpoint) {
			return m, ErrManifest
		}
		seen[s.ID] = true
	}
	for profileID, candidate := range m.CandidateProfiles {
		if profileID != SecurityCandidateProfile || !safeName(candidate.RuntimeVersion) || candidate.RuntimeVersion == m.RuntimeVersion || !pythonReleasePattern.MatchString(candidate.PythonVersion) || len(candidate.Platforms) != 3 || len(candidate.Files) != len(runtimeInputNames()) {
			return m, ErrManifest
		}
		platforms := map[string]bool{}
		for _, override := range candidate.Platforms {
			original, err := m.Platform(override.ID)
			if err != nil || original.UnsupportedReason != "" || platforms[override.ID] || override.Python == nil {
				return m, ErrManifest
			}
			platforms[override.ID] = true
			asset := override.Python
			if !safeName(asset.Name) || !validHash(asset.SHA256) || len(asset.URLs) == 0 {
				return m, ErrManifest
			}
			for _, address := range asset.URLs {
				if !immutableURL(address) {
					return m, ErrManifest
				}
			}
		}
		for _, platformID := range []string{"darwin-aarch64", "darwin-x64", "windows-x64"} {
			if !platforms[platformID] {
				return m, ErrManifest
			}
		}
		files := map[string]bool{}
		for _, file := range candidate.Files {
			if !safeName(file.Name) || files[file.Name] || !validHash(file.SHA256) || file.Bytes < 1 || file.Bytes > 2*1024*1024 {
				return m, ErrManifest
			}
			files[file.Name] = true
		}
		for _, name := range runtimeInputNames() {
			if !files[name] {
				return m, ErrManifest
			}
		}
	}
	if m.ProductionInputs != nil {
		pins := m.ProductionInputs
		if pins.Builder.Name != "build_vendor_wheel.py" || !validInputFile(pins.Builder) || !validRuntimeInputFiles(pins.Files) {
			return m, ErrManifest
		}
	}
	return m, nil
}

func validInputFile(file RuntimeInputFile) bool {
	return safeName(file.Name) && validHash(file.SHA256) && file.Bytes > 0 && file.Bytes <= 2*1024*1024
}

func validRuntimeInputFiles(files []RuntimeInputFile) bool {
	if len(files) != len(runtimeInputNames()) {
		return false
	}
	seen := map[string]bool{}
	for _, file := range files {
		if !validInputFile(file) || seen[file.Name] {
			return false
		}
		seen[file.Name] = true
	}
	for _, name := range runtimeInputNames() {
		if !seen[name] {
			return false
		}
	}
	return true
}

func (m Manifest) withProfile(profileID string) (Manifest, *CandidateProfile, error) {
	profileID, err := runtimeProfileID(profileID)
	if err != nil {
		return Manifest{}, nil, err
	}
	if profileID == ProductionRuntimeProfile {
		return m, nil, nil
	}
	candidate, ok := m.CandidateProfiles[profileID]
	if !ok {
		return Manifest{}, nil, ErrManifest
	}
	m.RuntimeVersion, m.PythonVersion = candidate.RuntimeVersion, candidate.PythonVersion
	m.Platforms = append([]Platform(nil), m.Platforms...)
	for index := range m.Platforms {
		for _, override := range candidate.Platforms {
			if m.Platforms[index].ID == override.ID {
				m.Platforms[index].Python = override.Python
			}
		}
	}
	return m, &candidate, nil
}
func (m Manifest) Platform(id string) (Platform, error) {
	for _, p := range m.Platforms {
		if p.ID == id {
			return p, nil
		}
	}
	return Platform{}, ErrManifest
}
func (m Manifest) Source(id string) (Source, error) {
	for _, s := range m.Sources {
		if s.ID == strings.TrimSpace(id) {
			return s, nil
		}
	}
	return Source{}, ErrManifest
}
func (a Asset) URL(source string) (string, error) {
	u := a.URLs[source]
	if u == "" {
		u = a.URLs["official"]
	}
	if !immutableURL(u) {
		return "", ErrAsset
	}
	return u, nil
}
func CurrentPlatform() string {
	switch runtime.GOOS + "/" + runtime.GOARCH {
	case "darwin/arm64":
		return "darwin-aarch64"
	case "darwin/amd64":
		return "darwin-x64"
	case "windows/amd64":
		return "windows-x64"
	case "windows/386":
		return "windows-x86"
	}
	return "unsupported"
}
func modelPins() ([]ModelPin, error) {
	var pins []ModelPin
	if json.Unmarshal([]byte(resources["models.json"]), &pins) != nil || len(pins) != 4 {
		return nil, ErrManifest
	}
	return pins, nil
}
func safeName(value string) bool {
	if value == "" || value == "." || value == ".." || strings.ContainsAny(value, "/\\:\x00") || strings.TrimSpace(value) != value || strings.TrimRight(value, " .") != value {
		return false
	}
	stem := strings.ToUpper(strings.SplitN(value, ".", 2)[0])
	return stem != "CON" && stem != "PRN" && stem != "AUX" && stem != "NUL" && !(len(stem) == 4 && (strings.HasPrefix(stem, "COM") || strings.HasPrefix(stem, "LPT")) && stem[3] >= '1' && stem[3] <= '9')
}
func safeRelative(value string) (string, error) {
	if value == "" || strings.ContainsAny(value, "\\:\x00") || strings.HasPrefix(value, "/") {
		return "", ErrArchive
	}
	for _, part := range strings.Split(value, "/") {
		if part == ".." {
			return "", ErrArchive
		}
	}
	normalized := path.Clean(value)
	if normalized == "." || normalized == ".." || strings.HasPrefix(normalized, "../") {
		return "", ErrArchive
	}
	for _, part := range strings.Split(normalized, "/") {
		if !safeName(part) {
			return "", ErrArchive
		}
	}
	return normalized, nil
}
