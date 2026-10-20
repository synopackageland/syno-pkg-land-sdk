package main

import (
	"bufio"
	"encoding/json"
	"errors"
	"net"
	"path/filepath"
	"time"
)

// This command runs from the protected, pinned binary via the DSM root task.
// It asks the live executor to serialize selection with admission and publish;
// a second process must never write the authority file around that fence.
func rootSelectScope(pkg, scope, actor string, expectedGeneration uint64) error {
	c, err := net.DialTimeout("unix", filepath.Join(socketBase, pkg, "executor.sock"), 5*time.Second)
	if err != nil {
		return errors.New("scope executor unavailable")
	}
	defer c.Close()
	if err = c.SetDeadline(time.Now().Add(30 * time.Second)); err != nil {
		return err
	}
	if err = json.NewEncoder(c).Encode(map[string]any{"v": 1, "op": "delegate.setScope", "auth": map[string]string{}, "params": map[string]any{"scope": scope, "actor": actor, "expected_generation": expectedGeneration}}); err != nil {
		return err
	}
	scan := bufio.NewScanner(c)
	scan.Buffer(make([]byte, 4096), maxRequest)
	if !scan.Scan() {
		return errors.New("scope response unavailable")
	}
	var out Response
	if strictJSON(scan.Bytes(), &out) != nil || !out.OK {
		return errors.New("scope selection refused")
	}
	return nil
}
