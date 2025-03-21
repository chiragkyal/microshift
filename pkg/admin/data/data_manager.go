package data

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/openshift/microshift/pkg/config"
	"github.com/openshift/microshift/pkg/util"
	"k8s.io/klog/v2"
)

var (
	cpArgs = []string{
		"--verbose",
		"--recursive",
		"--preserve",
		"--reflink=auto",
	}
)

func NewManager(storage StoragePath) (*manager, error) {
	if storage == "" {
		return nil, &EmptyArgErr{argName: "storage"}
	}
	return &manager{storage: storage}, nil
}

var _ Manager = (*manager)(nil)

type manager struct {
	storage StoragePath
}

func (dm *manager) GetBackupPath(name BackupName) string {
	return filepath.Join(string(dm.storage), string(name))
}

func (dm *manager) BackupExists(name BackupName) (bool, error) {
	return pathExists(dm.GetBackupPath(name))
}

func (dm *manager) RemoveBackup(name BackupName) error {
	klog.InfoS("Removing backup",
		"name", name,
	)
	if err := os.RemoveAll(dm.GetBackupPath(name)); err != nil {
		return fmt.Errorf("failed to delete backup %q: %w", name, err)
	}
	klog.InfoS("Removed backup",
		"name", name,
	)
	return nil
}

func (dm *manager) GetBackupList() ([]BackupName, error) {
	if exists, err := util.PathExists(config.BackupsDir); err != nil {
		return nil, err
	} else if !exists {
		return []BackupName{}, nil
	}

	files, err := os.ReadDir(config.BackupsDir)
	if err != nil {
		return nil, err
	}

	backups := make([]BackupName, 0, len(files))
	for _, file := range files {
		if file.IsDir() {
			backups = append(backups, BackupName(file.Name()))
		}
	}

	return backups, nil
}

func (dm *manager) Backup(name BackupName) error {
	klog.InfoS("Starting backup",
		"storage", dm.storage,
		"name", name,
		"data", config.DataDir,
	)

	if name == "" {
		return &EmptyArgErr{"name"}
	}

	if exists, err := dm.BackupExists(name); err != nil {
		return fmt.Errorf("failed to determine if backup %q exists: %w", name, err)
	} else if exists {
		return fmt.Errorf("failed to create backup destination %q because it already exists",
			name)
	}

	if found, err := pathExists(string(dm.storage)); err != nil {
		return fmt.Errorf("failed to determine if storage location %q for backup exists: %w",
			dm.storage, err)
	} else if !found {
		if makeDirErr := util.MakeDir(string(dm.storage)); makeDirErr != nil {
			return fmt.Errorf("failed to create backup storage directory %q: %w",
				dm.storage, makeDirErr)
		}
		klog.InfoS("Created backup storage directory", "path", dm.storage)
	} else {
		klog.InfoS("Found existing backup storage directory", "path", dm.storage)
	}

	dest := dm.GetBackupPath(name)

	if err := copyPath(config.DataDir, dest); err != nil {
		return err
	}

	klog.InfoS("Backup finished", "backup", dest, "data", config.DataDir)
	return nil
}

func (dm *manager) Restore(name BackupName) error {
	klog.InfoS("Starting restore",
		"storage", dm.storage,
		"name", name,
		"data", config.DataDir,
	)

	if name == "" {
		return &EmptyArgErr{"name"}
	}

	if exists, err := dm.BackupExists(name); err != nil {
		return fmt.Errorf("failed to determine if backup %q exists: %w", name, err)
	} else if !exists {
		return fmt.Errorf("failed to restore backup, %q does not exist", name)
	}

	tmp := fmt.Sprintf("%s.saved", config.DataDir)
	klog.InfoS("Renaming existing data dir", "data", config.DataDir, "renamedTo", tmp)
	if err := os.Rename(config.DataDir, tmp); err != nil {
		return fmt.Errorf("failed to rename existing data directory %q to %q: %w",
			config.DataDir, tmp, err)
	}

	src := dm.GetBackupPath(name)
	if err := copyPath(src, config.DataDir); err != nil {
		klog.ErrorS(err, "Failed to restore from backup, restoring current data dir")

		if err := os.RemoveAll(config.DataDir); err != nil {
			return fmt.Errorf("failed to remove data directory %q: %w", config.DataDir, err)
		}

		if err := os.Rename(tmp, config.DataDir); err != nil {
			return fmt.Errorf("failed to rename temporary directory %q to %q: %w",
				tmp, config.DataDir, err)
		}

		return fmt.Errorf("failed to restore backup: %w", err)
	}

	klog.InfoS("Removing temporary data directory", "path", tmp)
	if err := os.RemoveAll(tmp); err != nil {
		klog.ErrorS(err, "Failed to remove temporary data directory, leaving in place", "path", tmp)
	}

	klog.InfoS("Finished restore",
		"name", name,
		"data", config.DataDir,
	)
	return nil
}

func (dm *manager) RemoveData() error {
	klog.InfoS("Starting MicroShift data removal")

	err := os.RemoveAll(config.DataDir)
	if err != nil {
		return fmt.Errorf("failed to remove MicroShift data: %w", err)
	}

	klog.InfoS("Removed MicroShift data")
	return nil
}

func copyPath(src, dest string) error {
	cmd := exec.Command("cp", append(cpArgs, src, dest)...) //nolint:gosec
	klog.InfoS("Starting copy", "cmd", cmd)

	var outb, errb bytes.Buffer
	cmd.Stdout = &outb
	cmd.Stderr = &errb
	err := cmd.Run()

	if err != nil {
		klog.InfoS("Failed to copy", "cmd", cmd,
			"stdout", strings.ReplaceAll(outb.String(), "\n", `, `),
			"stderr", errb.String())
		return fmt.Errorf("failed to copy %q to %q: %w", src, dest, err)
	}

	klog.InfoS("Finished copy", "cmd", cmd)
	return nil
}

func pathExists(path string) (bool, error) {
	exists, err := util.PathExists(path)
	if err != nil {
		return false, fmt.Errorf("failed to check if path %q exists: %w", path, err)
	}
	return exists, nil
}
