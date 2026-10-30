//go:build !linux && !darwin

package diskspace

import "os"

func Available(*os.File) (int64, error) { return 0, os.ErrInvalid }
