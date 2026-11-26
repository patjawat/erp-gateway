package backup

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"syscall"
	"time"
)

const DefaultResticBinary = "/usr/local/bin/restic"
const repositoryCancelGrace = 5 * time.Second

// Repository contains only trusted coordinator inputs. Env permits the cloud
// adapter to select its private rclone config and fixed bundled executable;
// neither Env nor Binary may be accepted from an HTTP request.
type Repository struct {
	Binary       string
	Path         string
	PasswordFile string
	Env          []string
}

type Snapshot struct {
	ID    string    `json:"id"`
	Time  time.Time `json:"time"`
	Paths []string  `json:"paths,omitempty"`
	Tags  []string  `json:"tags,omitempty"`
}

// Init refuses to overwrite an existing repository. A caller opening a known
// repository should use Check instead of treating any init error as success.
func (r Repository) Init(ctx context.Context) error {
	_, err := r.run(ctx, "", "init", "--repository-version", "2")
	return err
}

// Backup captures a prepared fixed-layout directory. The capture directory is
// not logged; Restic encrypts its file names and content. Partial-backup status
// (exit 3) is an error, even if the output contains a snapshot ID.
func (r Repository) Backup(ctx context.Context, directory string) (string, error) {
	info, err := os.Lstat(directory)
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return "", errors.New("backup capture must be a real directory")
	}
	directory, err = filepath.Abs(directory)
	if err != nil {
		return "", err
	}
	output, err := r.run(ctx, directory, "backup", "--host", "erp-gateway", "--tag", "gateway-full-backup", "--one-file-system", ".")
	if err != nil {
		return "", err
	}
	var snapshotID string
	scanner := bufio.NewScanner(bytes.NewReader(output))
	scanner.Buffer(make([]byte, 4096), 1<<20)
	for scanner.Scan() {
		var summary struct {
			MessageType string `json:"message_type"`
			SnapshotID  string `json:"snapshot_id"`
		}
		if err := json.Unmarshal(scanner.Bytes(), &summary); err != nil {
			return "", errors.New("invalid backup tool response")
		}
		if summary.MessageType == "summary" {
			snapshotID = summary.SnapshotID
		}
	}
	if err := scanner.Err(); err != nil || !objectID(snapshotID) {
		return "", errors.New("backup tool did not confirm a complete snapshot")
	}
	return snapshotID, nil
}

// Restore restores exactly one authenticated snapshot, flattening the capture
// root so its fixed logical filenames appear directly inside destination.
// Destination must not exist. It is removed if validation/restore fails.
func (r Repository) Restore(ctx context.Context, snapshotID, destination string) (result error) {
	if !objectID(snapshotID) {
		return errors.New("restore requires an exact full snapshot ID")
	}
	snapshot, err := r.Snapshot(ctx, snapshotID)
	if err != nil {
		return err
	}
	if len(snapshot.Paths) != 1 {
		return errors.New("restore requires one exact single-root capture")
	}
	sourceRoot := snapshot.Paths[0]
	if !path.IsAbs(sourceRoot) || path.Clean(sourceRoot) != sourceRoot || strings.ContainsAny(sourceRoot, "\x00\r\n\\") || len(sourceRoot) > 4096 {
		return errors.New("invalid snapshot capture root")
	}
	// `backup .` records the absolute source in snapshot.Paths, but the actual
	// encrypted tree starts at /. Using Paths[0] as a restore subfolder would
	// inspect/restore an empty tree, as opposed to the fixed logical files.
	if err := r.validateTree(ctx, snapshotID, "/"); err != nil {
		return err
	}
	if err := os.Mkdir(destination, 0700); err != nil {
		return errors.New("restore staging destination must not exist")
	}
	defer func() {
		if result != nil {
			_ = os.RemoveAll(destination)
		}
	}()
	_, err = r.runRead(ctx, "restore", snapshotID, "--target", destination, "--verify")
	if err != nil {
		return err
	}
	return filepath.WalkDir(destination, func(filename string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		if entry.IsDir() {
			if err := os.Chmod(filename, 0700); err != nil {
				return err
			}
		} else {
			if !entry.Type().IsRegular() {
				return errors.New("restored snapshot contains an unexpected file type")
			}
			if err := os.Chmod(filename, 0600); err != nil {
				return err
			}
		}
		return os.Chown(filename, os.Geteuid(), os.Getegid())
	})
}

