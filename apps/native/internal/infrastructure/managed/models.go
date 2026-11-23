package managed

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"liberty.local/liberty/internal/domain"
	"liberty.local/liberty/internal/infrastructure/runner"
)

type ModelFile struct {
	Path     string `json:"path"`
	Revision string `json:"revision"`
	SHA256   string `json:"sha256"`
	Size     int64  `json:"size"`
}
type AcquiredModel struct {
	Role         string      `json:"role"`
	ID           string      `json:"modelId"`
	Revision     string      `json:"revision"`
	RelativePath string      `json:"relativePath"`
	Files        []ModelFile `json:"files"`
}
type AcquiredSet struct {
	Version string          `json:"modelSetVersion"`
	Profile string          `json:"modelProfile"`
	Models  []AcquiredModel `json:"models"`
}

const acquiredMarker = "models-acquired.json"

func modelRelative(id string) (string, error) {
	parts := strings.Split(id, "/")
	if len(parts) != 2 {
		return "", ErrManifest
	}
	for _, part := range parts {
		if !safeName(part) {
			return "", ErrManifest
		}
	}
	return path.Join("modelscope", "models", id), nil
}
func (i *Installer) modelFiles(ctx context.Context, endpoint string, pin ModelPin) ([]ModelFile, error) {
	if !httpsURL(endpoint) || !revisionPattern.MatchString(pin.Revision) {
		return nil, ErrManifest
	}
	if _, err := modelRelative(pin.ID); err != nil {
		return nil, err
	}
	address := strings.TrimRight(endpoint, "/") + "/api/v1/models/" + pin.ID + "/repo/files?" + url.Values{"Revision": {pin.Revision}, "Recursive": {"true"}}.Encode()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, address, nil)
	if err != nil {
		return nil, ErrDownload
	}
	response, err := i.Download.client.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, ErrDownload
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return nil, ErrDownload
	}
	data, err := io.ReadAll(io.LimitReader(response.Body, 16*1024*1024+1))
	if err != nil || len(data) > 16*1024*1024 {
		return nil, ErrDownload
	}
	var result struct {
		Success bool
		Code    int
		Data    struct {
			Files []struct {
				Path, Revision, Sha256, Type string
				Size                         int64
			}
		}
	}
	if json.Unmarshal(data, &result) != nil || !result.Success || result.Code != 200 {
		return nil, ErrDownload
	}
	files := []ModelFile{}
	seen := map[string]bool{}
	total := int64(0)
	for _, f := range result.Data.Files {
		if f.Type != "blob" {
			continue
		}
		normalized, err := safeRelative(f.Path)
		if err != nil || seen[strings.ToLower(normalized)] || !revisionPattern.MatchString(f.Revision) || !validHash(f.Sha256) || f.Size < 0 || f.Size > maxEntryBytes || total > maxAssetBytes-f.Size {
			return nil, ErrManifest
		}
		seen[strings.ToLower(normalized)] = true
		total += f.Size
		files = append(files, ModelFile{normalized, f.Revision, strings.ToLower(f.Sha256), f.Size})
	}
	if len(files) == 0 || len(files) > maxEntries {
		return nil, ErrManifest
	}
	sort.Slice(files, func(a, b int) bool { return files[a].Path < files[b].Path })
	return files, nil
}
func (i *Installer) AcquireModels(ctx context.Context, generation int64, sourceID string, log func(string) error) error {
	if i.Platform.UnsupportedReason != "" {
		return domain.ErrRuntimeUnsupported
	}
	if i.Manifest.ModelProfile != "paraformer" {
		return ErrManifest
	}
	source, err := i.Manifest.Source(sourceID)
	if err != nil {
		return err
	}
	directory, err := i.generation("model", generation)
	if err != nil {
		return err
	}
	root, err := os.OpenRoot(directory)
	if err != nil {
		return ErrResources
	}
	defer root.Close()
	if err := root.Remove(acquiredMarker); err != nil && !os.IsNotExist(err) {
		return ErrResources
	}
	pins, err := modelPins()
	if err != nil {
		return err
	}
	set := AcquiredSet{Version: i.Manifest.ModelSetVersion, Profile: i.Manifest.ModelProfile, Models: []AcquiredModel{}}
	total, entries := int64(0), 0
	for _, pin := range pins {
		files, err := i.modelFiles(ctx, source.ModelEndpoint, pin)
		if err != nil {
			return err
		}
		relative, err := modelRelative(pin.ID)
		if err != nil {
			return err
		}
		if log != nil {
			if err := log("runtime_acquiring_model=" + pin.Role); err != nil {
				return err
			}
		}
		for _, file := range files {
			entries++
			if entries > maxEntries || total > maxAssetBytes-file.Size {
				return ErrManifest
			}
			total += file.Size
			parent := path.Join("models", relative, path.Dir(file.Path))
			if root.MkdirAll(parent, 0700) != nil {
				return ErrResources
			}
			target, err := root.OpenRoot(parent)
			if err != nil {
				return ErrResources
			}
			address := strings.TrimRight(source.ModelEndpoint, "/") + "/api/v1/models/" + pin.ID + "/repo?" + url.Values{"Revision": {file.Revision}, "FilePath": {file.Path}}.Encode()
			err = i.Download.Fetch(ctx, target, path.Base(file.Path), address, file.SHA256, &file.Size, func(done, total int64) error {
				if log != nil {
					return log("runtime_model=" + pin.Role + " bytes=" + strconv.FormatInt(done, 10) + "/" + strconv.FormatInt(total, 10))
				}
				return nil
			})
			target.Close()
			if err != nil {
				return err
			}
		}
		set.Models = append(set.Models, AcquiredModel{pin.Role, pin.ID, pin.Revision, relative, files})
	}
	if err := i.validateModelSet(root, set, true); err != nil {
		return err
	}
	data, err := json.Marshal(set)
	if err != nil {
		return ErrResources
	}
	return atomicJSON(root, acquiredMarker, data)
}
func (i *Installer) validateModelSet(root *os.Root, set AcquiredSet, hashes bool) error {
	pins, err := modelPins()
	if err != nil {
		return err
	}
	if set.Version != i.Manifest.ModelSetVersion || set.Profile != i.Manifest.ModelProfile || len(set.Models) != len(pins) {
		return ErrManifest
	}
	roles := map[string]bool{}
	total, entries := int64(0), 0
	for _, model := range set.Models {
		var pin *ModelPin
		for _, candidate := range pins {
			if candidate.Role == model.Role {
				copy := candidate
				pin = &copy
			}
		}
		relative, err := modelRelative(model.ID)
		if err != nil || pin == nil || roles[model.Role] || model.ID != pin.ID || model.Revision != pin.Revision || model.RelativePath != relative || len(model.Files) == 0 {
			return ErrManifest
		}
		roles[model.Role] = true
		seen := map[string]bool{}
		nonzero := false
		for _, file := range model.Files {
			name, err := safeRelative(file.Path)
			entries++
			if err != nil || seen[strings.ToLower(name)] || !validHash(file.SHA256) || !revisionPattern.MatchString(file.Revision) || file.Size < 0 || file.Size > maxEntryBytes || entries > maxEntries || total > maxAssetBytes-file.Size {
				return ErrManifest
			}
			total += file.Size
			seen[strings.ToLower(name)] = true
			nonzero = nonzero || file.Size > 0
			full := path.Join("models", relative, name)
			info, err := root.Lstat(full)
			if err != nil || !info.Mode().IsRegular() || info.Size() != file.Size {
				return ErrAsset
			}
			if hashes && !checksum(root, full, file.SHA256, &file.Size) {
				return ErrChecksum
			}
		}
		if !nonzero {
			return ErrManifest
		}
	}
	return nil
}
func (i *Installer) acquiredSet(generation int64) (*os.Root, AcquiredSet, error) {
	root, err := i.openGeneration("model", generation)
	if err != nil {
		return nil, AcquiredSet{}, ErrResources
	}
	file, err := root.Open(acquiredMarker)
	if err != nil {
		root.Close()
		return nil, AcquiredSet{}, ErrResources
	}
	data, err := io.ReadAll(io.LimitReader(file, 16*1024*1024+1))
	file.Close()
	var set AcquiredSet
	if err != nil || len(data) > 16*1024*1024 || json.Unmarshal(data, &set) != nil {
		root.Close()
		return nil, set, ErrManifest
	}
	return root, set, nil
}
func (i *Installer) ModelsAcquired(generation int64) bool {
	root, set, err := i.acquiredSet(generation)
	if err != nil {
		return false
	}
	defer root.Close()
	return i.validateModelSet(root, set, false) == nil
}
func (i *Installer) ModelRetryable(generation int64) bool {
	if generation < 1 {
		return false
	}
	root, err := i.openGeneration("model", generation)
	if err != nil {
		return false
	}
	defer root.Close()
	info, err := root.Stat("models")
	return err == nil && info.IsDir()
}

