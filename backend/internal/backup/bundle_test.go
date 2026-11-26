package backup

import (
	"archive/tar"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func fakeRepository(t *testing.T) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "repo")
	for _, name := range []string{"config", "keys/" + strings.Repeat("1", 64), "snapshots/" + strings.Repeat("2", 64), "index/" + strings.Repeat("3", 64), "data/aa/" + strings.Repeat("a", 64), "locks/" + strings.Repeat("4", 64)} {
		path := filepath.Join(dir, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte("encrypted-test-object:"+name), 0600); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

func TestBundleRoundTripAndLockExclusion(t *testing.T) {
	repo := fakeRepository(t)
	bundle := filepath.Join(t.TempDir(), "backup.gateway")
	if err := Pack(context.Background(), repo, bundle); err != nil {
		t.Fatal(err)
	}
	dest := filepath.Join(t.TempDir(), "unpacked")
	if err := Unpack(context.Background(), bundle, dest, 1<<20); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dest, "locks")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("locks copied: %v", err)
	}
	body, err := os.ReadFile(filepath.Join(dest, "data", "aa", strings.Repeat("a", 64)))
	if err != nil || !strings.Contains(string(body), "encrypted-test-object") {
		t.Fatalf("missing data: %v", err)
	}
	if info, err := os.Stat(dest); err != nil || info.Mode().Perm() != 0700 {
		t.Fatalf("destination permissions: %v %v", info, err)
	}
	if err := Unpack(context.Background(), bundle, dest, 1<<20); err == nil {
		t.Fatal("existing destination accepted")
	}
}

func writeBundle(t *testing.T, headers []*tar.Header, bodies []string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "input.gateway")
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	tw := tar.NewWriter(f)
	metadata, _ := json.Marshal(bundleMetadata{Format: bundleFormat, Version: 1})
	if err := tw.WriteHeader(&tar.Header{Name: bundleMetadataName, Mode: 0600, Size: int64(len(metadata)), Typeflag: tar.TypeReg}); err != nil {
		t.Fatal(err)
	}
	if _, err := tw.Write(metadata); err != nil {
		t.Fatal(err)
	}
	for i, h := range headers {
		if err := tw.WriteHeader(h); err != nil {
			t.Fatal(err)
		}
		if i < len(bodies) && bodies[i] != "" {
			if _, err := io.WriteString(tw, bodies[i]); err != nil {
				t.Fatal(err)
			}
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestUnpackRejectsUnsafeEntriesAndCleansStaging(t *testing.T) {
	cases := map[string]*tar.Header{
		"traversal":   {Name: "repository/../escape", Mode: 0600, Typeflag: tar.TypeReg},
		"absolute":    {Name: "/repository/config", Mode: 0600, Typeflag: tar.TypeReg},
		"symlink":     {Name: "repository/config", Typeflag: tar.TypeSymlink, Linkname: "/tmp/escape"},
		"hardlink":    {Name: "repository/config", Typeflag: tar.TypeLink, Linkname: "../escape"},
		"device":      {Name: "repository/config", Typeflag: tar.TypeChar},
		"backslash":   {Name: "repository\\config", Typeflag: tar.TypeReg},
		"secret path": {Name: "repository/recovery.key", Mode: 0600, Typeflag: tar.TypeReg},
	}
	for name, h := range cases {
		t.Run(name, func(t *testing.T) {
			bundle := writeBundle(t, []*tar.Header{h}, nil)
			dest := filepath.Join(t.TempDir(), "unpacked")
			if err := Unpack(context.Background(), bundle, dest, 1<<20); err == nil {
				t.Fatal("unsafe archive accepted")
			}
			if _, err := os.Stat(dest); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("failed extraction retained: %v", err)
			}
		})
	}
}

func TestUnpackRejectsDuplicateOversizeTruncatedAndCancelled(t *testing.T) {
	repo := fakeRepository(t)
	bundle := filepath.Join(t.TempDir(), "backup.gateway")
	if err := Pack(context.Background(), repo, bundle); err != nil {
		t.Fatal(err)
	}
	t.Run("size", func(t *testing.T) {
		if err := Unpack(context.Background(), bundle, filepath.Join(t.TempDir(), "out"), 100); err == nil {
			t.Fatal("oversize bundle accepted")
		}
	})
	t.Run("truncated", func(t *testing.T) {
		body, err := os.ReadFile(bundle)
		if err != nil {
			t.Fatal(err)
		}
		short := filepath.Join(t.TempDir(), "short.gateway")
		if err := os.WriteFile(short, body[:len(body)/2], 0600); err != nil {
			t.Fatal(err)
		}
		if err := Unpack(context.Background(), short, filepath.Join(t.TempDir(), "out"), 1<<20); err == nil {
			t.Fatal("truncated bundle accepted")
		}
	})
	t.Run("duplicate", func(t *testing.T) {
		h := &tar.Header{Name: "repository/config", Mode: 0600, Typeflag: tar.TypeReg}
		duplicate := writeBundle(t, []*tar.Header{h, h}, nil)
		if err := Unpack(context.Background(), duplicate, filepath.Join(t.TempDir(), "out"), 1<<20); err == nil {
			t.Fatal("duplicate accepted")
		}
	})
	t.Run("cancelled", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		if err := Unpack(ctx, bundle, filepath.Join(t.TempDir(), "out"), 1<<20); !errors.Is(err, context.Canceled) {
			t.Fatalf("cancelled err=%v", err)
		}
	})
}
