package delegate

import (
	"context"
	"encoding/json"
	"github.com/synopackageland/syno-pkg-land-sdk/background"
	"golang.org/x/sys/unix"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

func stagedService(t *testing.T) (*Service, MoveParams) {
	t.Helper()
	s := service(t, nil)
	s.Lifetime = "until-revoked"
	s.Operations = []string{"files.moveFromStaging"}
	if err := os.MkdirAll(filepath.Join(s.StagingDir, "exports"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(s.StagingDir, "exports", "a"), []byte("abc"), 0600); err != nil {
		t.Fatal(err)
	}
	return s, MoveParams{DelegationID: "grant", JobID: "export-1", Staging: "exports/a", Name: "a", Size: 3}
}
func TestMoveFromStagingNoReplayAndPackageCleanupOnlyAfterReceipt(t *testing.T) {
	s, in := stagedService(t)
	moves := 0
	state := "queued"
	s.Client = fakeExecutor(func(_ context.Context, op string, auth map[string]string, p, out any) error {
		if len(auth) != 0 {
			t.Fatal("background operation retained session")
		}
		switch op {
		case "files.moveFromStaging":
			moves++
			return json.Unmarshal([]byte(`{"job_id":"export-1","state":"queued"}`), out)
		case "files.operationStatus":
			result := OperationResult{JobID: in.JobID, State: state}
			if state == "committed" {
				result.Receipt = &Receipt{Path: "/home/a", Size: 3}
			}
			data, _ := json.Marshal(result)
			return json.Unmarshal(data, out)
		default:
			t.Fatalf("unexpected operation %s", op)
			return nil
		}
	})
	out, err := s.MoveFromStaging(context.Background(), in)
	if err != nil || out.State != "queued" || moves != 1 {
		t.Fatal(out, err, moves)
	}
	if _, err = os.Stat(filepath.Join(s.StagingDir, in.Staging)); err != nil {
		t.Fatal("removed before commit")
	}
	// A CGI/worker restart retains only a reference and exact source identity.
	s.Store = background.NewStore(filepath.Dir(s.StagingDir) + "/background")
	if _, err = s.MoveFromStaging(context.Background(), in); err != nil || moves != 1 {
		t.Fatal("replayed admission", err, moves)
	}
	changed := in
	changed.Name = "b"
	if _, err = s.MoveFromStaging(context.Background(), changed); err == nil || moves != 1 {
		t.Fatal("job input changed")
	}
	state = "committed"
	result, err := s.OperationStatus(context.Background(), nil, OperationRef{DelegationID: in.DelegationID, JobID: in.JobID})
	if err != nil || result.State != "committed" || result.Receipt.Path != "/home/a" {
		t.Fatal(result, err)
	}
	if _, err = os.Stat(filepath.Join(s.StagingDir, in.Staging)); !os.IsNotExist(err) {
		t.Fatal("committed source not cleaned", err)
	}
	if _, err = s.OperationStatus(context.Background(), nil, OperationRef{DelegationID: in.DelegationID, JobID: in.JobID}); err != nil {
		t.Fatal("cleanup not idempotent", err)
	}
	if err = s.Sweep(context.Background()); err != nil {
		t.Fatal(err)
	}
}
func TestAmbiguousAdmissionQueriesOriginalJobAndNeverMovesAgain(t *testing.T) {
	s, in := stagedService(t)
	moves, queries := 0, 0
	s.Client = fakeExecutor(func(_ context.Context, op string, _ map[string]string, p, out any) error {
		if op == "files.moveFromStaging" {
			moves++
			return fail("EXECUTOR_TIMEOUT")
		}
		if op != "files.operationStatus" {
			t.Fatal(op)
		}
		queries++
		b, _ := json.Marshal(p)
		var ref OperationRef
		json.Unmarshal(b, &ref)
		if ref.JobID != in.JobID || ref.DelegationID != in.DelegationID {
			t.Fatal("queried new job")
		}
		return json.Unmarshal([]byte(`{"job_id":"export-1","state":"uncertain"}`), out)
	})
	for i := 0; i < 2; i++ {
		out, err := s.MoveFromStaging(context.Background(), in)
		if err != nil || out.State != "uncertain" {
			t.Fatal(out, err)
		}
	}
	if moves != 1 || queries != 2 {
		t.Fatal(moves, queries)
	}
	if _, err := os.Stat(filepath.Join(s.StagingDir, in.Staging)); err != nil {
		t.Fatal("uncertain source removed")
	}
}
func TestMoveRejectsPathAttacksAndBoundsBeforeAdmission(t *testing.T) {
	for _, attack := range []string{"traversal", "absolute", "symlink", "parent-link", "hardlink", "size", "root-name", "fifo", "size-mismatch"} {
		t.Run(attack, func(t *testing.T) {
			s, in := stagedService(t)
			source := filepath.Join(s.StagingDir, in.Staging)
			switch attack {
			case "traversal":
				in.Staging = "../a"
			case "absolute":
				in.Staging = source
			case "symlink":
				os.Remove(source)
				os.Symlink("missing", source)
			case "parent-link":
				os.Rename(filepath.Join(s.StagingDir, "exports"), filepath.Join(s.StagingDir, "renamed"))
				os.Symlink("renamed", filepath.Join(s.StagingDir, "exports"))
			case "hardlink":
				os.Link(source, filepath.Join(s.StagingDir, "b"))
			case "size":
				in.Size = 16<<30 + 1
			case "root-name":
				in.Name = "../a"
			case "fifo":
				os.Remove(source)
				if err := unix.Mkfifo(source, 0600); err != nil {
					t.Fatal(err)
				}
			case "size-mismatch":
				in.Size = 2
			}
			s.Client = fakeExecutor(func(context.Context, string, map[string]string, any, any) error {
				t.Fatal("invalid source admitted")
				return nil
			})
			if _, err := s.MoveFromStaging(context.Background(), in); err == nil {
				t.Fatal("unsafe move accepted")
			}
		})
	}
}
func TestCommittedCleanupFailureDoesNotDowngradeOrDeleteReplacement(t *testing.T) {
	s, in := stagedService(t)
	s.Client = fakeExecutor(func(_ context.Context, op string, _ map[string]string, _ any, out any) error {
		if op == "files.moveFromStaging" {
			return json.Unmarshal([]byte(`{"job_id":"export-1","state":"queued"}`), out)
		}
		return json.Unmarshal([]byte(`{"job_id":"export-1","state":"committed","receipt":{"path":"/home/a","size":3}}`), out)
	})
	if _, err := s.MoveFromStaging(context.Background(), in); err != nil {
		t.Fatal(err)
	}
	// Keep original inode alive so this is a definite replacement, not reuse.
	source := filepath.Join(s.StagingDir, in.Staging)
	os.Rename(source, source+".old")
	os.WriteFile(source, []byte("new"), 0600)
	out, err := s.OperationStatus(context.Background(), nil, OperationRef{DelegationID: in.DelegationID, JobID: in.JobID})
	if err != nil || out.State != "committed" {
		t.Fatal("commit downgraded", out, err)
	}
	if bytes, err := os.ReadFile(source); err != nil || string(bytes) != "new" {
		t.Fatal("replacement deleted")
	}
}
func TestConcurrentSameJobAdmitsOnce(t *testing.T) {
	s, in := stagedService(t)
	var mu sync.Mutex
	moves := 0
	s.Client = fakeExecutor(func(_ context.Context, op string, _ map[string]string, _ any, out any) error {
		if op == "files.moveFromStaging" {
			mu.Lock()
			moves++
			mu.Unlock()
		}
		return json.Unmarshal([]byte(`{"job_id":"export-1","state":"queued"}`), out)
	})
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := s.MoveFromStaging(context.Background(), in)
			if err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	if moves != 1 {
		t.Fatal(moves)
	}
}

func TestCanceledOperationRetainsSourceAndReusableGrant(t *testing.T) {
	s, in := stagedService(t)
	s.Client = fakeExecutor(func(_ context.Context, op string, auth map[string]string, _ any, out any) error {
		switch op {
		case "files.moveFromStaging":
			return json.Unmarshal([]byte(`{"job_id":"export-1","state":"queued"}`), out)
		case "files.cancelOperation":
			if auth["dsm_cookie"] != "owner-cookie" {
				t.Fatal("owner session not forwarded")
			}
			return json.Unmarshal([]byte(`{"job_id":"export-1","state":"canceled"}`), out)
		default:
			t.Fatalf("unexpected operation/revocation %s", op)
			return nil
		}
	})
	if _, err := s.MoveFromStaging(context.Background(), in); err != nil {
		t.Fatal(err)
	}
	out, err := s.CancelOperation(context.Background(), map[string]string{"dsm_cookie": "owner-cookie"}, OperationRef{in.DelegationID, in.JobID})
	if err != nil || out.State != "canceled" {
		t.Fatal(out, err)
	}
	if _, err = os.Stat(filepath.Join(s.StagingDir, in.Staging)); err != nil {
		t.Fatal("canceled source removed")
	}
}

func TestOperationReceiptAndReferenceMustComeFromExecutor(t *testing.T) {
	for _, reply := range []string{
		`{"job_id":"different","state":"committed","receipt":{"path":"/home/a","size":3}}`,
		`{"job_id":"export-1","state":"succeeded"}`,
		`{"job_id":"export-1","state":"committed"}`,
		`{"job_id":"export-1","state":"queued","receipt":{"path":"/home/a","size":3}}`,
		`{"job_id":"export-1","state":"committed","receipt":{"path":"/home/a","size":4}}`,
		`{"job_id":"export-1","state":"committed","receipt":{"path":"/home/b","size":3}}`,
	} {
		t.Run(reply, func(t *testing.T) {
			s, in := stagedService(t)
			s.Client = fakeExecutor(func(_ context.Context, op string, _ map[string]string, _ any, out any) error {
				if op == "files.moveFromStaging" {
					return json.Unmarshal([]byte(`{"job_id":"export-1","state":"queued"}`), out)
				}
				return json.Unmarshal([]byte(reply), out)
			})
			if _, err := s.MoveFromStaging(context.Background(), in); err != nil {
				t.Fatal(err)
			}
			if _, err := s.OperationStatus(context.Background(), nil, OperationRef{in.DelegationID, in.JobID}); err == nil {
				t.Fatal("invalid result accepted")
			}
			if _, err := os.Stat(filepath.Join(s.StagingDir, in.Staging)); err != nil {
				t.Fatal("invalid receipt cleaned source")
			}
		})
	}
}

func TestSourceAndParentReplacementNeverCleanedAfterCommit(t *testing.T) {
	for _, kind := range []string{"parent", "root", "contents"} {
		t.Run(kind, func(t *testing.T) {
			s, in := stagedService(t)
			s.Client = fakeExecutor(func(_ context.Context, op string, _ map[string]string, _ any, out any) error {
				if op == "files.moveFromStaging" {
					return json.Unmarshal([]byte(`{"job_id":"export-1","state":"queued"}`), out)
				}
				return json.Unmarshal([]byte(`{"job_id":"export-1","state":"committed","receipt":{"path":"/home/a","size":3}}`), out)
			})
			if _, err := s.MoveFromStaging(context.Background(), in); err != nil {
				t.Fatal(err)
			}
			switch kind {
			case "parent":
				os.Rename(filepath.Join(s.StagingDir, "exports"), filepath.Join(s.StagingDir, "old"))
				os.Mkdir(filepath.Join(s.StagingDir, "exports"), 0700)
				os.WriteFile(filepath.Join(s.StagingDir, in.Staging), []byte("new"), 0600)
			case "root":
				os.Rename(s.StagingDir, s.StagingDir+".old")
				os.MkdirAll(filepath.Join(s.StagingDir, "exports"), 0700)
				os.WriteFile(filepath.Join(s.StagingDir, in.Staging), []byte("new"), 0600)
			case "contents":
				os.WriteFile(filepath.Join(s.StagingDir, in.Staging), []byte("new content"), 0600)
			}
			out, err := s.OperationStatus(context.Background(), nil, OperationRef{in.DelegationID, in.JobID})
			if err != nil || out.State != "committed" {
				t.Fatal(out, err)
			}
			if _, err = os.Stat(filepath.Join(s.StagingDir, in.Staging)); err != nil {
				t.Fatal("changed object removed")
			}
		})
	}
}

func TestFrozenV1MoveVectorUsesExistingUnixClient(t *testing.T) {
	s, in := stagedService(t)
	in.JobID = "export-20261009"
	in.DelegationID = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	requests := 0
	s.Client = executor(t, func(request Request) string {
		requests++
		var got MoveParams
		if json.Unmarshal(request.Params, &got) != nil || request.V != 1 || request.Op != "files.moveFromStaging" || len(request.Auth) != 0 || got != in {
			t.Errorf("unexpected v1 request")
		}
		return `{"ok":true,"result":{"job_id":"export-20261009","state":"queued"}}` + "\n"
	})
	out, err := s.MoveFromStaging(context.Background(), in)
	if err != nil || out.JobID != in.JobID || out.State != "queued" || requests != 1 {
		t.Fatal(out, err, requests)
	}
}

func TestRepeatedCommittedMoveReturnsOnlyAdmissionReference(t *testing.T) {
	s, in := stagedService(t)
	s.Client = fakeExecutor(func(_ context.Context, op string, _ map[string]string, _ any, out any) error {
		if op == "files.moveFromStaging" {
			return json.Unmarshal([]byte(`{"job_id":"export-1","state":"queued"}`), out)
		}
		return json.Unmarshal([]byte(`{"job_id":"export-1","state":"committed","receipt":{"path":"/home/a","size":3}}`), out)
	})
	if _, err := s.MoveFromStaging(context.Background(), in); err != nil {
		t.Fatal(err)
	}
	out, err := s.MoveFromStaging(context.Background(), in)
	if err != nil || out.State != "committed" {
		t.Fatal(out, err)
	}
	data, _ := json.Marshal(out)
	if string(data) != `{"job_id":"export-1","state":"committed"}` {
		t.Fatal("admission leaked status DTO", string(data))
	}
}

func TestNewSemanticRefusalsUseFrozenCodes(t *testing.T) {
	s, in := stagedService(t)
	in.Size = MaxMoveBytes + 1
	_, err := s.MoveFromStaging(context.Background(), in)
	code(t, err, "invalid-size")
	s.SessionFiles = &fakeSession{}
	s.Destinations = []string{"save"}
	_, err = s.SelectDestination(context.Background(), destinationCaller(t, "alice"), destinationRequest(), SelectDestinationParams{Key: "other", Path: "/home"})
	code(t, err, "invalid-destination")
}

func TestMoveIntoSubfoldersCleansSourceOnlyForTheFullReceipt(t *testing.T) {
	for receipt, ok := range map[string]bool{"/home/Album/CD1/a": true, "/home/a": false, "/home/xAlbum/CD1/a": false, "/home/CD1/a": false} {
		t.Run(receipt, func(t *testing.T) {
			s, in := stagedService(t)
			in.Name = "Album/CD1/a"
			s.Client = fakeExecutor(func(_ context.Context, op string, _ map[string]string, params any, out any) error {
				if op == "files.moveFromStaging" {
					if params.(MoveParams).Name != "Album/CD1/a" {
						t.Fatal("subfolder name not forwarded", params)
					}
					return json.Unmarshal([]byte(`{"job_id":"export-1","state":"queued"}`), out)
				}
				return json.Unmarshal([]byte(`{"job_id":"export-1","state":"committed","receipt":{"path":"`+receipt+`","size":3}}`), out)
			})
			if _, err := s.MoveFromStaging(context.Background(), in); err != nil {
				t.Fatal(err)
			}
			_, err := s.OperationStatus(context.Background(), nil, OperationRef{in.DelegationID, in.JobID})
			_, stat := os.Stat(filepath.Join(s.StagingDir, in.Staging))
			if ok != (err == nil) || ok != os.IsNotExist(stat) {
				t.Fatal(err, stat)
			}
		})
	}
}

func TestMoveRefusesMalformedSubfolderNamesBeforeAdmission(t *testing.T) {
	for _, name := range []string{"/Album/a", "Album//a", "Album/../a", "Album/./a", "Album/a/", "Album/\x00/a", strings.Repeat("abcdefgh/", 114) + "a"} {
		s, in := stagedService(t)
		in.Name = name
		s.Client = fakeExecutor(func(context.Context, string, map[string]string, any, any) error {
			t.Fatal("malformed name admitted", name)
			return nil
		})
		if _, err := s.MoveFromStaging(context.Background(), in); err == nil {
			t.Fatal("accepted", name)
		}
	}
}

func TestMoveForwardsConflictPolicyAndAcceptsRenamedReceipt(t *testing.T) {
	for _, tc := range []struct {
		conflict, receipt string
		ok                bool
	}{
		{"rename", "/home/Album/a (2)", true},
		{"rename", "/home/Album/a", true},
		{"rename", "/home/Album/b (2)", false},
		{"rename", "/home/Other/a (2)", false},
		{"overwrite", "/home/Album/a", true},
		{"overwrite", "/home/Album/a (1)", false},
		{"", "/home/Album/a (1)", false},
	} {
		t.Run(tc.conflict+tc.receipt, func(t *testing.T) {
			s, in := stagedService(t)
			in.Name, in.Conflict = "Album/a", tc.conflict
			s.Client = fakeExecutor(func(_ context.Context, op string, _ map[string]string, params any, out any) error {
				if op == "files.moveFromStaging" {
					b, _ := json.Marshal(params)
					var wire map[string]any
					json.Unmarshal(b, &wire)
					if got, has := wire["conflict"]; (tc.conflict == "") == has || (has && got != tc.conflict) {
						t.Fatal("conflict policy not forwarded as given", string(b))
					}
					return json.Unmarshal([]byte(`{"job_id":"export-1","state":"queued"}`), out)
				}
				return json.Unmarshal([]byte(`{"job_id":"export-1","state":"committed","receipt":{"path":"`+tc.receipt+`","size":3}}`), out)
			})
			if _, err := s.MoveFromStaging(context.Background(), in); err != nil {
				t.Fatal(err)
			}
			if _, err := s.OperationStatus(context.Background(), nil, OperationRef{in.DelegationID, in.JobID}); tc.ok != (err == nil) {
				t.Fatal(err)
			}
		})
	}
}

func TestMoveRefusesUnknownConflictPolicy(t *testing.T) {
	s, in := stagedService(t)
	in.Conflict = "replace"
	s.Client = fakeExecutor(func(context.Context, string, map[string]string, any, any) error {
		t.Fatal("unknown policy admitted")
		return nil
	})
	if _, err := s.MoveFromStaging(context.Background(), in); err == nil {
		t.Fatal("accepted")
	}
}
