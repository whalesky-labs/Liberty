package companion3d

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"io/fs"
)

var PixelActions = []string{"crush", "defecate", "drive", "eat", "gaming", "pants", "reading", "rope", "run", "slack", "sleep", "snow", "studying", "toy", "work"}

type PixelAsset struct {
	ID     string   `json:"id"`
	Title  []string `json:"title"`
	File   string   `json:"file"`
	SHA256 string   `json:"sha256"`
	Bytes  int      `json:"bytes"`
}
type PixelLibrary struct {
	source  fs.FS
	actions []PixelAsset
}

func boundedAsset(source fs.FS, name string, limit int64) ([]byte, error) {
	f, err := source.Open(name)
	if err != nil {
		return nil, ErrModel
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Size() < 1 || info.Size() > limit {
		return nil, ErrModel
	}
	data, err := io.ReadAll(io.LimitReader(f, limit+1))
	if err != nil || int64(len(data)) > limit {
		return nil, ErrModel
	}
	return data, nil
}

// OpenPixelLibrary pins the exact visually reviewed files. It never loads the
// rejected pilot manifest or accepts arbitrary paths/URLs from asset metadata.
func OpenPixelLibrary(source fs.FS) (*PixelLibrary, error) {
	data, err := boundedAsset(source, "manifest.json", 256<<10)
	if err != nil {
		return nil, err
	}
	var manifest struct {
		Style, Status string
		Actions       []PixelAsset
	}
	if json.Unmarshal(data, &manifest) != nil || manifest.Style != "volumetric-pixel" || manifest.Status != "pixel-actions-review-required" || len(manifest.Actions) != len(PixelActions) {
		return nil, ErrModel
	}
	data, err = boundedAsset(source, "native-review.json", 16<<10)
	if err != nil {
		return nil, err
	}
	var review struct {
		NativeIntegrationApproved bool
		ActionSHA256              map[string]string
	}
	if json.Unmarshal(data, &review) != nil || !review.NativeIntegrationApproved || len(review.ActionSHA256) != len(PixelActions) {
		return nil, ErrModel
	}
	for i, action := range manifest.Actions {
		if action.ID != PixelActions[i] || action.File != action.ID+".glb" || action.Bytes < 28 || action.Bytes > 18<<20 || len(action.Title) != 2 || action.Title[0] == "" || action.Title[1] == "" || review.ActionSHA256[action.ID] != action.SHA256 {
			return nil, ErrModel
		}
		digest, e := hex.DecodeString(action.SHA256)
		if e != nil || len(digest) != 32 {
			return nil, ErrModel
		}
	}
	return &PixelLibrary{source, manifest.Actions}, nil
}
func (l *PixelLibrary) Actions() []PixelAsset {
	result := make([]PixelAsset, len(l.actions))
	copy(result, l.actions)
	for i := range result {
		result[i].Title = append([]string(nil), result[i].Title...)
	}
	return result
}
func (l *PixelLibrary) Load(name string) (*PixelModel, error) {
	for _, asset := range l.actions {
		if asset.ID != name {
			continue
		}
		data, err := boundedAsset(l.source, asset.File, 18<<20)
		if err != nil || len(data) != asset.Bytes {
			return nil, ErrModel
		}
		digest := sha256.Sum256(data)
		if hex.EncodeToString(digest[:]) != asset.SHA256 {
			return nil, ErrModel
		}
		model, err := DecodePixel(data)
		if err != nil {
			return nil, err
		}
		model.Name = name
		return model, nil
	}
	return nil, ErrModel
}
