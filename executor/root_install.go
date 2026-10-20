package main

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"path/filepath"
	"sort"
)

// installRootPrograms stages the complete policy inventory before publishing
// any entry. Only protected copies with identical binary digests share an
// inode; source files are never linked into the runtime. Publication replaces
// names rather than writing through a previous start's hard links.
func installRootPrograms(source string, r rootPrograms, entries map[string]RootEntry) error {
	if os.Geteuid() != 0 {
		return errors.New("root required")
	}
	if err := secureDir(r.Dir, 0700); err != nil {
		return err
	}
	names := make([]string, 0, len(entries))
	for name, entry := range entries {
		if !tokenRE.MatchString(name) || !entry.valid() {
			return errors.New("root inventory")
		}
		names = append(names, name)
	}
	sort.Strings(names)
	if len(names) == 0 {
		return nil
	}
	// DSM's package target is a symlink. Its contents remain untrusted; every
	// staged file must independently match the approved policy's digest.
	source, err := filepath.EvalSymlinks(source)
	if err != nil {
		return err
	}
	stage, err := os.MkdirTemp(r.Dir, ".root-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(stage)
	staged := rootPrograms{Dir: stage}
	if err = os.Mkdir(filepath.Join(stage, "root"), 0700); err != nil {
		return err
	}
	binaries := map[string]string{}
	for _, name := range names {
		entry := entries[name]
		path := staged.path(name, entry.Kind)
		if err = copyPinnedRoot(filepath.Join(source, filepath.Base(path)), path, entry); err != nil {
			return err
		}
		if entry.Kind == "binary" {
			if previous := binaries[entry.Digest]; previous != "" {
				if err = os.Remove(path); err != nil {
					return err
				}
				if err = os.Link(previous, path); err != nil {
					return err
				}
			} else {
				binaries[entry.Digest] = path
			}
		}
	}
	root := filepath.Join(r.Dir, "root")
	if err = os.Mkdir(root, 0700); err != nil && !os.IsExist(err) {
		return err
	}
	if err = trustedTree(root); err != nil {
		return err
	}
	for _, name := range names {
		entry := entries[name]
		if err = os.Rename(staged.path(name, entry.Kind), r.path(name, entry.Kind)); err != nil {
			return err
		}
	}
	return nil
}

func copyPinnedRoot(source, dest string, entry RootEntry) error {
	src, err := openRegular(source)
	if err != nil {
		return err
	}
	defer src.Close()
	info, err := src.Stat()
	if err != nil {
		return err
	}
	mode := os.FileMode(0600)
	if entry.Kind == "binary" {
		mode = 0700
	}
	out, err := os.OpenFile(dest, os.O_WRONLY|os.O_CREATE|os.O_EXCL, mode)
	if err != nil {
		return err
	}
	defer out.Close()
	hash := sha256.New()
	if err = copyExact(io.MultiWriter(out, hash), src, info.Size()); err != nil {
		return err
	}
	if hex.EncodeToString(hash.Sum(nil)) != entry.Digest {
		return errors.New("root program digest")
	}
	if err = out.Chmod(mode); err != nil {
		return err
	}
	if err = out.Sync(); err != nil {
		return err
	}
	return out.Close()
}
