package delegate

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"github.com/synopackageland/syno-pkg-land-sdk/api"
	"github.com/synopackageland/syno-pkg-land-sdk/background"
	"github.com/synopackageland/syno-pkg-land-sdk/internal/runtimebudget"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"sync"
	"time"
)

// DownloadTimeout bounds the background transfer, independently of the CGI.
// Delegation expiry and revocation still bound the final authorized write.
const DownloadTimeout = background.JobTimeout

func downloadClient() *http.Client {
	return &http.Client{Timeout: DownloadTimeout, Transport: &http.Transport{Proxy: nil, DisableKeepAlives: true, DialContext: (&net.Dialer{Timeout: 5 * time.Second}).DialContext, ResponseHeaderTimeout: runtimebudget.Operation, MaxResponseHeaderBytes: 32 << 10, DisableCompression: true}, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
}
func (s *Service) download(parent context.Context, in DownloadInput, job JobContext) error {
	if !validDownload(in) {
		return fail(CodeInvalidRequest)
	}
	ctx, cancel := context.WithTimeout(parent, DownloadTimeout)
	defer cancel()
	request, e := http.NewRequestWithContext(ctx, "GET", in.URL, nil)
	if e != nil {
		return fail(CodeInvalidRequest)
	}
	client := s.HTTP
	if client == nil {
		client = downloadClient()
	}
	response, e := client.Do(request)
	if e != nil {
		if ctx.Err() != nil {
			return fail("timeout")
		}
		return fail("download_failed")
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return fail("download_failed")
	}
	if response.ContentLength > MaxDownloadBytes {
		return fail("limit-exceeded")
	}
	if e = os.MkdirAll(job.StagingDir, 0700); e != nil {
		return fail("STAGING_UNAVAILABLE")
	}
	info, e := os.Lstat(job.StagingDir)
	if e != nil || !info.IsDir() || info.Mode().Perm()&0077 != 0 {
		return fail("STAGING_UNAVAILABLE")
	}
	tmp, e := os.CreateTemp(job.StagingDir, "download-")
	if e != nil {
		return fail("STAGING_UNAVAILABLE")
	}
	defer os.Remove(tmp.Name())
	defer tmp.Close()
	n, e := io.Copy(tmp, io.LimitReader(contextReader{ctx, response.Body}, MaxDownloadBytes+1))
	if e != nil {
		return fail("download_failed")
	}
	if n > MaxDownloadBytes {
		return fail("limit-exceeded")
	}
	if _, e = tmp.Seek(0, io.SeekStart); e != nil {
		return fail("STAGING_UNAVAILABLE")
	}
	return job.Files().Write(parent, in.Dest, in.Name, tmp, n)
}
func (s *Service) Recover(ctx context.Context) error {
	if e := s.Store.Recover(ctx); e != nil {
		return e
	}
	return s.Sweep(ctx)
}

// RunOnce processes either the App's sha256-v1 worker or the fixed delegated
// download worker. No registry or operation ID comes from HTTP input.
func (s *Service) RunOnce(parent context.Context) error {
	job, e := s.Store.Claim(parent)
	if e != nil || job == nil {
		return e
	}
	var outcome background.Outcome
	switch job.Worker {
	case background.WorkerSHA256V1:
		s.runner().Process(parent, job)
		finish, done := context.WithTimeout(context.Background(), 20*time.Second)
		defer done()
		return s.Sweep(finish)
	case background.WorkerDownloadV1:
		outcome = s.runDownload(parent, job)
	default:
		outcome = background.Outcome{State: background.StateFailed, ErrorCode: "unsupported"}
	}
	finish, done := context.WithTimeout(context.Background(), 20*time.Second)
	defer done()
	if e = s.Store.Finish(finish, job.ID, outcome); e != nil {
		return e
	}
	return s.Sweep(finish)
}

func (s *Service) runner() *background.Runner {
	work := s.Work
	if work == nil {
		work = background.SHA256V1
	}
	return &background.Runner{Store: s.Store, Work: work, Timeout: background.JobTimeout, CancelPoll: 100 * time.Millisecond, ProgressEvery: time.Second}
}

// runDownload runs a delegated download. Its executor work cannot pause, so a
// stop interrupts it.
func (s *Service) runDownload(parent context.Context, job *background.Job) background.Outcome {
	ctx, cancel := context.WithCancel(parent)
	defer cancel()
	watched := make(chan struct{})
	go func() {
		defer close(watched)
		ticker := time.NewTicker(100 * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				state, e := s.Store.Get(ctx, job.ID)
				if e == nil && (state.CancelRequested || state.State != background.StateRunning) {
					cancel()
					return
				}
			}
		}
	}()
	outcome := background.Outcome{State: background.StateSucceeded}
	input, e := s.Store.OpenInput(job.ID)
	if e == nil {
		var in DownloadInput
		data, readErr := io.ReadAll(io.LimitReader(input, background.MaxInputBytes+1))
		input.Close()
		if readErr != nil || json.Unmarshal(data, &in) != nil {
			e = fail(CodeInvalidRequest)
		} else {
			// Verify immutable input before contacting any remote host or executor.
			hash, e2 := background.HashInput(ctx, bytes.NewReader(data))
			if e2 != nil || hash.SHA256 != job.InputDigest || hash.Bytes != job.InputBytes {
				e = fail("input-changed")
			} else {
				binding, e2 := s.Store.Delegation(ctx, job.ID)
				e = e2
				if e == nil {
					e = s.download(ctx, in, JobContext{Client: s.Client, JobID: job.ID, DelegationID: binding.DelegationID, StagingDir: s.StagingDir, callExecutor: func(ctx context.Context, op string, params map[string]any, out any) error {
						return s.callWaitingExecutor(ctx, job.ID, binding.ExpiresAt, op, params, out)
					}})
				}
			}
		}
	}
	if e != nil {
		outcome = background.Outcome{State: background.StateFailed, ErrorCode: errorCode(e)}
	}
	if parent.Err() != nil {
		outcome = background.Outcome{State: background.StateInterrupted, ErrorCode: "worker-stopped"}
	}
	cancel()
	<-watched
	return outcome
}
func (s *Service) Run(ctx context.Context, info background.WorkerInfo) error {
	if e := background.RefuseRoot(info.UID); e != nil {
		return e
	}
	if e := s.Store.Recover(ctx); e != nil {
		return e
	}
	if e := s.Store.Heartbeat(ctx, info); e != nil {
		return e
	}
	ctx, stop := context.WithCancel(ctx)
	var heartbeat sync.WaitGroup
	heartbeat.Add(1)
	go func() {
		defer heartbeat.Done()
		beat := time.NewTicker(5 * time.Second)
		defer beat.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-beat.C:
				if e := s.Store.Heartbeat(ctx, info); e != nil && ctx.Err() == nil {
					log.Print("worker heartbeat unavailable")
				}
			}
		}
	}()
	defer func() { stop(); heartbeat.Wait(); s.Store.ClearHeartbeat(info) }()
	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()
	for ctx.Err() == nil {
		if e := s.Sweep(ctx); e != nil {
			log.Printf("delegation cleanup pending: %s", errorCode(e))
		}
		if e := s.Store.Tick(ctx); e != nil {
			log.Print("schedule tick unavailable")
		}
		if e := s.RunOnce(ctx); e != nil && ctx.Err() == nil {
			log.Printf("worker operation: %s", errorCode(e))
		}
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
		}
	}
	return nil
}

func errorCode(e error) string {
	var a *api.Error
	if errors.As(e, &a) {
		return a.Code
	}
	return "unavailable"
}
