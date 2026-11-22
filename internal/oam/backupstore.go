package oam

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// BackupStore keeps the backups: for each, the archive and the
// manifest. Several OAM instances that share one store see the same
// backups.
type BackupStore interface {
	// Create starts the archive of a new backup. Open does not see it
	// until Commit.
	Create(id string) (ArchiveWriter, error)
	// Open reads the archive of a backup; ErrNotFound when there is
	// none.
	Open(id string) (io.ReadCloser, error)
	// SaveManifest writes the manifest of a backup, in place of the one
	// it had.
	SaveManifest(Manifest) error
	// Manifest reads the manifest of a backup; ErrNotFound when there is
	// none.
	Manifest(id string) (Manifest, error)
	// Manifests reads every manifest, the newest first.
	Manifests() ([]Manifest, error)
}

// ArchiveWriter writes the archive of a new backup.
type ArchiveWriter interface {
	io.Writer
	// Commit makes the archive whole and readable.
	Commit() error
	// Abort drops what was written.
	Abort() error
}

// FileBackupStore keeps each backup in a directory as two files:
// <id>.jsonl, the archive, and <id>.json, the manifest. An archive is
// written as <id>.jsonl.partial and renamed when it is whole, so a
// backup that stopped half way leaves no archive that a restore would
// read.
//
// An archive holds the credentials of every subscription, K, OPc and
// passwords, as the HSS needs them back. The directory is 0700 and each
// file 0600, readable by the user of the OAM only.
//
// Several OAM instances can share the directory on one volume. Two
// restores of one backup at the same moment may lose one of the two
// runs in the manifest, which records the run after it ends.
//
// SIMPLIFIED: TS 33.102 6.3.1. The archive is not encrypted: the file
// permissions are its only protection. The key K is available only to
// the USIM and the AuC of the home environment, so a copy of it outside
// the HSS must be as well kept as the AuC, and a product encrypts such a
// backup. Here only test credentials may be in the network, and
// encryption would need a key of its own and its handling, which is not
// what the backup and restore teach.
type FileBackupStore struct {
	dir string
}

// NewFileBackupStore uses dir, which it makes if need be, and limits to
// its owner.
func NewFileBackupStore(dir string) (*FileBackupStore, error) {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	if err := os.Chmod(dir, 0o700); err != nil {
		return nil, err
	}
	return &FileBackupStore{dir: dir}, nil
}

// checkID refuses an ID that is not of letters, digits and hyphens, so
// that no ID names a path outside the directory.
func checkID(id string) error {
	if id == "" || strings.Trim(id, "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789-") != "" || id[0] == '-' {
		return fmt.Errorf("%w: backup %q", ErrNotFound, id)
	}
	return nil
}

func (f *FileBackupStore) path(id, ext string) string { return filepath.Join(f.dir, id+ext) }

func (f *FileBackupStore) Create(id string) (ArchiveWriter, error) {
	if err := checkID(id); err != nil {
		return nil, err
	}
	if _, err := os.Stat(f.path(id, ".jsonl")); err == nil {
		return nil, fmt.Errorf("backup %s exists", id)
	}
	file, err := os.OpenFile(f.path(id, ".jsonl.partial"), os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return nil, err
	}
	return &fileArchive{file: file, final: f.path(id, ".jsonl")}, nil
}

type fileArchive struct {
	file  *os.File
	final string
}

func (a *fileArchive) Write(p []byte) (int, error) { return a.file.Write(p) }

func (a *fileArchive) Commit() error {
	err := a.file.Sync()
	if closeErr := a.file.Close(); err == nil {
		err = closeErr
	}
	if err == nil {
		err = os.Rename(a.file.Name(), a.final)
	}
	if err != nil {
		os.Remove(a.file.Name())
	}
	return err
}

func (a *fileArchive) Abort() error {
	a.file.Close()
	return os.Remove(a.file.Name())
}

func (f *FileBackupStore) Open(id string) (io.ReadCloser, error) {
	if err := checkID(id); err != nil {
		return nil, err
	}
	file, err := os.Open(f.path(id, ".jsonl"))
	if errors.Is(err, fs.ErrNotExist) {
		return nil, fmt.Errorf("%w: no archive of backup %s", ErrNotFound, id)
	}
	return file, err
}

func (f *FileBackupStore) SaveManifest(m Manifest) error {
	if err := checkID(m.ID); err != nil {
		return err
	}
	b, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(f.dir, m.ID+".json.*.tmp")
	if err != nil {
		return err
	}
	_, err = tmp.Write(append(b, '\n'))
	if err == nil {
		err = tmp.Sync()
	}
	if closeErr := tmp.Close(); err == nil {
		err = closeErr
	}
	// CreateTemp makes the file 0600, and the rename keeps that.
	if err == nil {
		err = os.Rename(tmp.Name(), f.path(m.ID, ".json"))
	}
	if err != nil {
		os.Remove(tmp.Name())
	}
	return err
}

func (f *FileBackupStore) Manifest(id string) (Manifest, error) {
	if err := checkID(id); err != nil {
		return Manifest{}, err
	}
	b, err := os.ReadFile(f.path(id, ".json"))
	if errors.Is(err, fs.ErrNotExist) {
		return Manifest{}, fmt.Errorf("%w: backup %s", ErrNotFound, id)
	}
	if err != nil {
		return Manifest{}, err
	}
	var m Manifest
	if err := json.Unmarshal(b, &m); err != nil {
		return Manifest{}, fmt.Errorf("manifest of %s: %w", id, err)
	}
	return m, nil
}

func (f *FileBackupStore) Manifests() ([]Manifest, error) {
	entries, err := os.ReadDir(f.dir)
	if err != nil {
		return nil, err
	}
	ms := []Manifest{}
	for _, e := range entries {
		id, ok := strings.CutSuffix(e.Name(), ".json")
		if !ok || e.IsDir() || checkID(id) != nil {
			continue
		}
		m, err := f.Manifest(id)
		if err != nil {
			return nil, err
		}
		ms = append(ms, m)
	}
	sortManifests(ms)
	return ms, nil
}
