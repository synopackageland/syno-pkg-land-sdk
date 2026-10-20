package main

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
)

// File Station's "file already exists" for copy and move, "unknown error of
// file operation" for a task that failed without listing a cause, and the
// generic "failed to delete" a delete lists for each path it could not remove.
const (
	dsmNameTaken    = 1003
	dsmUnknownError = 401
	dsmDeleteFailed = 900
)

// taskAPIs maps a task kind to its File Station API and version.
var taskAPIs = map[string][2]string{
	"copy":   {"SYNO.FileStation.CopyMove", "3"},
	"move":   {"SYNO.FileStation.CopyMove", "3"},
	"delete": {"SYNO.FileStation.Delete", "2"},
}

func (d *liveDSM) Start(ctx context.Context, runner, kind string, p map[string]any) (string, error) {
	api, ok := taskAPIs[kind]
	if !ok {
		return "", errors.New("task kind")
	}
	params := map[string]any{"path": []any{p["path"]}}
	if kind == "delete" {
		params["recursive"] = p["recursive"] == true
		params["accurate_progress"] = false
	} else {
		params["dest_folder_path"] = p["dest_dir"]
		params["remove_src"] = kind == "move"
		// Without overwrite File Station refuses a taken name: the fail policy.
		if p["overwrite"] == true {
			params["overwrite"] = true
		}
	}
	data, err := d.callAsUser(ctx, runner, api[0], "start", api[1], params)
	if err != nil {
		return "", err
	}
	var out struct {
		TaskID string `json:"taskid"`
	}
	if json.Unmarshal(data, &out) != nil || out.TaskID == "" {
		return "", errors.New("task id")
	}
	return out.TaskID, nil
}

func (d *liveDSM) Status(ctx context.Context, runner, kind, id string) (taskProgress, error) {
	var got taskProgress
	api, ok := taskAPIs[kind]
	if !ok {
		return got, errors.New("task kind")
	}
	b, err := d.execAsUser(ctx, runner, api[0], "status", api[1], map[string]any{"taskid": id})
	if err != nil {
		return got, err
	}
	// A per-file error is the task's outcome, not a refused status call.
	var r envelope
	if json.Unmarshal(b, &r) != nil || !r.Success {
		_, err = decodeSuccess(b)
		if err == nil {
			err = errors.New("task status")
		}
		return got, err
	}
	var out struct {
		Finished   bool            `json:"finished"`
		Progress   float64         `json:"progress"`
		Path       json.RawMessage `json:"path"`
		Dest       string          `json:"dest_folder_path"`
		Processing string          `json:"processing_path"`
		Status     string          `json:"status"`
		Skip       json.RawMessage `json:"skipstatus"`
	}
	if json.Unmarshal(r.Data, &out) != nil {
		return got, errors.New("task status")
	}
	var paths []string
	var one string
	if json.Unmarshal(out.Path, &one) == nil {
		paths = append(paths, one)
	} else if len(out.Path) > 0 && json.Unmarshal(out.Path, &paths) != nil {
		return got, errors.New("task paths")
	}
	for _, p := range append(paths, out.Dest, out.Processing) {
		if p != "" {
			got.Paths = append(got.Paths, p)
		}
	}
	got.Finished, got.Progress = out.Finished, out.Progress
	switch {
	case hasDSMError(r.Data):
		got.Code = embeddedCode(r.Data)
	case len(out.Skip) > 0 && string(out.Skip) != "null":
		// The executor never asks DSM to skip; a skip is a taken name left as it was.
		got.Code = dsmNameTaken
	case out.Status == "FAIL":
		got.Code = dsmUnknownError
	}
	return got, nil
}

func (d *liveDSM) Stop(ctx context.Context, runner, kind, id string) error {
	api, ok := taskAPIs[kind]
	if !ok {
		return errors.New("task kind")
	}
	b, err := d.execAsUser(ctx, runner, api[0], "stop", api[1], map[string]any{"taskid": id})
	if err == nil {
		_, err = decodeSuccess(b)
	}
	return err
}

// RecycleBin reads the share's recycle bin setting as root; it is not a user call.
func (d *liveDSM) RecycleBin(ctx context.Context, _ Subject, scope string) (bool, error) {
	name := "homes"
	if scope != "home" {
		name = strings.TrimPrefix(scope, "shared:")
	}
	data, err := d.call(ctx, "SYNO.Core.Share", "get", "1", "", map[string]any{"name": name, "additional": []string{"recyclebin"}})
	if err != nil {
		return false, err
	}
	var out struct {
		Name    string `json:"name"`
		Recycle *bool  `json:"enable_recycle_bin"`
	}
	if json.Unmarshal(data, &out) != nil || out.Name != name || out.Recycle == nil {
		return false, errors.New("recycle bin setting")
	}
	return *out.Recycle, nil
}
