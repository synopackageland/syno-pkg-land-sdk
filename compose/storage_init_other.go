//go:build !linux

package compose

import "fmt"

func initializeStorageDirectory(WritableDirectory) error {
	return fmt.Errorf("container storage initialization requires Linux")
}

func resetScratchDirectory(string) error {
	return fmt.Errorf("container scratch requires Linux")
}
