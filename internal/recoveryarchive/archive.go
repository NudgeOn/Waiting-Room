// SPDX-License-Identifier: Apache-2.0
// Package recoveryarchive creates cold snapshots of explicitly mounted volumes.
// It never follows links, opens devices, overwrites files, or starts services.
package recoveryarchive

import (
	"archive/tar"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path"
	"path/filepath"
	"strings"
	"syscall"
	"time"
)

var Volumes = []string{"database", "state", "identities", "gateway-data", "coordinator-data", "queue-config", "queue-data"}

const MaxBytes int64 = 16 << 30
const maxEntries = 1000000

var ErrArchive = errors.New("invalid or incomplete private recovery archive; no services were started")

type Manifest struct {
	Schema  int    `json:"schema"`
	SHA256  string `json:"sha256"`
	Bytes   int64  `json:"bytes"`
	Entries int    `json:"entries"`
}

func privateDir(dir string) error {
	s, e := os.Lstat(dir)
	if e != nil || !s.IsDir() || s.Mode().Perm()&0077 != 0 {
		return ErrArchive
	}
	return nil
}
func Create(ctx context.Context, root, dest string) (Manifest, error) {
	var m Manifest
	if privateDir(dest) != nil {
		return m, ErrArchive
	}
	f, e := os.OpenFile(filepath.Join(dest, "volumes.tar"), os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if e != nil {
		return m, ErrArchive
	}
	committed := false
	defer func() {
		f.Close()
		if !committed {
			os.Remove(f.Name())
		}
	}()
	h := sha256.New()
	w := tar.NewWriter(io.MultiWriter(f, h))
	entries := 0
	var bytes int64
	for _, name := range Volumes {
		base := filepath.Join(root, name)
		e = filepath.Walk(base, func(file string, info os.FileInfo, err error) error {
			if err != nil || ctx.Err() != nil || info == nil {
				return ErrArchive
			}
			if !info.IsDir() && !info.Mode().IsRegular() {
				return ErrArchive
			}
			entries++
			if entries > maxEntries {
				return ErrArchive
			}
			rel, err := filepath.Rel(root, file)
			if err != nil {
				return ErrArchive
			}
			hdr, err := tar.FileInfoHeader(info, "")
			if err != nil {
				return ErrArchive
			}
			hdr.Name = filepath.ToSlash(rel)
			hdr.ModTime = hdr.ModTime.UTC().Truncate(time.Second)
			hdr.AccessTime = time.Time{}
			hdr.ChangeTime = time.Time{}
			hdr.Format = tar.FormatUSTAR
			hdr.Uname = ""
			hdr.Gname = ""
			if stat, ok := info.Sys().(*syscall.Stat_t); ok {
				hdr.Uid = int(stat.Uid)
				hdr.Gid = int(stat.Gid)
			}
			hdr.Mode = int64(info.Mode().Perm())
			bytes += hdr.Size
			if bytes > MaxBytes {
				return ErrArchive
			}
			if err = w.WriteHeader(hdr); err != nil {
				return ErrArchive
			}
			if info.IsDir() {
				return nil
			}
			in, err := os.Open(file)
			if err != nil {
				return ErrArchive
			}
			defer in.Close()
			actual, err := in.Stat()
			if err != nil || !os.SameFile(info, actual) || actual.Size() != info.Size() {
				return ErrArchive
			}
			if _, err = io.CopyN(w, in, hdr.Size); err != nil {
				return ErrArchive
			}
			return nil
		})
		if e != nil {
			return m, ErrArchive
		}
	}
	if w.Close() != nil || f.Sync() != nil {
		return m, ErrArchive
	}
	stat, e := f.Stat()
	if e != nil {
		return m, ErrArchive
	}
	m = Manifest{1, hex.EncodeToString(h.Sum(nil)), stat.Size(), entries}
	b, _ := json.Marshal(m)
	mf, e := os.OpenFile(filepath.Join(dest, "volumes.json"), os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if e != nil {
		return m, ErrArchive
	}
	if _, e = mf.Write(b); e == nil {
		e = mf.Sync()
	}
	closeErr := mf.Close()
	if e != nil || closeErr != nil {
		return m, ErrArchive
	}
	committed = true
	return m, nil
}
func ReadManifest(dir string) (Manifest, error) {
	var m Manifest
	if privateDir(dir) != nil {
		return m, ErrArchive
	}
	s, e := os.Lstat(filepath.Join(dir, "volumes.json"))
	if e != nil || !s.Mode().IsRegular() || s.Mode().Perm()&0077 != 0 || s.Size() > 4096 {
		return m, ErrArchive
	}
	f, e := os.Open(filepath.Join(dir, "volumes.json"))
	if e != nil {
		return m, ErrArchive
	}
	defer f.Close()
	d := json.NewDecoder(io.LimitReader(f, 4097))
	d.DisallowUnknownFields()
	if d.Decode(&m) != nil || d.Decode(new(any)) != io.EOF || m.Schema != 1 || m.Bytes < 1024 || m.Bytes > MaxBytes+(1<<30) || m.Entries < 7 || m.Entries > maxEntries {
		return m, ErrArchive
	}
	b, e := hex.DecodeString(m.SHA256)
	if e != nil || len(b) != 32 || hex.EncodeToString(b) != m.SHA256 {
		return m, ErrArchive
	}
	return m, nil
}
func archiveFile(dir string) (*os.File, error) {
	s, e := os.Lstat(filepath.Join(dir, "volumes.tar"))
	if e != nil || !s.Mode().IsRegular() || s.Mode().Perm()&0077 != 0 {
		return nil, ErrArchive
	}
	f, e := os.Open(filepath.Join(dir, "volumes.tar"))
	if e != nil {
		return nil, ErrArchive
	}
	actual, e := f.Stat()
	if e != nil || !os.SameFile(s, actual) {
		f.Close()
		return nil, ErrArchive
	}
	return f, nil
}
func validHeader(h *tar.Header) bool {
	if h.Name == "" || path.Clean(h.Name) != h.Name || strings.Contains(h.Name, "\\") || path.IsAbs(h.Name) || h.Size < 0 || h.Uid < 0 || h.Uid > 65535 || h.Gid < 0 || h.Gid > 65535 || h.Mode < 0 || h.Mode > 0777 || h.Linkname != "" || len(h.PAXRecords) > 0 {
		return false
	}
	first, _, _ := strings.Cut(h.Name, "/")
	if first == h.Name && h.Typeflag != tar.TypeDir {
		return false
	}
	ok := false
	for _, v := range Volumes {
		if first == v {
			ok = true
		}
	}
	return ok && (h.Typeflag == tar.TypeDir && h.Size == 0 || h.Typeflag == tar.TypeReg)
}

// Verify performs a complete read before Restore creates any file.
func Verify(ctx context.Context, dir string) (Manifest, error) {
	m, e := ReadManifest(dir)
	if e != nil {
		return m, e
	}
	f, e := archiveFile(dir)
	if e != nil {
		return m, e
	}
	defer f.Close()
	s, e := f.Stat()
	if e != nil || s.Size() != m.Bytes {
		return m, ErrArchive
	}
	h := sha256.New()
	if _, e = io.Copy(h, f); e != nil || hex.EncodeToString(h.Sum(nil)) != m.SHA256 {
		return m, ErrArchive
	}
	if _, e = f.Seek(0, 0); e != nil {
		return m, ErrArchive
	}
	r := tar.NewReader(f)
	seen := map[string]bool{}
	directories := map[string]bool{}
	count := 0
	var size int64
	for {
		if ctx.Err() != nil {
			return m, ctx.Err()
		}
		head, e := r.Next()
		if e == io.EOF {
			break
		}
		if e != nil || !validHeader(head) || seen[head.Name] {
			return m, ErrArchive
		}
		if parent := path.Dir(head.Name); parent != "." && !directories[parent] {
			return m, ErrArchive
		}
		if head.Typeflag == tar.TypeDir {
			directories[head.Name] = true
		}
		seen[head.Name] = true
		count++
		size += head.Size
		if count > maxEntries || size > MaxBytes {
			return m, ErrArchive
		}
		if _, e = io.Copy(io.Discard, r); e != nil {
			return m, ErrArchive
		}
	}
	for _, v := range Volumes {
		if !seen[v] {
			return m, ErrArchive
		}
	}
	if count != m.Entries {
		return m, ErrArchive
	}
	return m, nil
}
func Restore(ctx context.Context, dir, root string) error {
	if _, e := Verify(ctx, dir); e != nil {
		return e
	}
	for _, v := range Volumes {
		base := filepath.Join(root, v)
		s, e := os.Lstat(base)
		if e != nil || !s.IsDir() {
			return ErrArchive
		}
		items, e := os.ReadDir(base)
		if e != nil || len(items) != 0 {
			return ErrArchive
		}
	}
	f, e := archiveFile(dir)
	if e != nil {
		return e
	}
	defer f.Close()
	r := tar.NewReader(f)
	for {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		h, e := r.Next()
		if e == io.EOF {
			break
		}
		if e != nil || !validHeader(h) {
			return ErrArchive
		}
		dest := filepath.Join(root, filepath.FromSlash(h.Name))
		if h.Typeflag == tar.TypeDir {
			if e = os.Mkdir(dest, 0700); e != nil && !errors.Is(e, os.ErrExist) {
				return ErrArchive
			}
			s, e := os.Lstat(dest)
			if e != nil || !s.IsDir() {
				return ErrArchive
			}
		} else {
			out, e := os.OpenFile(dest, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
			if e != nil {
				return ErrArchive
			}
			_, e = io.CopyN(out, r, h.Size)
			if e == nil {
				e = out.Sync()
			}
			ce := out.Close()
			if e != nil || ce != nil {
				return ErrArchive
			}
		}
		if e = os.Chmod(dest, os.FileMode(h.Mode)); e != nil {
			return ErrArchive
		}
		if e = os.Chown(dest, h.Uid, h.Gid); e != nil {
			return ErrArchive
		}
	}
	return nil
}
