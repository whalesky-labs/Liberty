package runner

import (
	"os"
	"path/filepath"
	"strings"
)

type ModelPaths struct {
	Profile, ASR, VAD, Punctuation, Speaker string
}

func (models ModelPaths) Environment(directory string) (map[string]string, error) {
	if !filepath.IsAbs(directory) || models.Profile != "paraformer" && models.Profile != "sensevoice" {
		return nil, ErrStart
	}
	root, err := os.OpenRoot(directory)
	if err != nil {
		return nil, ErrStart
	}
	defer root.Close()
	environment := map[string]string{
		"FUNASR_PROFILE":    models.Profile,
		"FUNASR_MODEL":      models.ASR,
		"FUNASR_VAD_MODEL":  models.VAD,
		"FUNASR_PUNC_MODEL": models.Punctuation,
		"FUNASR_SPK_MODEL":  models.Speaker,
	}
	for _, model := range []string{models.ASR, models.VAD, models.Punctuation, models.Speaker} {
		if !filepath.IsAbs(model) {
			return nil, ErrStart
		}
		relative, err := filepath.Rel(directory, model)
		if err != nil || relative == "." || relative == ".." || filepath.IsAbs(relative) || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
			return nil, ErrStart
		}
		child, err := root.OpenRoot(relative)
		if err != nil {
			return nil, ErrStart
		}
		child.Close()
	}
	return environment, nil
}
