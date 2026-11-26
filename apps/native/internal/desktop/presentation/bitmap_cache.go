package presentation

import (
	"bytes"
	"container/list"
	"image"
	"io/fs"
	"sync"
	"weak"

	"github.com/egoist/mygo/ui"
	"golang.org/x/image/draw"
)

type cachedBitmap struct {
	name   string
	bitmap *ui.Bitmap
	bytes  int
}

// The budget bounds retained base RGBA pixels, not total process/GPU memory.
// Weak entries reuse images still held by a visible frame/window after LRU
// eviction, without keeping every previously visited page alive forever.
type bitmapCache struct {
	mu           sync.Mutex
	budget, used int
	order        list.List
	strong       map[string]*list.Element
	weak         map[string]weak.Pointer[ui.Bitmap]
}

func newBitmapCache(budget int) *bitmapCache {
	return &bitmapCache{budget: budget, strong: make(map[string]*list.Element), weak: make(map[string]weak.Pointer[ui.Bitmap])}
}

func (c *bitmapCache) load(files fs.FS, name string, maxEdge int) *ui.Bitmap {
	c.mu.Lock()
	defer c.mu.Unlock()
	if entry := c.strong[name]; entry != nil {
		c.order.MoveToFront(entry)
		return entry.Value.(cachedBitmap).bitmap
	}
	bitmap := c.weak[name].Value()
	if bitmap == nil {
		data, err := fs.ReadFile(files, name)
		if err != nil {
			panic("missing embedded artwork")
		}
		decoded, _, err := image.Decode(bytes.NewReader(data))
		if err != nil {
			panic("invalid embedded artwork")
		}
		bounds := decoded.Bounds()
		width, height := bounds.Dx(), bounds.Dy()
		if edge := max(width, height); edge > maxEdge {
			width, height = max(1, width*maxEdge/edge), max(1, height*maxEdge/edge)
			thumbnail := image.NewRGBA(image.Rect(0, 0, width, height))
			draw.CatmullRom.Scale(thumbnail, thumbnail.Bounds(), decoded, bounds, draw.Src, nil)
			decoded = thumbnail
		}
		bitmap = ui.NewBitmap(decoded)
		c.weak[name] = weak.Make(bitmap)
	}
	width, height := bitmap.Size()
	size := width * height * 4
	if size <= c.budget {
		for c.used+size > c.budget && c.order.Len() > 0 {
			last := c.order.Back()
			entry := last.Value.(cachedBitmap)
			delete(c.strong, entry.name)
			c.used -= entry.bytes
			c.order.Remove(last)
		}
		c.strong[name] = c.order.PushFront(cachedBitmap{name, bitmap, size})
		c.used += size
	}
	return bitmap
}
