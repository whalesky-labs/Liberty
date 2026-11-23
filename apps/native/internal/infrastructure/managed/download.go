package managed

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"net/http"
	"os"
	"strings"
	"time"
)

const maxAssetBytes int64 = 20 * 1024 * 1024 * 1024

type Downloader struct{ client *http.Client }

func NewDownloader() *Downloader {
	return &Downloader{client: &http.Client{Timeout: 30 * time.Minute, CheckRedirect: func(req *http.Request, via []*http.Request) error {
		if len(via) >= 10 || !httpsURL(req.URL.String()) {
			return ErrDownload
		}
		return nil
	}}}
}
func checksum(root *os.Root, name, expected string, size *int64) bool {
	info, err := root.Lstat(name)
	if err != nil || !info.Mode().IsRegular() || info.Size() > maxAssetBytes || (size != nil && info.Size() != *size) {
		return false
	}
	f, err := root.Open(name)
	if err != nil {
		return false
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, io.LimitReader(f, maxAssetBytes+1)); err != nil {
		return false
	}
	return strings.EqualFold(hex.EncodeToString(h.Sum(nil)), expected)
}

// Fetch keeps a verified cached file, and atomically publishes only a complete,
// hash-verified download. Cancellation/hash/IO failure preserves prior contents.
func (d *Downloader) Fetch(ctx context.Context, root *os.Root, name, address, digest string, size *int64, progress func(int64, int64) error) error {
	if !safeName(name) || !httpsURL(address) || !validHash(digest) || (size != nil && (*size < 0 || *size > maxAssetBytes)) {
		return ErrAsset
	}
	if checksum(root, name, digest, size) {
		return nil
	}
	if info, err := root.Lstat(name); err == nil && !info.Mode().IsRegular() {
		return ErrAsset
	} else if err != nil && !os.IsNotExist(err) {
		return ErrAsset
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, address, nil)
	if err != nil {
		return ErrDownload
	}
	req.Header.Set("User-Agent", "Liberty Runtime/1.1")
	response, err := d.client.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return ErrDownload
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK || response.ContentLength > maxAssetBytes || (size != nil && response.ContentLength >= 0 && response.ContentLength != *size) {
		return ErrDownload
	}
	var nonce [16]byte
	if _, err := rand.Read(nonce[:]); err != nil {
		return ErrDownload
	}
	temporary := ".download-" + hex.EncodeToString(nonce[:])
	f, err := root.OpenFile(temporary, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return ErrDownload
	}
	defer root.Remove(temporary)
	h := sha256.New()
	var downloaded int64
	last := time.Now()
	buffer := make([]byte, 128*1024)
	result := func() error {
		for {
			if err := ctx.Err(); err != nil {
				return err
			}
			n, readErr := response.Body.Read(buffer)
			if n > 0 {
				downloaded += int64(n)
				if downloaded > maxAssetBytes || (size != nil && downloaded > *size) {
					return ErrDownload
				}
				if _, err := f.Write(buffer[:n]); err != nil {
					return ErrDownload
				}
				h.Write(buffer[:n])
				if progress != nil && time.Since(last) >= time.Second {
					if err := progress(downloaded, response.ContentLength); err != nil {
						return err
					}
					last = time.Now()
				}
			}
			if readErr == io.EOF {
				break
			}
			if readErr != nil {
				if ctx.Err() != nil {
					return ctx.Err()
				}
				return ErrDownload
			}
		}
		if (response.ContentLength >= 0 && downloaded != response.ContentLength) || (size != nil && downloaded != *size) {
			return ErrDownload
		}
		if !strings.EqualFold(hex.EncodeToString(h.Sum(nil)), digest) {
			return ErrChecksum
		}
		if progress != nil {
			if err := progress(downloaded, response.ContentLength); err != nil {
				return err
			}
		}
		if f.Sync() != nil {
			return ErrDownload
		}
		return nil
	}()
	closeErr := f.Close()
	if result != nil {
		return result
	}
	if closeErr != nil {
		return ErrDownload
	}
	if root.Rename(temporary, name) != nil {
		return ErrDownload
	}
	return nil
}