func (r Repository) validateTree(ctx context.Context, snapshotID, root string) error {
	output, err := r.runRead(ctx, "ls", "--recursive", snapshotID)
	if err != nil {
		return err
	}
	scanner := bufio.NewScanner(bytes.NewReader(output))
	scanner.Buffer(make([]byte, 4096), 1<<20)
	count := 0
	var total int64
	for scanner.Scan() {
		var node struct {
			StructType string `json:"struct_type"`
			Type       string `json:"type"`
			Path       string `json:"path"`
			Size       int64  `json:"size"`
		}
		if err := json.Unmarshal(scanner.Bytes(), &node); err != nil {
			return errors.New("invalid encrypted snapshot tree")
		}
		if node.StructType != "node" {
			continue
		}
		count++
		if count > maxBundleFiles {
			return errors.New("snapshot contains too many files")
		}
		if node.Type != "file" && node.Type != "dir" {
			return errors.New("snapshot links and special files cannot be restored")
		}
		if !path.IsAbs(node.Path) || path.Clean(node.Path) != node.Path || strings.ContainsAny(node.Path, "\x00\\") || len(node.Path) > 8192 {
			return errors.New("snapshot contains an unsafe path")
		}
		if node.Path != root && root != "/" && !strings.HasPrefix(node.Path, root+"/") {
			return errors.New("snapshot file is outside the capture root")
		}
		if node.Size < 0 || node.Size > DefaultMaxBundleBytes-total {
			return errors.New("snapshot exceeds expanded restore limit")
		}
		total += node.Size
	}
	if err := scanner.Err(); err != nil {
		return errors.New("snapshot tree exceeds inspection limit")
	}
	if count == 0 {
		return errors.New("snapshot capture is empty")
	}
	return nil
}

// Check authenticates metadata and reads all data packs, not just the catalog.
func (r Repository) Check(ctx context.Context) error {
	_, err := r.run(ctx, "", "check", "--read-data")
	return err
}

// CheckMetadata authenticates catalog/index structure without rereading every
// historical data pack on each scheduled off-site backup.
func (r Repository) CheckMetadata(ctx context.Context) error {
	_, err := r.run(ctx, "", "check")
	return err
}

// CheckSubset rotates deterministic data-pack reads across maintenance runs.
// Full Check remains available for isolated exports and explicit rehearsals.
func (r Repository) CheckSubset(ctx context.Context, part, total int) error {
	if total < 1 || total > 365 || part < 1 || part > total {
		return errors.New("invalid repository data-check subset")
	}
	_, err := r.run(ctx, "", "check", "--read-data-subset", fmt.Sprintf("%d/%d", part, total))
	return err
}

func (r Repository) Snapshots(ctx context.Context) ([]Snapshot, error) { return r.snapshots(ctx) }

// Snapshot resolves exactly one ID without enumerating the repository catalog.
func (r Repository) Snapshot(ctx context.Context, id string) (Snapshot, error) {
	if !objectID(id) {
		return Snapshot{}, errors.New("snapshot inspection requires an exact full ID")
	}
	items, err := r.snapshots(ctx, id)
	if err != nil {
		return Snapshot{}, err
	}
	if len(items) != 1 || items[0].ID != id {
		return Snapshot{}, errors.New("exact snapshot was not found")
	}
	return items[0], nil
}

func (r Repository) snapshots(ctx context.Context, ids ...string) ([]Snapshot, error) {
	args := append([]string{"snapshots"}, ids...)
	var output []byte
	var err error
	if len(ids) > 0 {
		output, err = r.runRead(ctx, args...)
	} else {
		output, err = r.run(ctx, "", args...)
	}
	if err != nil {
		return nil, err
	}
	var result []Snapshot
	if err := json.Unmarshal(output, &result); err != nil {
		return nil, errors.New("invalid snapshot catalog response")
	}
	if len(result) > maxBundleFiles {
		return nil, errors.New("snapshot catalog exceeds inspection limit")
	}
	for _, snapshot := range result {
		if !objectID(snapshot.ID) || snapshot.Time.IsZero() {
			return nil, errors.New("invalid snapshot catalog entry")
		}
	}
	sort.Slice(result, func(i, j int) bool {
		if result[i].Time.Equal(result[j].Time) {
			return result[i].ID > result[j].ID
		}
		return result[i].Time.After(result[j].Time)
	})
	return result, nil
}

