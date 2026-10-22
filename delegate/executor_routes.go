package delegate

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"time"

	"github.com/synopackageland/syno-pkg-land-sdk/api"
)

// ExecutorRoutes are executor.status and executor.control, the routes the
// CLI's root window calls after approval: status answers the executor's own
// pins, and control writes the package's lifecycle mailbox
// <dataDir>/executor/control. A Go App and the bridge runtime serve these
// same routes; their permissions are the SDK's (rootOps).
func ExecutorRoutes(c ExecutorClient, dataDir string) []api.Route {
	mailbox := filepath.Join(dataDir, "executor")
	return []api.Route{
		api.Handle("executor.control", func(_ context.Context, caller api.Caller, raw json.RawMessage) (any, error) {
			return executorControl(caller, raw, mailbox)
		}),
		api.Handle("executor.status", func(ctx context.Context, caller api.Caller, _ json.RawMessage) (any, error) {
			result, err := executorStatus(ctx, c, mailbox)
			if err != nil || !caller.Admin() {
				return result, err
			}
			if diagnostic := protectedApproval(c); diagnostic != nil {
				b, marshalErr := json.Marshal(result)
				var status map[string]any
				if marshalErr == nil && json.Unmarshal(b, &status) == nil && status != nil {
					status["approval"] = diagnostic
					return status, nil
				}
			}
			return result, nil
		}),
	}
}

// Control changes a fixed package-owned file that the package's lifecycle
// reads; only an administrator may ask, even if a policy were to differ.
func executorControl(caller api.Caller, raw json.RawMessage, mailbox string) (any, error) {
	if err := api.RequireAdmin(caller); err != nil {
		return nil, err
	}
	var in struct {
		Action string `json:"action"`
	}
	if err := strict(raw, &in); err != nil {
		return nil, err
	}
	if in.Action != "start" && in.Action != "stop" && in.Action != "purge" {
		return nil, api.BadRequest("executor_control_invalid")
	}
	if err := os.MkdirAll(mailbox, 0o700); err != nil {
		return nil, err
	}
	if info, err := os.Lstat(mailbox); err != nil || !info.IsDir() {
		return nil, errors.New("executor_control_directory_invalid")
	}
	if err := replaceFile(filepath.Join(mailbox, "control"), []byte(in.Action)); err != nil {
		return nil, err
	}
	return map[string]bool{"written": true}, nil
}

// The executor closes its socket while paused: the package is stopped, or an
// upgrade changed the pins and awaits administrator approval. A stopped
// package, and one that asked to start less than StartGrace ago, refuse with
// APP_STOPPED or APP_STARTING, as the root services do; only a paused
// executor answers its status.
func executorStatus(ctx context.Context, c ExecutorClient, mailbox string) (any, error) {
	var status json.RawMessage
	err := c.Call(ctx, "status", nil, nil, &status)
	var refusal *api.Error
	if errors.As(err, &refusal) && refusal.Code == CodeExecutorUnavailable {
		action, at := lifecycleRequest(mailbox)
		next := "Start the package."
		switch {
		case action == "stop":
			return nil, fail(CodeAppStopped)
		case action == "start" && time.Since(at) < StartGrace:
			return nil, fail(CodeAppStarting)
		case action == "start":
			next = "The package is running: an administrator approves background access again in Administrator setup; the executor log names the cause."
		}
		return map[string]any{"ready": false, "state": "paused", "reason": "executor_not_listening", "next": next}, nil
	}
	return status, err
}

// replaceFile writes name whole or not at all: the lifecycle never reads a
// partial request.
func replaceFile(name string, data []byte) error {
	f, err := os.CreateTemp(filepath.Dir(name), ".control-")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	_, err = f.Write(data)
	if err == nil {
		err = f.Sync()
	}
	if closeErr := f.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		return err
	}
	return os.Rename(f.Name(), name)
}
