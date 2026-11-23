package managed

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
)

func digest(data string) string {
	sum := sha256.Sum256([]byte(data))
	return hex.EncodeToString(sum[:])
}
func TestDownloadVerifiesBeforePublicationAndReusesOnlyValidCache(t *testing.T) {
	count := 0
	content := "verified"
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { count++; w.Write([]byte(content)) }))
	defer server.Close()
	d := &Downloader{client: server.Client()}
	root, err := os.OpenRoot(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	if err := d.Fetch(context.Background(), root, "asset.zip", server.URL, digest("verified"), nil, nil); err != nil {
		t.Fatal(err)
	}
	if err := d.Fetch(context.Background(), root, "asset.zip", server.URL, digest("verified"), nil, nil); err != nil || count != 1 {
		t.Fatal("verified cache not reused")
	}
	content = "corrupt"
	if err := d.Fetch(context.Background(), root, "asset.zip", server.URL, digest("different"), nil, nil); !errors.Is(err, ErrChecksum) {
		t.Fatal("hash mismatch accepted")
	}
	data, _ := root.ReadFile("asset.zip")
	if string(data) != "verified" {
		t.Fatal("bad download replaced old verified bytes")
	}
	entries, err := os.ReadDir(root.Name())
	if err != nil || len(entries) != 1 {
		t.Fatal("temporary download leaked")
	}
	size := int64(100)
	if d.Fetch(context.Background(), root, "new.zip", server.URL, digest("corrupt"), &size, nil) == nil {
		t.Fatal("size mismatch accepted")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if !errors.Is(d.Fetch(ctx, root, "cancel.zip", server.URL, digest("corrupt"), nil, nil), context.Canceled) {
		t.Fatal("cancel lost")
	}
}
func TestDownloadRejectsInsecureSourceAndSymlinkTarget(t *testing.T) {
	root, err := os.OpenRoot(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	d := NewDownloader()
	if d.Fetch(context.Background(), root, "a", "http://example.invalid/v1/a", digest("a"), nil, nil) == nil {
		t.Fatal("http accepted")
	}
	if root.Symlink("other", "a") != nil {
		t.Skip("symlink unavailable")
	}
	if d.Fetch(context.Background(), root, "a", "https://example.invalid/v1/a", digest("a"), nil, nil) == nil {
		t.Fatal("symlink accepted")
	}
}