// ID returns the authenticated repository identity for migration binding.
func (r Repository) ID(ctx context.Context) (string, error) {
	output, err := r.runRead(ctx, "cat", "config")
	if err != nil {
		return "", err
	}
	var info struct {
		ID      string `json:"id"`
		Version int    `json:"version"`
	}
	if err := json.Unmarshal(output, &info); err != nil || !objectID(info.ID) || info.Version != 2 {
		return "", errors.New("unsupported encrypted repository config")
	}
	return info.ID, nil
}

// Forget removes snapshots not in keep. It never prunes data packs and refuses
// an empty or missing keep set. The coordinator must hold its independent
// policy/repository lock across policy activation, this operation and pruning.
func (r Repository) Forget(ctx context.Context, keep []string) error {
	if len(keep) == 0 {
		return errors.New("retention must keep at least one snapshot")
	}
	wanted := make(map[string]bool, len(keep))
	for _, id := range keep {
		if !objectID(id) {
			return errors.New("invalid retained snapshot ID")
		}
		wanted[id] = false
	}
	snapshots, err := r.Snapshots(ctx)
	if err != nil {
		return err
	}
	var remove []string
	for _, snapshot := range snapshots {
		if _, ok := wanted[snapshot.ID]; ok {
			wanted[snapshot.ID] = true
		} else {
			remove = append(remove, snapshot.ID)
		}
	}
	for _, found := range wanted {
		if !found {
			return errors.New("retention keep set no longer matches repository")
		}
	}
	if len(remove) == 0 {
		return nil
	}
	if len(remove) > 10000 {
		return errors.New("retention deletion batch exceeds limit")
	}
	_, err = r.run(ctx, "", append([]string{"forget"}, remove...)...)
	return err
}

// Prune is intentionally separate from metadata deletion and must be performed
// only under the coordinator's active retention policy/repository lock.
func (r Repository) Prune(ctx context.Context) error { _, err := r.run(ctx, "", "prune"); return err }

