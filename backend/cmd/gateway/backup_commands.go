package main

import (
	"context"
	"errors"
	"io"

	"erp-platform/gateway/internal/bootstrap"
)

func runBackupCommand(ctx context.Context, args []string, directory string, input io.Reader, output io.Writer) (bool, error) {
	if len(args) == 0 {
		return false, nil
	}
	switch args[0] {
	case "backup-export":
		if len(args) != 3 {
			return true, errors.New("usage: backup-export <bundle-path> <recovery-kit-path>")
		}
		return true, bootstrap.RunBackupExport(ctx, directory, args[1], args[2])
	case "backup-import":
		if len(args) < 3 || len(args) > 4 {
			return true, errors.New("usage: backup-import <bundle-path> <recovery-kit-path> [public-url]")
		}
		public := ""
		if len(args) == 4 {
			public = args[3]
		}
		return true, bootstrap.RunBackupImport(ctx, directory, args[1], args[2], public, input, output)
	case "migration-cancel-offline":
		if len(args) != 1 {
			return true, errors.New("usage: migration-cancel-offline")
		}
		return true, bootstrap.RunMigrationCancelOffline(ctx, directory, input, output)
	}
	return false, nil
}
