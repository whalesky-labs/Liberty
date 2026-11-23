package managed

import (
	"archive/tar"
	"archive/zip"
	"compress/gzip"
	"context"
	"errors"
	"io"
	"os"
	"path"
	"strings"
)

const maxEntries = 100000
const maxEntryBytes int64 = 8 * 1024 * 1024 * 1024

type archiveEntry struct {
	name, link string
	kind       byte
	size       int64
	mode       os.FileMode
}
type archivePlan struct {
	entries []archiveEntry
	names   map[string]archiveEntry
	total   int64
}

func (p *archivePlan) add(entry archiveEntry) error {
	normalized, err := safeRelative(entry.name)
	if err != nil {
		return err
	}
	entry.name = normalized
	if p.names == nil {
		p.names = map[string]archiveEntry{}
	}
	key := strings.ToLower(normalized)
	if _, ok := p.names[key]; ok {
		return ErrArchive
	}
	if len(p.entries) >= maxEntries || entry.size < 0 || entry.size > maxEntryBytes || p.total > maxAssetBytes-entry.size {
		return ErrArchive
	}
	p.total += entry.size
	p.entries = append(p.entries, entry)
	p.names[key] = entry
	return nil
}
func (p archivePlan) validateLinks() error {
	for _, e := range p.entries {
		for parent := path.Dir(e.name); parent != "."; parent = path.Dir(parent) {
			if existing, ok := p.names[strings.ToLower(parent)]; ok && existing.kind != 'd' {
				return ErrArchive
			}
		}
		if e.kind == 's' || e.kind == 'h' {
			resolved, err := linkTarget(e)
			if err != nil {
				return err
			}
			seen := map[string]bool{strings.ToLower(e.name): true}
			for {
				key := strings.ToLower(resolved)
				if seen[key] {
					return ErrArchive
				}
				seen[key] = true
				target, ok := p.names[key]
				if !ok {
					break
				} // dangling internal links are permitted, as in tar
				if target.kind != 's' && target.kind != 'h' {
					break
				}
				resolved, err = linkTarget(target)
				if err != nil {
					return err
				}
			}
		}
	}
	return nil
}
func linkTarget(e archiveEntry) (string, error) {
	if e.link == "" || strings.ContainsAny(e.link, "\\:\x00") || strings.HasPrefix(e.link, "/") {
		return "", ErrArchive
	}
	base := ""
	if e.kind == 's' {
		base = path.Dir(e.name)
	}
	resolved := path.Clean(path.Join(base, e.link))
	if resolved == "." || resolved == ".." || strings.HasPrefix(resolved, "../") {
		return "", ErrArchive
	}
	return resolved, nil
}

type contextReader struct {
	ctx context.Context
	r   io.Reader
}

func (r contextReader) Read(b []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	return r.r.Read(b)
}
func walkTar(ctx context.Context, file *os.File, visit func(*tar.Header, io.Reader) error) error {
	if _, err := file.Seek(0, io.SeekStart); err != nil {
		return ErrArchive
	}
	compressed, err := gzip.NewReader(contextReader{ctx, file})
	if err != nil {
		return ErrArchive
	}
	defer compressed.Close()
	// Bound expanded headers, payloads and padding, including metadata extensions.
	limited := &io.LimitedReader{R: compressed, N: maxAssetBytes + 64*1024*1024}
	reader := tar.NewReader(limited)
	count := 0
	for {
		header, err := reader.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			return ErrArchive
		}
		count++
		if count > maxEntries {
			return ErrArchive
		}
		if header.Size < 0 || header.Size > maxEntryBytes {
			return ErrArchive
		}
		if err := visit(header, contextReader{ctx, reader}); err != nil {
			return err
		}
	}
	if limited.N <= 0 {
		return ErrArchive
	}
	// tar EOF precedes the gzip trailer; consume it so truncated streams or a
	// bad gzip checksum cannot be published after a successful tar walk.
	if _, err := io.Copy(io.Discard, limited); err != nil || limited.N <= 0 {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return ErrArchive
	}
	return nil
}
func tarKind(header *tar.Header) (byte, error) {
	switch header.Typeflag {
	case tar.TypeReg, tar.TypeRegA:
		return 'f', nil
	case tar.TypeDir:
		return 'd', nil
	case tar.TypeSymlink:
		return 's', nil
	case tar.TypeLink:
		return 'h', nil
	}
	return 0, ErrArchive
}

