//go:build !linux && !darwin

package background

func heartbeatBases() []string         { return nil }
func memoryFilesystem(string) error    { return ErrInvalid }
func processStamp(int) (string, error) { return "", ErrInvalid }
