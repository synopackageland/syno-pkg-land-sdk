//go:build !linux

package compose

func discoverGPU() ([]gpuNode, int) { return nil, 0 }