// Extract preflights the entire archive before creating the destination. Only
// a new staging directory is accepted. On any failure that staging is removed.
func Extract(ctx context.Context, archive string, parent *os.Root, destination string) error {
	if !safeName(destination) {
		return ErrArchive
	}
	file, err := os.Open(archive)
	if err != nil {
		return ErrArchive
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() {
		return ErrArchive
	}
	plan := archivePlan{}
	zipFile := strings.HasSuffix(strings.ToLower(archive), ".zip")
	var zipReader *zip.Reader
	if zipFile {
		zipReader, err = zip.NewReader(file, info.Size())
		if err != nil || len(zipReader.File) > maxEntries {
			return ErrArchive
		}
		for _, entry := range zipReader.File {
			if err := ctx.Err(); err != nil {
				return err
			}
			if entry.UncompressedSize64 > uint64(maxEntryBytes) {
				return ErrArchive
			}
			kind := byte('f')
			if entry.FileInfo().IsDir() {
				kind = 'd'
			} else if !entry.Mode().IsRegular() {
				return ErrArchive
			}
			if err := plan.add(archiveEntry{name: entry.Name, kind: kind, size: int64(entry.UncompressedSize64), mode: entry.Mode().Perm()}); err != nil {
				return err
			}
		}
	} else if strings.HasSuffix(strings.ToLower(archive), ".tar.gz") {
		if err := walkTar(ctx, file, func(h *tar.Header, _ io.Reader) error {
			kind, err := tarKind(h)
			if err != nil {
				return err
			}
			// Root ./ directory entries carry no path or business content.
			if kind == 'd' && (h.Name == "." || h.Name == "./") {
				return nil
			}
			return plan.add(archiveEntry{name: h.Name, link: h.Linkname, kind: kind, size: h.Size, mode: os.FileMode(h.Mode) & 0777})
		}); err != nil {
			return err
		}
	} else {
		return ErrArchive
	}
	if len(plan.entries) == 0 {
		return ErrArchive
	}
	if err := plan.validateLinks(); err != nil {
		return err
	}
	if err := parent.Mkdir(destination, 0700); err != nil {
		return ErrArchive
	}
	root, err := parent.OpenRoot(destination)
	if err != nil {
		_ = parent.RemoveAll(destination)
		return ErrArchive
	}
	complete := false
	defer func() {
		root.Close()
		if !complete {
			_ = parent.RemoveAll(destination)
		}
	}()
	extractFile := func(e archiveEntry, reader io.Reader) error {
		if root.MkdirAll(path.Dir(e.name), 0700) != nil {
			return ErrArchive
		}
		if e.kind == 'd' {
			if root.MkdirAll(e.name, 0755) != nil {
				return ErrArchive
			}
			return nil
		}
		mode := e.mode & 0777
		if mode == 0 {
			mode = 0600
		}
		out, err := root.OpenFile(e.name, os.O_WRONLY|os.O_CREATE|os.O_EXCL, mode)
		if err != nil {
			return ErrArchive
		}
		copied, copyErr := io.Copy(out, io.LimitReader(reader, e.size+1))
		closeErr := out.Close()
		if copyErr != nil || closeErr != nil || copied != e.size {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			return ErrArchive
		}
		return nil
	}
	if zipFile {
		for i, z := range zipReader.File {
			if err := ctx.Err(); err != nil {
				return err
			}
			stream, err := z.Open()
			if err != nil {
				return ErrArchive
			}
			err = extractFile(plan.entries[i], contextReader{ctx, stream})
			closeErr := stream.Close()
			if err != nil {
				return err
			}
			if closeErr != nil {
				return ErrArchive
			}
		}
	} else {
		if err := walkTar(ctx, file, func(h *tar.Header, r io.Reader) error {
			kind, _ := tarKind(h)
			if kind == 'd' && (h.Name == "." || h.Name == "./") {
				return nil
			}
			name, err := safeRelative(h.Name)
			if err != nil {
				return err
			}
			entry, ok := plan.names[strings.ToLower(name)]
			if !ok {
				return ErrArchive
			}
			if kind == 's' || kind == 'h' {
				return nil
			}
			return extractFile(entry, r)
		}); err != nil {
			return err
		}
		// Links come last. A parent cannot itself be a link (checked above).
		pending := []archiveEntry{}
		for _, e := range plan.entries {
			if e.kind == 's' || e.kind == 'h' {
				pending = append(pending, e)
			}
		}
		for len(pending) > 0 {
			remaining := []archiveEntry{}
			advanced := false
			for _, e := range pending {
				if root.MkdirAll(path.Dir(e.name), 0700) != nil {
					return ErrArchive
				}
				if e.kind == 's' {
					if root.Symlink(e.link, e.name) != nil {
						return ErrArchive
					}
					advanced = true
					continue
				}
				target, _ := linkTarget(e)
				targetInfo, statErr := root.Lstat(target)
				if errors.Is(statErr, os.ErrNotExist) {
					remaining = append(remaining, e)
					continue
				}
				if statErr != nil || !targetInfo.Mode().IsRegular() || root.Link(target, e.name) != nil {
					return ErrArchive
				}
				advanced = true
			}
			if !advanced {
				return ErrArchive
			}
			pending = remaining
		}
	}
	complete = true
	return nil
}
