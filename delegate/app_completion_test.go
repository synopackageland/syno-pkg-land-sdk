package delegate

import (
	"bytes"
	"context"
	"encoding/csv"
	"os"
	"path/filepath"
	"testing"
)

// App-side completion producers against the production executor: a daemon
// hook hands over a file its engine finished, and an App-owned exporter writes
// a CSV for a schedule slot. Neither carries a session or replays its work.

func daemonComplete(ctx context.Context, s *Service, grant, task, staging, name string, size int64) (OperationAdmission, error) {
	return s.MoveFromStaging(ctx, MoveParams{DelegationID: grant, JobID: task, Staging: staging, Name: name, Size: size})
}

func exportCSV(ctx context.Context, s *Service, grant, schedule, slot string, rows [][]string) (OperationAdmission, error) {
	var data bytes.Buffer
	if err := csv.NewWriter(&data).WriteAll(rows); err != nil {
		return OperationAdmission{}, err
	}
	job := schedule + "-" + slot
	name := job + ".csv"
	if err := os.WriteFile(filepath.Join(s.StagingDir, name), data.Bytes(), 0600); err != nil {
		return OperationAdmission{}, err
	}
	return s.MoveFromStaging(ctx, MoveParams{DelegationID: grant, JobID: job, Staging: name, Name: name, Size: int64(data.Len())})
}

func TestAppCompletionProducersUseOneGrantWithoutReplay(t *testing.T) {
	for _, owner := range []string{"admin", "alice"} {
		t.Run(owner, func(t *testing.T) {
			f := newContractFixture(t, contractServer)
			f.s.LookupUID = func(name string) string {
				return map[string]string{"alice": "1205", "bob": "1206", "admin": "1207"}[name]
			}
			grant, err := f.s.SelectDestination(context.Background(), destinationCaller(t, owner), contractRequest(owner), SelectDestinationParams{Key: "save", Path: "/home"})
			if err != nil {
				t.Fatal(err)
			}
			if err = os.MkdirAll(f.s.StagingDir, 0700); err != nil {
				t.Fatal(err)
			}
			var input MoveParams
			var admission OperationAdmission
			if owner == "admin" {
				data := []byte("completed engine bytes")
				if err = os.WriteFile(filepath.Join(f.s.StagingDir, "engine.bin"), data, 0600); err != nil {
					t.Fatal(err)
				}
				input = MoveParams{DelegationID: grant.ID, JobID: "download-task", Staging: "engine.bin", Name: "download.bin", Size: int64(len(data))}
				admission, err = daemonComplete(context.Background(), f.s, grant.ID, input.JobID, input.Staging, input.Name, input.Size)
			} else {
				input = MoveParams{DelegationID: grant.ID, JobID: "daily-slot-1", Staging: "daily-slot-1.csv", Name: "daily-slot-1.csv", Size: int64(len("name,count\n\"A, B\",2\n"))}
				admission, err = exportCSV(context.Background(), f.s, grant.ID, "daily", "slot-1", [][]string{{"name", "count"}, {"A, B", "2"}})
			}
			if err != nil || admission.JobID != input.JobID {
				t.Fatal(admission, err)
			}
			out := f.wait(t, input, "committed")
			if out.Receipt == nil || out.Receipt.Path != "/home/"+input.Name || out.Receipt.Size != input.Size {
				t.Fatal(out)
			}
			if _, err = os.Stat(filepath.Join(f.s.StagingDir, input.Staging)); !os.IsNotExist(err) {
				t.Fatal("source cleanup", err)
			}
			f.attempts(t, input.JobID, 1)
			// The operations mirror shows the job to its owner only.
			view := mirrorRPC(t, f.s, owner, `{}`, "contract-token")
			if ops, _ := view["data"].(map[string]any)["operations"].([]any); view["success"] != true || len(ops) != 1 || ops[0].(map[string]any)["job_id"] != input.JobID {
				t.Fatal(view)
			}
			foreign := mirrorRPC(t, f.s, "bob", `{}`, "contract-token")
			if ops, _ := foreign["data"].(map[string]any)["operations"].([]any); foreign["success"] != true || len(ops) != 0 {
				t.Fatal(foreign)
			}
			ref := OperationRef{grant.ID, input.JobID}
			f.rpc(t, "bob", "delegate.operations.get", ref, false)
			f.rpc(t, "bob", "delegate.operations.cancel", ref, false)
			f.rpc(t, owner, "delegate.operations.get", ref, true)
			// After an executor restart the same result is read back; the producer never runs again.
			f.restart(t)
			if again := f.wait(t, input, "committed"); again.Receipt == nil || again.Receipt.Path != out.Receipt.Path {
				t.Fatal(again)
			}
			f.attempts(t, input.JobID, 1)
			if err = f.s.Sweep(context.Background()); err != nil {
				t.Fatal(err)
			}
			// The permanent grant admits a distinct completion.
			next := f.stage(t, grant.ID, "next-task")
			if _, err = f.s.MoveFromStaging(context.Background(), next); err != nil {
				t.Fatal(err)
			}
			f.wait(t, next, "committed")
			f.attempts(t, input.JobID, 1)
		})
	}
}