func (i *Installer) ResolveModels(ctx context.Context, artifact domain.RuntimeArtifact) (runner.ModelPaths, error) {
	if err := ctx.Err(); err != nil {
		return runner.ModelPaths{}, err
	}
	if i.Platform.UnsupportedReason != "" {
		return runner.ModelPaths{}, domain.ErrRuntimeUnsupported
	}
	if artifact.Version != i.Manifest.ModelSetVersion {
		return runner.ModelPaths{}, ErrManifest
	}
	pins, err := modelPins()
	if err != nil {
		return runner.ModelPaths{}, err
	}
	models := runner.ModelPaths{Profile: i.Manifest.ModelProfile}
	seen := map[string]bool{}
	for _, pin := range pins {
		relative, err := modelRelative(pin.ID)
		if err != nil || seen[pin.Role] || !revisionPattern.MatchString(pin.Revision) {
			return runner.ModelPaths{}, ErrManifest
		}
		seen[pin.Role] = true
		model := filepath.Join(artifact.Path, filepath.FromSlash(relative))
		switch pin.Role {
		case "model":
			models.ASR = model
		case "vad":
			models.VAD = model
		case "punc":
			models.Punctuation = model
		case "spk":
			models.Speaker = model
		default:
			return runner.ModelPaths{}, ErrManifest
		}
	}
	if _, err := models.Environment(artifact.Path); err != nil {
		return runner.ModelPaths{}, ErrAsset
	}
	return models, nil
}

