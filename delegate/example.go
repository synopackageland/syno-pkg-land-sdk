package delegate

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/synopackageland/syno-pkg-land-sdk/api"
	"github.com/synopackageland/syno-pkg-land-sdk/background"
	"golang.org/x/sys/unix"
	"net/http"
	"os"
	"path"
)

// ExportExample is a small producer: capture the caller-bound destination,
// produce in package-private staging, then use the existing completion hook.
// A repeated job ID repeats the original request: MoveFromStaging returns the
// original operation and admits only one the executor does not know.
func (s *Service) ExportExample(ctx context.Context, caller api.Caller, request *http.Request, key, jobID string) (OperationRef, error) {
	ref := OperationRef{JobID: jobID}
	if !operationToken.MatchString(jobID) {
		return ref, fail(CodeInvalidRequest)
	}
	if s.Store == nil || background.RefuseRoot(os.Geteuid()) != nil {
		return ref, fail("STAGING_UNAVAILABLE")
	}
	selected, err := s.Destination(ctx, caller, request, key)
	if err != nil {
		return ref, err
	}
	if selected.Status != "active" || selected.Destination == nil {
		return ref, fail(CodeInvalidDestination)
	}
	ref.DelegationID = selected.Destination.ID
	binding, err := s.Store.MoveBinding(ctx, jobID)
	if err == nil {
		if binding.DelegationID != ref.DelegationID {
			return ref, fail(CodeOperationConflict)
		}
		auth, err := api.DSMSession(request)
		if err != nil {
			return ref, err
		}
		var original MoveParams
		if json.Unmarshal(binding.Params, &original) != nil {
			return ref, fail(CodeOperationConflict)
		}
		_, err = s.admitStaging(ctx, auth, original)
		return ref, err
	}
	if !errors.Is(err, os.ErrNotExist) {
		return ref, jobError(err)
	}
	if err = os.MkdirAll(s.StagingDir, 0700); err != nil {
		return ref, fail("STAGING_UNAVAILABLE")
	}
	name := "sdk-example-" + jobID + ".json"
	parent, _, err := stagingParent(s.StagingDir, name)
	if err != nil {
		return ref, err
	}
	defer unix.Close(parent)
	fd, err := unix.Openat(parent, path.Base(name), unix.O_WRONLY|unix.O_CREAT|unix.O_EXCL|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0600)
	if err != nil {
		return ref, fail(CodeOperationConflict)
	}
	file := os.NewFile(uintptr(fd), name)
	contents, _ := json.Marshal(map[string]string{"example": "sdk-example", "job_id": jobID})
	if _, err = file.Write(contents); err == nil {
		err = file.Sync()
	}
	closeErr := file.Close()
	if err != nil || closeErr != nil {
		return ref, fail("STAGING_UNAVAILABLE")
	}
	if err = unix.Fsync(parent); err != nil {
		return ref, fail("STAGING_UNAVAILABLE")
	}
	_, err = s.MoveFromStaging(ctx, MoveParams{DelegationID: ref.DelegationID, JobID: jobID, Staging: name, Name: name, Size: int64(len(contents))})
	return ref, err
}
