//go:build !linux

package main

import "fmt"

func applyNoFile(n int) error {
	if n == 0 {
		return nil
	}
	return fmt.Errorf("daemon nofile requires Linux package system units")
}
