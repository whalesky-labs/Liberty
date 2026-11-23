package managed

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"
)

// The application embeds reviewed source text, never a downloaded wheel. The
// managed interpreter builds the pinned metadata-only wheel in a private tree.
// All five outputs are independently verified before pip can see any of them.
type vendorRecipe struct {
	SchemaVersion int `json:"schemaVersion"`
	Upstream      struct {
		Name   string `json:"filename"`
		URL    string `json:"url"`
		Bytes  int64  `json:"bytes"`
		SHA256 string `json:"sha256"`
	} `json:"upstream"`
}

func fileMatches(content []byte, pin RuntimeInputFile) bool {
	digest := sha256.Sum256(content)
	return int64(len(content)) == pin.Bytes && strings.EqualFold(hex.EncodeToString(digest[:]), pin.SHA256)
}

func productionSourceInputs(manifest Manifest) (map[string][]byte, string, error) {
	if manifest.ProductionInputs == nil {
		return nil, "", nil
	}
	pins := manifest.ProductionInputs
	if !validInputFile(pins.Builder) || !validRuntimeInputFiles(pins.Files) {
		return nil, "", ErrManifest
	}
	inputs := map[string][]byte{pins.Builder.Name: []byte(resources[pins.Builder.Name])}
	if !fileMatches(inputs[pins.Builder.Name], pins.Builder) {
		return nil, "", ErrChecksum
	}
	for _, pin := range pins.Files {
		name := pin.Name
		if name == "recipe.json" {
			name = "funasr-1.3.9.json"
		} else if !strings.HasPrefix(name, "requirements-lock-") {
			continue
		}
		content := []byte(resources[name])
		if !fileMatches(content, pin) {
			return nil, "", ErrChecksum
		}
		inputs[name] = content
	}
	encoded, err := json.Marshal(pins)
	if err != nil {
		return nil, "", ErrManifest
	}
	inputs["production-inputs.json"] = encoded
	return inputs, resourceDigest(inputs), nil
}

func (i *Installer) productionRecipe() (vendorRecipe, error) {
	var recipe vendorRecipe
	if json.Unmarshal(i.inputs["funasr-1.3.9.json"], &recipe) != nil || recipe.SchemaVersion != 1 || !safeName(recipe.Upstream.Name) || !immutableURL(recipe.Upstream.URL) || !validHash(recipe.Upstream.SHA256) || recipe.Upstream.Bytes < 1 || recipe.Upstream.Bytes > 2*1024*1024 {
		return recipe, ErrManifest
	}
	return recipe, nil
}

func (i *Installer) vendorFiles() []RuntimeInputFile {
	files := []RuntimeInputFile{}
	for _, file := range i.Manifest.ProductionInputs.Files {
		if !strings.HasPrefix(file.Name, "requirements-lock-") {
			files = append(files, file)
		}
	}
	return files
}

func (i *Installer) readVendor(root *os.Root) (map[string][]byte, error) {
	files := i.vendorFiles()
	expected := make(map[string]bool, len(files))
	for _, file := range files {
		expected[file.Name] = true
	}
	if len(files) != 5 || checkDirectoryEntries(root, expected) != nil {
		return nil, ErrResources
	}
	inputs := make(map[string][]byte, len(files))
	for _, pin := range files {
		content, err := readRegularFile(root, pin.Name, pin.Bytes)
		if err != nil {
			return nil, err
		}
		if !fileMatches(content, pin) {
			return nil, ErrChecksum
		}
		inputs[pin.Name] = content
	}
	return inputs, nil
}

