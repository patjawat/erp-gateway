package backup

import (
	"archive/tar"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"strings"
)

const (
	bundleFormat                = "erp-gateway-restic"
	bundleMetadataName          = "gateway-backup.json"
	DefaultMaxBundleBytes int64 = 64 << 30
	maxBundleFiles              = 250000
	maxObjectBytes        int64 = 1 << 30
)

type bundleMetadata struct {
	Format  string `json:"format"`
	Version int    `json:"version"`
}

// Pack writes a portable versioned bundle of encrypted repository objects.
// Only Restic's fixed file namespaces are included; transient locks and any
// password/config staging outside that namespace are never exported.
func Pack(ctx context.Context, repositoryPath, bundlePath string) (result error) {
	if err := ctx.Err(); err != nil {
		return err
	}
	info, err := os.Lstat(repositoryPath)
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return errors.New("backup repository must be a real directory")
	}
	f, err := os.OpenFile(bundlePath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return errors.New("backup bundle destination must be new")
	}
	defer func() {
		if err := f.Close(); result == nil {
			result = err
		}
		if result != nil {
			_ = os.Remove(bundlePath)
		}
	}()
	tw := tar.NewWriter(contextWriter{ctx: ctx, writer: f})
	body, _ := json.Marshal(bundleMetadata{Format: bundleFormat, Version: 1})
	if err = tw.WriteHeader(&tar.Header{Name: bundleMetadataName, Mode: 0600, Typeflag: tar.TypeReg, Size: int64(len(body))}); err != nil {
		return err
	}
	if _, err = tw.Write(body); err != nil {
		return err
	}
	files := 0
	var total int64
	hasConfig, hasKey, hasSnapshot := false, false, false
	err = filepath.WalkDir(repositoryPath, func(filename string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		rel, err := filepath.Rel(repositoryPath, filename)
		if err != nil {
			return err
		}
		name := filepath.ToSlash(rel)
		if name == "." {
			return nil
		}
		if name == "locks" {
			if !entry.IsDir() {
				return errors.New("invalid repository lock directory")
			}
			return filepath.SkipDir
		}
		if entry.IsDir() {
			if !repositoryDirectory(name) {
				return errors.New("unexpected repository directory")
			}
			return nil
		}
		if !repositoryObject(name) {
			return errors.New("unexpected file in backup repository")
		}
		if entry.Type()&os.ModeSymlink != 0 {
			return errors.New("repository links cannot be exported")
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		if !info.Mode().IsRegular() || info.Size() > maxObjectBytes || (name == "config" && info.Size() > 1<<20) {
			return errors.New("invalid repository object type or size")
		}
		files++
		total += info.Size()
		if files > maxBundleFiles || total > DefaultMaxBundleBytes {
			return errors.New("backup repository exceeds portable export limits")
		}
		source, err := os.Open(filename)
		if err != nil {
			return err
		}
		defer source.Close()
		if err = tw.WriteHeader(&tar.Header{Name: "repository/" + name, Mode: 0600, Typeflag: tar.TypeReg, Size: info.Size()}); err != nil {
			return err
		}
		if _, err = io.CopyN(tw, contextReader{ctx: ctx, reader: source}, info.Size()); err != nil {
			return err
		}
		if err = source.Close(); err != nil {
			return err
		}
		hasConfig = hasConfig || name == "config"
		hasKey = hasKey || strings.HasPrefix(name, "keys/")
		hasSnapshot = hasSnapshot || strings.HasPrefix(name, "snapshots/")
		return nil
	})
	if err != nil {
		_ = tw.Close()
		return err
	}
	if !hasConfig || !hasKey || !hasSnapshot {
		_ = tw.Close()
		return errors.New("portable repository is missing config, key or snapshot objects")
	}
	if err = tw.Close(); err != nil {
		return err
	}
	return f.Sync()
}

