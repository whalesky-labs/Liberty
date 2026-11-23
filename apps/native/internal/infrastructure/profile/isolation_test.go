package profile

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestCandidateIsolationRejectsProductionAliasesAndDatabaseLinksBeforeCreation(t *testing.T) {
	root := t.TempDir()
	production := filepath.Join(root, ProductionID)
	if err := os.Mkdir(production, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(production, DatabaseName), []byte("synthetic production bytes"), 0600); err != nil {
		t.Fatal(err)
	}
	volumeRoot := root
	for filepath.Dir(volumeRoot) != volumeRoot {
		volumeRoot = filepath.Dir(volumeRoot)
	}
	for _, candidate := range []string{production, filepath.Join(production, "new-candidate"), root, volumeRoot, filepath.Join(root, "COM.WESTNG.LIBERTY")} {
		if err := RequireIsolatedDirectory(candidate, production); !errors.Is(err, ErrProfileOverlap) {
			t.Fatal("overlapping production directory accepted", err)
		}
	}
	parentComponent := root + string(filepath.Separator) + "subdirectory" + string(filepath.Separator) + ".." + string(filepath.Separator) + "candidate"
	if _, err := ResolveDirectory(parentComponent); !errors.Is(err, ErrLocation) {
		t.Fatal("ambiguous parent component accepted")
	}
	alias := filepath.Join(root, "production-alias")
	if err := os.Symlink(production, alias); err == nil {
		if err := RequireIsolatedDirectory(filepath.Join(alias, "new-candidate"), production); !errors.Is(err, ErrProfileOverlap) {
			t.Fatal("symlink ancestor bypassed isolation", err)
		}
	}
	candidate := filepath.Join(root, CandidateID)
	if err := RequireIsolatedDirectory(candidate, production); err != nil {
		t.Fatal("separate new directory rejected", err)
	}
	if _, err := os.Stat(candidate); !os.IsNotExist(err) {
		t.Fatal("isolation check created candidate directory")
	}
	if err := os.Mkdir(candidate, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.Link(filepath.Join(production, DatabaseName), filepath.Join(candidate, DatabaseName)); err != nil {
		t.Fatal(err)
	}
	if err := RequireIsolatedDirectory(candidate, production); !errors.Is(err, ErrProfileOverlap) {
		t.Fatal("hard-linked production database accepted", err)
	}
	data, err := os.ReadFile(filepath.Join(production, DatabaseName))
	if err != nil || string(data) != "synthetic production bytes" {
		t.Fatal("isolation check changed production fixture", err)
	}
	if _, err := os.Stat(filepath.Join(production, "new-candidate")); !os.IsNotExist(err) {
		t.Fatal("isolation check created a production child")
	}
}
