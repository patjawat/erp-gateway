package backup

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestInventoryHashesOnlyImmutableObjects(t *testing.T) {
	repo := fakeRepository(t)
	objects, err := Inventory(context.Background(), repo)
	if err != nil {
		t.Fatal(err)
	}
	if len(objects) != 5 {
		t.Fatalf("object count=%d", len(objects))
	}
	for _, object := range objects {
		if !ValidateObjectPath(object.Path) || strings.HasPrefix(object.Path, "locks/") {
			t.Fatalf("unexpected object: %v", object)
		}
		body, err := os.ReadFile(filepath.Join(repo, filepath.FromSlash(object.Path)))
		if err != nil {
			t.Fatal(err)
		}
		hash := sha256.Sum256(body)
		if object.Size != int64(len(body)) || object.SHA256 != hex.EncodeToString(hash[:]) {
			t.Fatalf("inventory mismatch: %v", object)
		}
	}
	for _, unsafe := range []string{"../config", "/config", "locks/" + strings.Repeat("a", 64), "data/aa/" + strings.Repeat("b", 64), "keys/not-an-id", "config/extra", "data\\aa\\" + strings.Repeat("a", 64)} {
		if ValidateObjectPath(unsafe) {
			t.Fatalf("unsafe object path accepted %q", unsafe)
		}
	}
	if err := os.WriteFile(filepath.Join(repo, "recovery.key"), []byte("secret"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := Inventory(context.Background(), repo); err == nil {
		t.Fatal("unexpected secret file accepted")
	}
}
