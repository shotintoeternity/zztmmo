package zztgo

import (
	"os"
	"path/filepath"
)

// atomicWriteData writes the payload into the temp file. It is a variable only
// so a test can fail a write midway (M36.21); production never replaces it.
var atomicWriteData = func(f *os.File, data []byte) error {
	_, err := f.Write(data)
	return err
}

// writeFileAtomic is temp + rename, so a crash or ENOSPC mid-write leaves the
// previous file intact rather than a truncated one. The temp file sits beside
// the target (same filesystem, so the rename is atomic) under a dot-name no
// *.ZZT / *.HI glob matches, and is removed on any failure.
func writeFileAtomic(path string, data []byte, perm os.FileMode) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, "."+filepath.Base(path)+"-*.tmp")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	fail := func(err error) error {
		tmp.Close()
		os.Remove(tmpName)
		return err
	}
	if err := atomicWriteData(tmp, data); err != nil {
		return fail(err)
	}
	if err := tmp.Chmod(perm); err != nil {
		return fail(err)
	}
	if err := tmp.Sync(); err != nil {
		return fail(err)
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmpName)
		return err
	}
	if err := os.Rename(tmpName, path); err != nil {
		os.Remove(tmpName)
		return err
	}
	return nil
}