// Unpack creates a new private repository directory from a portable bundle.
// It rejects unsafe names, links, unexpected objects, duplicates and excessive
// expansion. On every failure it removes only the newly created staging dir.
// Restic.Check must subsequently authenticate/decrypt the repository.
func Unpack(ctx context.Context, bundlePath, destination string, maxBytes int64) (result error) {
	if err := ctx.Err(); err != nil {
		return err
	}
	if maxBytes <= 0 || maxBytes > 1<<50 {
		return errors.New("invalid backup size limit")
	}
	if err := regularFile(bundlePath); err != nil {
		return err
	}
	f, err := os.Open(bundlePath)
	if err != nil {
		return err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return err
	}
	if info.Size() > maxBytes {
		return errors.New("backup bundle exceeds size limit")
	}
	if err := os.Mkdir(destination, 0700); err != nil {
		return errors.New("backup staging destination must not exist")
	}
	defer func() {
		if result != nil {
			_ = os.RemoveAll(destination)
		}
	}()
	limited := &io.LimitedReader{R: contextReader{ctx: ctx, reader: f}, N: maxBytes + 1}
	tr := tar.NewReader(limited)
	seen := make(map[string]struct{})
	files := 0
	var total int64
	metadataSeen, hasConfig, hasKey, hasSnapshot := false, false, false, false
	for {
		header, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return errors.New("backup bundle is truncated or malformed")
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		files++
		if files > maxBundleFiles+10 {
			return errors.New("backup bundle contains too many files")
		}
		name := header.Name
		if len(name) > 1024 || name == "" || strings.ContainsAny(name, "\\\x00") || path.IsAbs(name) || path.Clean(name) != name {
			return errors.New("backup bundle contains an unsafe path")
		}
		if _, exists := seen[name]; exists {
			return errors.New("backup bundle contains a duplicate entry")
		}
		seen[name] = struct{}{}
		if header.Typeflag == tar.TypeDir {
			if name != "repository" && (!strings.HasPrefix(name, "repository/") || !repositoryDirectory(strings.TrimPrefix(name, "repository/"))) {
				return errors.New("backup bundle contains an unexpected directory")
			}
			continue
		}
		if header.Typeflag != tar.TypeReg && header.Typeflag != tar.TypeRegA {
			return errors.New("backup bundle links and device files are not allowed")
		}
		if header.Size < 0 || header.Size > maxObjectBytes || header.Size > maxBytes-total {
			return errors.New("backup bundle exceeds expanded size limit")
		}
		total += header.Size
		if name == bundleMetadataName {
			if header.Size > 4096 {
				return errors.New("invalid backup wrapper metadata")
			}
			var metadata bundleMetadata
			decoder := json.NewDecoder(tr)
			decoder.DisallowUnknownFields()
			if err := decoder.Decode(&metadata); err != nil || metadata.Format != bundleFormat || metadata.Version != 1 {
				return errors.New("unsupported Gateway backup format")
			}
			if err := decoder.Decode(new(any)); err != io.EOF {
				return errors.New("invalid backup wrapper metadata")
			}
			metadataSeen = true
			continue
		}
		if !metadataSeen || !strings.HasPrefix(name, "repository/") {
			return errors.New("backup bundle contains an unexpected file")
		}
		rel := strings.TrimPrefix(name, "repository/")
		if !repositoryObject(rel) || (rel == "config" && header.Size > 1<<20) {
			return errors.New("backup bundle contains an invalid repository object")
		}
		dest := filepath.Join(destination, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(dest), 0700); err != nil {
			return err
		}
		out, err := os.OpenFile(dest, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
		if err != nil {
			return err
		}
		_, copyErr := io.CopyN(out, contextReader{ctx: ctx, reader: tr}, header.Size)
		if copyErr == nil {
			copyErr = out.Sync()
		}
		if err := errors.Join(copyErr, out.Close()); err != nil {
			return errors.New("backup bundle extraction failed")
		}
		hasConfig = hasConfig || rel == "config"
		hasKey = hasKey || strings.HasPrefix(rel, "keys/")
		hasSnapshot = hasSnapshot || strings.HasPrefix(rel, "snapshots/")
	}
	if !metadataSeen || !hasConfig || !hasKey || !hasSnapshot {
		return errors.New("backup bundle lacks required repository objects")
	}
	// tar.Reader stops at the end marker. Reject appended non-padding material.
	buffer := make([]byte, 32<<10)
	for {
		n, err := limited.Read(buffer)
		for _, b := range buffer[:n] {
			if b != 0 {
				return errors.New("backup bundle has trailing content")
			}
		}
		if limited.N == 0 {
			return errors.New("backup bundle exceeds size limit")
		}
		if err == io.EOF {
			break
		}
		if err != nil {
			return err
		}
	}
	return nil
}

func repositoryObject(name string) bool {
	if name == "config" {
		return true
	}
	parts := strings.Split(name, "/")
	if len(parts) == 2 && (parts[0] == "keys" || parts[0] == "snapshots" || parts[0] == "index") {
		return objectID(parts[1])
	}
	return len(parts) == 3 && parts[0] == "data" && len(parts[1]) == 2 && objectID(parts[2]) && parts[1] == parts[2][:2]
}

// ValidateObjectPath accepts only canonical immutable Restic object paths.
// Repository locks and arbitrary names can never be transferred as objects.
func ValidateObjectPath(name string) bool { return repositoryObject(name) }

type Object struct {
	Path   string `json:"path"`
	Size   int64  `json:"size"`
	SHA256 string `json:"sha256"`
}

// Inventory hashes every immutable object with bounded memory. The coordinator
// must pin and serialize the repository while preparing this inventory and any
// associated handoff descriptor; timestamps alone are never object identity.
func Inventory(ctx context.Context, directory string) ([]Object, error) {
	info, err := os.Lstat(directory)
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return nil, errors.New("repository inventory requires a real directory")
	}
	var result []Object
	var total int64
	err = filepath.WalkDir(directory, func(filename string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		rel, err := filepath.Rel(directory, filename)
		if err != nil {
			return err
		}
		name := filepath.ToSlash(rel)
		if name == "." {
			return nil
		}
		if name == "locks" {
			if !entry.IsDir() {
				return errors.New("invalid repository lock directory")
			}
			return filepath.SkipDir
		}
		if entry.IsDir() {
			if !repositoryDirectory(name) {
				return errors.New("unexpected repository directory")
			}
			return nil
		}
		if !repositoryObject(name) || !entry.Type().IsRegular() {
			return errors.New("invalid repository object")
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		if info.Size() < 0 || info.Size() > maxObjectBytes || info.Size() > DefaultMaxBundleBytes-total {
			return errors.New("repository inventory size limit exceeded")
		}
		if len(result) >= maxBundleFiles {
			return errors.New("repository inventory file limit exceeded")
		}
		total += info.Size()
		f, err := os.Open(filename)
		if err != nil {
			return err
		}
		hash := sha256.New()
		count, copyErr := io.Copy(hash, contextReader{ctx: ctx, reader: f})
		after, statErr := f.Stat()
		closeErr := f.Close()
		if err := errors.Join(copyErr, statErr, closeErr); err != nil {
			return err
		}
		if count != info.Size() || after.Size() != info.Size() || !after.ModTime().Equal(info.ModTime()) {
			return errors.New("repository changed during inventory")
		}
		result = append(result, Object{Path: name, Size: count, SHA256: hex.EncodeToString(hash.Sum(nil))})
		return nil
	})
	return result, err
}

func repositoryDirectory(name string) bool {
	if name == "data" || name == "keys" || name == "snapshots" || name == "index" {
		return true
	}
	parts := strings.Split(name, "/")
	return len(parts) == 2 && parts[0] == "data" && len(parts[1]) == 2 && hexString(parts[1])
}

func objectID(value string) bool { return len(value) == 64 && hexString(value) }
func hexString(value string) bool {
	for _, c := range value {
		if !(c >= 'a' && c <= 'f') && !(c >= '0' && c <= '9') {
			return false
		}
	}
	return value != ""
}

type contextReader struct {
	ctx    context.Context
	reader io.Reader
}

func (r contextReader) Read(buffer []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	return r.reader.Read(buffer)
}

type contextWriter struct {
	ctx    context.Context
	writer io.Writer
}

func (w contextWriter) Write(buffer []byte) (int, error) {
	if err := w.ctx.Err(); err != nil {
		return 0, err
	}
	return w.writer.Write(buffer)
}

// RepositoryObjects exposes the bounded immutable inventory for a migration
// handoff. A caller pins the repository and serializes writes during this read.
func RepositoryObjects(ctx context.Context, directory string) ([]string, error) {
	var result []string
	err := filepath.WalkDir(directory, func(filename string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		rel, err := filepath.Rel(directory, filename)
		if err != nil {
			return err
		}
		name := filepath.ToSlash(rel)
		if name == "." {
			return nil
		}
		if name == "locks" {
			if !entry.IsDir() {
				return errors.New("invalid repository lock directory")
			}
			return filepath.SkipDir
		}
		if entry.IsDir() {
			if !repositoryDirectory(name) {
				return errors.New("invalid repository directory")
			}
			return nil
		}
		if !repositoryObject(name) || !entry.Type().IsRegular() {
			return fmt.Errorf("invalid repository object")
		}
		if len(result) >= maxBundleFiles {
			return errors.New("repository inventory limit exceeded")
		}
		result = append(result, name)
		return nil
	})
	return result, err
}