func (i *Installer) ValidateModels(ctx context.Context, generation int64, python string, log func(string) error) (domain.RuntimeArtifact, error) {
	root, set, err := i.acquiredSet(generation)
	if err != nil {
		return domain.RuntimeArtifact{}, err
	}
	defer root.Close()
	if err := i.validateModelSet(root, set, true); err != nil {
		if errors.Is(err, ErrChecksum) {
			// A same-size corrupted cache passed the cheap readiness probe. Retire
			// its acquisition marker so retry reacquires only invalid files, rather
			// than repeatedly trying to warm up the same damaged bytes offline.
			if removeErr := root.Remove(acquiredMarker); removeErr != nil && !os.IsNotExist(removeErr) {
				return domain.RuntimeArtifact{}, ErrResources
			}
		}
		return domain.RuntimeArtifact{}, err
	}
	directory, err := i.generation("model", generation)
	if err != nil {
		return domain.RuntimeArtifact{}, err
	}
	modelsRoot := filepath.Join(directory, "models")
	models, err := i.ResolveModels(ctx, domain.RuntimeArtifact{Version: i.Manifest.ModelSetVersion, Path: modelsRoot})
	if err != nil {
		return domain.RuntimeArtifact{}, err
	}
	modelEnvironment, err := models.Environment(modelsRoot)
	if err != nil {
		return domain.RuntimeArtifact{}, ErrAsset
	}
	env := i.environment()
	for key, value := range modelEnvironment {
		env[key] = value
	}
	for key, child := range map[string]string{"MODELSCOPE_CACHE": "modelscope", "HF_HOME": "huggingface", "TORCH_HOME": "torch"} {
		env[key] = filepath.Join(modelsRoot, child)
	}
	for _, key := range []string{"MODELSCOPE_OFFLINE", "HF_HUB_OFFLINE", "TRANSFORMERS_OFFLINE"} {
		env[key] = "1"
	}
	runCtx, cancel := context.WithTimeout(ctx, 10*time.Minute)
	err = i.Commands.Run(runCtx, python, []string{filepath.Join(i.Resources, "runtime_warmup.py"), "--models-root", modelsRoot, "--validate-only"}, directory, env, log)
	cancel()
	if err != nil {
		return domain.RuntimeArtifact{}, err
	}
	runCtx, cancel = context.WithTimeout(ctx, time.Minute)
	err = i.Commands.Run(runCtx, python, []string{filepath.Join(i.Resources, "publish_model_cache.py"), filepath.Join(directory, acquiredMarker), modelsRoot}, directory, i.environment(), log)
	cancel()
	if err != nil {
		return domain.RuntimeArtifact{}, err
	}
	return i.publish(root, "model", generation, modelsRoot, i.Manifest.ModelSetVersion)
}
