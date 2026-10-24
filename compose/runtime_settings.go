package compose

import (
	"errors"
	"io"
	"os"
	"strings"
	"syscall"
	"unicode"
	"unicode/utf8"
)

// RuntimeSettingsVersion identifies bounded installation values for build tools.
const RuntimeSettingsVersion = 2

// Settings are nonempty private UTF-8 strings, not generated authentication keys.
// Missing values fail before the upstream is started. Never evaluate their text.
func readRuntimeSetting(file string) ([]byte, error) {
	return readRuntimeValue(file, 1)
}

func readRuntimeValue(file string, minimum int64) ([]byte, error) {
	bad := errors.New("runtime setting must be a private bounded package-owned regular file")
	st, err := os.Lstat(file)
	if err != nil {
		return nil, err
	}
	owner, ok := st.Sys().(*syscall.Stat_t)
	if !st.Mode().IsRegular() || st.Mode().Perm()&0077 != 0 || st.Size() < minimum || st.Size() > 2048 || !ok || owner.Uid != uint32(os.Geteuid()) || owner.Nlink != 1 {
		return nil, bad
	}
	f, err := os.OpenFile(file, os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	opened, err := f.Stat()
	if err != nil || !os.SameFile(st, opened) {
		return nil, bad
	}
	b, err := io.ReadAll(io.LimitReader(f, 2049))
	if err != nil {
		return nil, err
	}
	if int64(len(b)) < minimum || len(b) > 2048 || !utf8.Valid(b) || strings.IndexFunc(string(b), unicode.IsControl) >= 0 {
		return nil, bad
	}
	return b, nil
}
