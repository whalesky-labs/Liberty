package presentation

import (
	"bytes"
	"image"
	"image/color"
	"image/png"
	"testing"
	"testing/fstest"
)

func TestBitmapCacheBoundsPixelsAndReusesVisibleEvictions(t *testing.T) {
	source := image.NewRGBA(image.Rect(0, 0, 640, 320))
	source.Set(0, 0, color.RGBA{R: 255, A: 255})
	var encoded bytes.Buffer
	if err := png.Encode(&encoded, source); err != nil {
		t.Fatal(err)
	}
	files := fstest.MapFS{"one": &fstest.MapFile{Data: encoded.Bytes()}, "two": &fstest.MapFile{Data: encoded.Bytes()}}
	cache := newBitmapCache(320 * 160 * 4)
	first := cache.load(files, "one", 320)
	if width, height := first.Size(); width != 320 || height != 160 {
		t.Fatal("aspect-ratio thumbnail dimensions", width, height)
	}
	if cache.load(files, "one", 320) != first {
		t.Fatal("cached bitmap decoded again")
	}
	second := cache.load(files, "two", 320)
	if cache.used > cache.budget || len(cache.strong) != 1 {
		t.Fatal("retained pixels exceed budget")
	}
	if cache.load(files, "one", 320) != first || cache.load(files, "two", 320) != second {
		t.Fatal("visible image decoded again after eviction")
	}
}

func TestEmbeddedArtCachesStayWithinTheirBudgets(t *testing.T) {
	entries, err := petArtFiles.ReadDir("pet-art")
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		bitmap := petBitmap(entry.Name())
		if bitmap == nil || petArtCache.used > petArtCache.budget {
			t.Fatal("pet art cache exceeds its budget")
		}
	}
	entries, err = workArtFiles.ReadDir("work-art")
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if WorkArt(entry.Name()) == nil || workArtCache.used > workArtCache.budget {
			t.Fatal("work art cache exceeds its budget")
		}
	}
}
