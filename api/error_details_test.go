package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"testing"
)

func TestStructuredErrorsSurviveSingleAndBatchResponses(t *testing.T) {
	err := fmt.Errorf("operation: %w", WithDetails(&Error{Code: "shared_folder_permission", Message: "grant access"}, map[string]string{"account": "ExampleApp", "share": "Media", "required": "write"}))
	for _, batch := range []bool{false, true} {
		var raw []byte
		if batch {
			raw, _ = json.Marshal(BatchFailure(asRequestError(context.Background(), err)))
		} else {
			w := httptest.NewRecorder()
			writeError(w, asRequestError(context.Background(), err))
			raw = w.Body.Bytes()
		}
		var got struct {
			Error struct {
				Code    string            `json:"code"`
				Details map[string]string `json:"details"`
			} `json:"error"`
		}
		if json.Unmarshal(raw, &got) != nil || got.Error.Code != "shared_folder_permission" || got.Error.Details["account"] != "ExampleApp" || got.Error.Details["share"] != "Media" || got.Error.Details["required"] != "write" {
			t.Fatalf("batch=%v: %s", batch, raw)
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	result := BatchFailure(asRequestError(ctx, err))
	if result.Error.Code != "request_canceled" || result.Error.Details != nil {
		t.Fatalf("cancellation must replace operation details: %+v", result)
	}
}

func TestSharedFolderStatusUsesAuthenticatedSDKPolicy(t *testing.T) {
	for _, auth := range []*fakeAuth{{user: "viewer"}, {err: ErrNoSession}} {
		calls := 0
		srv := newTestServer(t, auth, Handle("sharedfolder.status", func(context.Context, Caller, json.RawMessage) (any, error) {
			calls++
			return map[string]string{"account": "ExampleApp"}, nil
		}))
		_, reply := post(t, srv, `{"method":"sharedfolder.status","params":{}}`)
		if auth.err == nil && (!reply.Success || calls != 1) {
			t.Fatalf("signed-in user: %+v, %d calls", reply, calls)
		}
		if auth.err != nil && (reply.Success || calls != 0) {
			t.Fatalf("anonymous: %+v, %d calls", reply, calls)
		}
	}
}
