package managed

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
)

func modelInstaller(t *testing.T, corrupt bool) (*Installer, *atomic.Int32) {
	t.Helper()
	pins, err := modelPins()
	if err != nil {
		t.Fatal(err)
	}
	blobs := &atomic.Int32{}
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var pin *ModelPin
		for _, p := range pins {
			if strings.HasPrefix(r.URL.Path, "/api/v1/models/"+p.ID+"/repo") {
				copy := p
				pin = &copy
				break
			}
		}
		if pin == nil {
			http.NotFound(w, r)
			return
		}
		if r.URL.Query().Get("Revision") != pin.Revision {
			t.Error("model requested without exact pinned revision")
			http.Error(w, "revision", 400)
			return
		}
		data := []byte("synthetic-model-" + pin.Role)
		if strings.HasSuffix(r.URL.Path, "/files") {
			if r.URL.Query().Get("Recursive") != "true" {
				t.Error("model file listing is not recursive")
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"Success": true, "Code": 200, "Data": map[string]any{"Files": []map[string]any{{"Path": "model.bin", "Revision": pin.Revision, "Sha256": digest(string(data)), "Size": len(data), "Type": "blob"}}}})
		} else {
			blobs.Add(1)
			if r.URL.Query().Get("FilePath") != "model.bin" {
				t.Error("unexpected model path")
			}
			if corrupt {
				data[0] = 'x'
			}
			_, _ = w.Write(data)
		}
	}))
	t.Cleanup(server.Close)
	i, err := NewInstaller(t.TempDir(), "darwin-aarch64")
	if err != nil {
		t.Fatal(err)
	}
	i.Manifest.Sources[0].ModelEndpoint = server.URL
	i.Download = &Downloader{client: server.Client()}
	return i, blobs
}
func TestModelAcquisitionValidatesPinsAndOfflineWarmupBeforePublication(t *testing.T) {
	i, blobs := modelInstaller(t, false)
	if i.ModelsAcquired(1) || i.ModelRetryable(1) {
		t.Fatal("missing models reported reusable")
	}
	if _, err := os.Stat(i.PlatformRoot()); !os.IsNotExist(err) {
		t.Fatal("status query created runtime generation")
	}
	if err := i.AcquireModels(context.Background(), 1, "official", nil); err != nil {
		t.Fatal(err)
	}
	if !i.ModelsAcquired(1) || !i.ModelRetryable(1) || blobs.Load() != 4 {
		t.Fatal("acquisition incomplete")
	}
	if err := i.AcquireModels(context.Background(), 1, "official", nil); err != nil {
		t.Fatal(err)
	}
	if blobs.Load() != 4 {
		t.Fatal("verified cache unnecessarily downloaded")
	}
	dir, _ := i.generation("model", 1)
	models := filepath.Join(dir, "models")
	calls := 0
	i.Commands = commandFunc(func(_ context.Context, e string, a []string, _ string, env map[string]string, _ func(string) error) error {
		calls++
		if e != "/synthetic/python" {
			t.Fatal("selected Python was not used")
		}
		if _, err := os.Stat(filepath.Join(dir, "ready.json")); !os.IsNotExist(err) {
			t.Fatal("unvalidated models published")
		}
		if calls == 1 {
			if len(a) != 4 || a[0] != filepath.Join(i.Resources, "runtime_warmup.py") || a[1] != "--models-root" || a[2] != models || a[3] != "--validate-only" {
				t.Fatal("offline validator changed")
			}
			for _, key := range []string{"MODELSCOPE_OFFLINE", "HF_HUB_OFFLINE", "TRANSFORMERS_OFFLINE"} {
				if env[key] != "1" {
					t.Fatal("warmup could access network")
				}
			}
			pins, _ := modelPins()
			keys := map[string]string{"model": "FUNASR_MODEL", "vad": "FUNASR_VAD_MODEL", "punc": "FUNASR_PUNC_MODEL", "spk": "FUNASR_SPK_MODEL"}
			for _, pin := range pins {
				relative, _ := modelRelative(pin.ID)
				if env[keys[pin.Role]] != filepath.Join(models, filepath.FromSlash(relative)) {
					t.Fatal("warmup did not select acquired pinned cache")
				}
			}
		} else if len(a) != 3 || a[0] != filepath.Join(i.Resources, "publish_model_cache.py") || a[1] != filepath.Join(dir, acquiredMarker) || a[2] != models {
			t.Fatal("legacy ModelScope metadata not published")
		}
		return nil
	})
	a, err := i.ValidateModels(context.Background(), 1, "/synthetic/python", nil)
	if err != nil {
		t.Fatal(err)
	}
	if calls != 2 || a.Path != models || a.Version != i.Manifest.ModelSetVersion {
		t.Fatal("model publication incorrect")
	}
}
func TestModelHashFailureAndTamperingNeverPublish(t *testing.T) {
	t.Run("download", func(t *testing.T) {
		i, _ := modelInstaller(t, true)
		if err := i.AcquireModels(context.Background(), 1, "official", nil); !errors.Is(err, ErrChecksum) {
			t.Fatal("invalid model hash accepted")
		}
		if i.ModelsAcquired(1) {
			t.Fatal("bad download marked acquired")
		}
	})
	t.Run("same_size_cache_change", func(t *testing.T) {
		i, _ := modelInstaller(t, false)
		if err := i.AcquireModels(context.Background(), 1, "official", nil); err != nil {
			t.Fatal(err)
		}
		root, set, err := i.acquiredSet(1)
		if err != nil {
			t.Fatal(err)
		}
		root.Close()
		dir, _ := i.generation("model", 1)
		model := set.Models[0]
		file := model.Files[0]
		if os.WriteFile(filepath.Join(dir, "models", filepath.FromSlash(model.RelativePath), file.Path), []byte(strings.Repeat("x", int(file.Size))), 0600) != nil {
			t.Fatal("fixture tamper")
		}
		i.Commands = commandFunc(func(context.Context, string, []string, string, map[string]string, func(string) error) error {
			t.Fatal("tampered model reached warmup")
			return nil
		})
		if _, err := i.ValidateModels(context.Background(), 1, "/synthetic/python", nil); !errors.Is(err, ErrChecksum) {
			t.Fatal("tampered cache accepted")
		}
		if _, err := os.Stat(filepath.Join(dir, "ready.json")); !os.IsNotExist(err) {
			t.Fatal("tampered cache published")
		}
		if i.ModelsAcquired(1) {
			t.Fatal("corrupt cache would skip acquisition on retry")
		}
		if err := i.AcquireModels(context.Background(), 1, "official", nil); err != nil {
			t.Fatal("corrupt cache retry failed", err)
		}
		if !i.ModelsAcquired(1) {
			t.Fatal("repaired cache not reusable")
		}
	})
	t.Run("marker", func(t *testing.T) {
		i, _ := modelInstaller(t, false)
		if err := i.AcquireModels(context.Background(), 1, "official", nil); err != nil {
			t.Fatal(err)
		}
		root, set, err := i.acquiredSet(1)
		if err != nil {
			t.Fatal(err)
		}
		defer root.Close()
		set.Models[0].Revision = strings.Repeat("0", 40)
		data, _ := json.Marshal(set)
		if err := atomicJSON(root, acquiredMarker, data); err != nil {
			t.Fatal(err)
		}
		if i.ModelsAcquired(1) {
			t.Fatal("marker changed pinned revision")
		}
	})
}
func TestModelFailedWarmupRetainsAcquiredGeneration(t *testing.T) {
	i, _ := modelInstaller(t, false)
	if err := i.AcquireModels(context.Background(), 2, "official", nil); err != nil {
		t.Fatal(err)
	}
	failure := errors.New("synthetic_warmup_failure")
	i.Commands = commandFunc(func(context.Context, string, []string, string, map[string]string, func(string) error) error {
		return failure
	})
	if _, err := i.ValidateModels(context.Background(), 2, "/synthetic/python", nil); !errors.Is(err, failure) {
		t.Fatal("warmup failure not returned")
	}
	if !i.ModelsAcquired(2) || !i.ModelRetryable(2) {
		t.Fatal("failed warmup discarded verified download")
	}
	dir, _ := i.generation("model", 2)
	if _, err := os.Stat(filepath.Join(dir, "ready.json")); !os.IsNotExist(err) {
		t.Fatal("failed warmup published ready state")
	}
}