// RecoverLocalLocks is an EXPLICIT crash-recovery operation, never an automatic
// fallback for a failed command. The caller must exclusively own this managed
// private local repository (service flock plus operation/transfer mutex), and
// establish that no previous child is still running. Shared Drive repositories
// are deliberately rejected; their immutable snapshots may be read without a
// lock instead, but their live peers' locks must never be removed here.
func (r Repository) RecoverLocalLocks(ctx context.Context) error {
	if r.Path == "" || strings.HasPrefix(r.Path, "rclone:") || strings.Contains(r.Path, "://") || (!filepath.IsAbs(r.Path) && strings.Contains(r.Path, ":")) {
		return errors.New("lock recovery is restricted to an exclusively owned private local repository")
	}
	path, err := filepath.Abs(r.Path)
	if err != nil {
		return err
	}
	info, err := os.Lstat(path)
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 || info.Mode().Perm()&0077 != 0 {
		return errors.New("lock recovery requires a private real local repository directory")
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok || stat.Uid != uint32(os.Geteuid()) {
		return errors.New("lock recovery requires repository ownership by the current service account")
	}
	if err := regularFile(filepath.Join(path, "config")); err != nil {
		return errors.New("local repository config is unavailable")
	}
	_, err = r.run(ctx, "", "unlock", "--remove-all")
	return err
}

// runRead bypasses native repository locks only for immutable authenticated
// reads. Concurrent pruning may make a read fail, but Restore writes to fresh
// private staging, verifies every file, and removes the partial result on error.
// This never removes a shared lock or authorizes no-lock mutation commands.
func (r Repository) runRead(ctx context.Context, args ...string) ([]byte, error) {
	if len(args) == 0 {
		return nil, errors.New("missing repository read operation")
	}
	switch args[0] {
	case "snapshots", "ls", "cat", "restore":
	default:
		return nil, errors.New("no-lock repository mutations are prohibited")
	}
	return r.runCommand(ctx, "", true, args...)
}

func (r Repository) run(ctx context.Context, directory string, args ...string) ([]byte, error) {
	return r.runCommand(ctx, directory, false, args...)
}

func (r Repository) runCommand(ctx context.Context, directory string, noLock bool, args ...string) ([]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if len(args) == 0 {
		return nil, errors.New("missing repository operation")
	}
	binary := r.Binary
	if binary == "" {
		binary = DefaultResticBinary
	}
	if !filepath.IsAbs(binary) || strings.ContainsAny(binary, "\x00\r\n") {
		return nil, errors.New("backup executable must be a fixed absolute path")
	}
	if r.Path == "" || strings.ContainsAny(r.Path, "\x00\r\n") {
		return nil, errors.New("invalid repository destination")
	}
	repositoryPath := r.Path
	if !strings.HasPrefix(repositoryPath, "rclone:gateway:") {
		if !filepath.IsAbs(repositoryPath) && strings.Contains(repositoryPath, ":") {
			return nil, errors.New("repository destination must be managed local storage or configured Drive")
		}
		var err error
		repositoryPath, err = filepath.Abs(repositoryPath)
		if err != nil {
			return nil, err
		}
	}
	passwordFile, err := filepath.Abs(r.PasswordFile)
	if err != nil {
		return nil, errors.New("invalid backup repository password path")
	}
	if err := regularFile(passwordFile); err != nil {
		return nil, errors.New("backup repository password file is unavailable")
	}
	info, err := os.Stat(passwordFile)
	if err != nil || info.Mode().Perm()&0077 != 0 || info.Size() == 0 || info.Size() > 64<<10 {
		return nil, errors.New("backup repository password file must be private and bounded")
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Minute)
	defer cancel()
	base := []string{"--no-cache", "--json", "--repo", repositoryPath, "--password-file", passwordFile}
	if noLock {
		base = append(base, "--no-lock")
	}
	command := exec.CommandContext(ctx, binary, append(base, args...)...)
	// Restic handles SIGINT by cancelling work and removing its own native lock.
	// Default CommandContext SIGKILL strands that lock across container hostname
	// changes. Kill remains a bounded fallback if cooperative shutdown stalls.
	command.Cancel = func() error { return command.Process.Signal(os.Interrupt) }
	command.WaitDelay = repositoryCancelGrace
	command.Dir = directory
	// Discard inherited Restic overrides, especially password-command and the
	// alternate repository file. Trusted cloud-specific overrides are supplied
	// explicitly, before flags pin the repository and password file above.
	for _, item := range os.Environ() {
		if !strings.HasPrefix(item, "RESTIC_") {
			command.Env = append(command.Env, item)
		}
	}
	command.Env = append(command.Env, r.Env...)
	stdout := &boundedBuffer{max: 16 << 20}
	stderr := &boundedBuffer{max: 32 << 10}
	command.Stdout = stdout
	command.Stderr = stderr
	if err := command.Run(); err != nil {
		if ctx.Err() != nil {
			return nil, fmt.Errorf("backup %s: %w", args[0], ctx.Err())
		}
		var exit *exec.ExitError
		if errors.As(err, &exit) {
			return nil, fmt.Errorf("backup %s failed (exit %d)", args[0], exit.ExitCode())
		}
		return nil, errors.New("bundled backup executable is unavailable")
	}
	if stdout.overflow {
		return nil, errors.New("backup tool response exceeds inspection limit")
	}
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("backup %s: %w", args[0], err)
	}
	return stdout.buffer.Bytes(), nil
}

type boundedBuffer struct {
	buffer   bytes.Buffer
	max      int
	overflow bool
}

func (b *boundedBuffer) Write(data []byte) (int, error) {
	n := len(data)
	remaining := b.max - b.buffer.Len()
	if len(data) > remaining {
		b.overflow = true
		data = data[:remaining]
	}
	_, _ = b.buffer.Write(data)
	return n, nil
}