func (i *Installer) ensureProductionVendor(ctx context.Context, executable string, log func(string) error) error {
	if i.candidateProfile() || i.Manifest.ProductionInputs == nil {
		return nil
	}
	i.vendorBuildMu.Lock()
	defer i.vendorBuildMu.Unlock()
	if i.productionVendor.Load() != nil {
		return i.verifyResources()
	}
	if err := i.verifyResources(); err != nil {
		return err
	}
	parent, err := i.runtimeDirectory(path.Join(i.platformRelative(), "vendor-inputs"), true)
	if err != nil {
		return err
	}
	defer parent.Close()
	root, err := openPlainChild(parent, i.InputDigest, false)
	if err == nil {
		defer root.Close()
		inputs, err := i.readVendor(root)
		if err != nil {
			return err
		}
		i.productionVendor.Store(&inputs)
		return nil
	}
	if !errors.Is(err, os.ErrNotExist) {
		return ErrResources
	}
	inputs, err := i.buildProductionVendor(ctx, executable, parent, log)
	if err != nil {
		return err
	}
	i.productionVendor.Store(&inputs)
	return nil
}

func writeVendorSource(root *os.Root, name string, content []byte) error {
	file, err := root.OpenFile(name, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return ErrResources
	}
	n, writeErr := file.Write(content)
	syncErr, closeErr := file.Sync(), file.Close()
	if writeErr != nil || n != len(content) || syncErr != nil || closeErr != nil {
		return ErrResources
	}
	return nil
}

func (i *Installer) buildProductionVendor(ctx context.Context, executable string, parent *os.Root, log func(string) error) (map[string][]byte, error) {
	recipe, err := i.productionRecipe()
	if err != nil {
		return nil, err
	}
	downloads, err := i.downloads()
	if err != nil {
		return nil, err
	}
	defer downloads.Close()
	if err := i.Download.Fetch(ctx, downloads, recipe.Upstream.Name, recipe.Upstream.URL, recipe.Upstream.SHA256, &recipe.Upstream.Bytes, nil); err != nil {
		return nil, err
	}
	if _, err := readRegularFile(downloads, recipe.Upstream.Name, recipe.Upstream.Bytes); err != nil {
		return nil, err
	}
	var nonce [16]byte
	if _, err := rand.Read(nonce[:]); err != nil {
		return nil, ErrResources
	}
	name := "build-" + hex.EncodeToString(nonce[:])
	if parent.Mkdir(name, 0700) != nil {
		return nil, ErrResources
	}
	defer parent.RemoveAll(name)
	stage, err := openPlainChild(parent, name, false)
	if err != nil {
		return nil, err
	}
	defer stage.Close()
	for folder, file := range map[string]string{"tools": "build_vendor_wheel.py", "vendor": "funasr-1.3.9.json"} {
		child, err := openPlainChild(stage, folder, true)
		if err != nil {
			return nil, err
		}
		err = writeVendorSource(child, file, i.inputs[file])
		child.Close()
		if err != nil {
			return nil, err
		}
	}
	directory := filepath.Join(parent.Name(), name)
	sources := map[string][]byte{"tools/build_vendor_wheel.py": i.inputs["build_vendor_wheel.py"], "vendor/funasr-1.3.9.json": i.inputs["funasr-1.3.9.json"]}
	verifySources := func() error {
		for filename, content := range sources {
			actual, err := readRegularFile(stage, filename, int64(len(content)))
			if err != nil || !fileMatches(actual, RuntimeInputFile{Bytes: int64(len(content)), SHA256: digestBytes(content)}) {
				return ErrResources
			}
		}
		return nil
	}
	if verifySources() != nil {
		return nil, ErrResources
	}
	args := []string{"-I", "-S", filepath.Join(directory, "tools", "build_vendor_wheel.py"), "--upstream-wheel", filepath.Join(downloads.Name(), recipe.Upstream.Name), "--output-dir", filepath.Join(directory, "output")}
	runCtx, cancel := context.WithTimeout(ctx, 90*time.Second)
	err = i.runCommand(runCtx, executable, args, directory, log)
	cancel()
	if verifySources() != nil {
		return nil, ErrResources
	}
	if err != nil {
		return nil, err
	}
	output, err := openPlainChild(stage, "output", false)
	if err != nil {
		return nil, err
	}
	inputs, err := i.readVendor(output)
	output.Close()
	if err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if parent.Rename(path.Join(name, "output"), i.InputDigest) != nil {
		return nil, ErrResources
	}
	return inputs, nil
}

func digestBytes(content []byte) string {
	digest := sha256.Sum256(content)
	return hex.EncodeToString(digest[:])
}
