package api

import (
	"context"
	"encoding/json"
	"log"
	"net/http"
	"sync"
)

// MaxBatchMethods bounds one authenticated request, including failed methods.
const MaxBatchMethods = 8

type BatchCall struct {
	Method string          `json:"method"`
	Params json.RawMessage `json:"params,omitempty"`
}
type BatchResult struct {
	Success bool        `json:"success"`
	Data    any         `json:"data,omitempty"`
	Error   *BatchError `json:"error,omitempty"`
}
type BatchError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
	Details any    `json:"details,omitempty"`
}

func BatchFailure(err error) BatchResult {
	e := asError(err)
	return BatchResult{Error: &BatchError{Code: e.Code, Message: e.Message, Details: errorDetails(err)}}
}
func BatchSuccess(data any) BatchResult { return BatchResult{Success: true, Data: data} }

// AuthorizedCall has passed this method's policy. Credentials stay in Request.
type AuthorizedCall struct {
	BatchCall
	Caller  Caller
	Request *http.Request
}

// BatchGroup executes independent App methods in one process. Results retain
// input order; execution order across different groups is unspecified.
type BatchGroup struct {
	Run func(context.Context, []AuthorizedCall) []BatchResult
}

func HandleRequestBatch(method string, h RequestHandler, group *BatchGroup) Route {
	r := HandleRequest(method, h)
	r.batch = group
	return r
}

type batchKey struct{}
type batchResources struct {
	mu     sync.Mutex
	values map[any]any
	close  []func()
	closed bool
}

// BatchResource owns transports for exactly one HTTP batch. It stores no
// account or privilege decisions. A nil return means this is a single call.
func BatchResource(ctx context.Context, key any, create func() (any, func())) any {
	b, _ := ctx.Value(batchKey{}).(*batchResources)
	if b == nil {
		return nil
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.closed || ctx.Err() != nil {
		return nil
	}
	if value, ok := b.values[key]; ok {
		return value
	}
	value, close := create()
	b.values[key] = value
	if close != nil {
		b.close = append(b.close, close)
	}
	return value
}

type verifiedBatch struct {
	identity Identity
	err      error
	next     Authenticator
	done     bool
	entry    string
}

func (a *verifiedBatch) EntryMode() string { return a.entry }

func (a *verifiedBatch) Authenticate(r *http.Request) (Identity, error) {
	if !a.done {
		a.identity, a.err = a.next.Authenticate(r)
		a.done = true
	}
	return a.identity, a.err
}

func (s *Server) serveBatch(w http.ResponseWriter, r *http.Request, calls []BatchCall) {
	resources := &batchResources{values: map[any]any{}}
	ctx := context.WithValue(r.Context(), batchKey{}, resources)
	r = r.WithContext(ctx)
	defer func() {
		resources.mu.Lock()
		resources.closed = true
		closers := append([]func(){}, resources.close...)
		resources.mu.Unlock()
		for i := len(closers) - 1; i >= 0; i-- {
			closers[i]()
		}
	}()

	// Only authorize decides whether authentication is required. Cache its
	// result for this request, including an all-public batch with an entry floor.
	requestServer := *s
	requestServer.auth = &verifiedBatch{next: s.auth, entry: s.entry}
	out := make([]BatchResult, len(calls))
	routes := make([]Route, len(calls))
	authorized := make([]AuthorizedCall, len(calls))
	ready := make([]bool, len(calls))
	for i, call := range calls {
		if len(call.Params) == 0 {
			call.Params = json.RawMessage(`{}`)
		}
		caller, route, err := requestServer.authorize(r, call.Method)
		if err != nil {
			out[i] = BatchFailure(err)
			continue
		}
		if route.streamHandler != nil || route.responseHandler != nil {
			out[i] = BatchFailure(BadRequest("method cannot be batched"))
			continue
		}
		authorized[i] = AuthorizedCall{call, caller, r}
		routes[i] = route
		ready[i] = true
	}
	for i := range calls {
		if !ready[i] {
			continue
		}
		if group := routes[i].batch; group != nil {
			var selected []AuthorizedCall
			var indexes []int
			for j := i; j < len(calls); j++ {
				if ready[j] && routes[j].batch == group {
					selected = append(selected, authorized[j])
					indexes = append(indexes, j)
					ready[j] = false
				}
			}
			results := runBatchGroup(ctx, group, selected)
			for j, index := range indexes {
				if j < len(results) {
					out[index] = results[j]
				} else {
					out[index] = BatchFailure(internalError)
				}
			}
			continue
		}
		out[i] = invokeBatch(ctx, routes[i], authorized[i])
		ready[i] = false
	}
	WriteData(w, map[string]any{"v": 1, "results": out})
}
func invokeBatch(ctx context.Context, route Route, call AuthorizedCall) (out BatchResult) {
	defer func() {
		if p := recover(); p != nil {
			log.Print("batch handler panic")
			out = BatchFailure(internalError)
		}
	}()
	if ctx.Err() != nil {
		return BatchFailure(ctx.Err())
	}
	data, err := route.Invoke(ctx, call.Caller, call.Request, call.Params)
	if err != nil {
		return BatchFailure(asRequestError(ctx, err))
	}
	// Isolate a response encoding failure to this method too.
	if _, err := json.Marshal(data); err != nil {
		return BatchFailure(internalError)
	}
	return BatchSuccess(data)
}
func runBatchGroup(ctx context.Context, g *BatchGroup, calls []AuthorizedCall) (out []BatchResult) {
	defer func() {
		if recover() != nil {
			log.Print("batch group panic")
			out = nil
		}
	}()
	return g.Run(ctx, calls)
}

func (r BatchResult) MarshalJSON() ([]byte, error) {
	if r.Success {
		return json.Marshal(struct {
			Success bool `json:"success"`
			Data    any  `json:"data"`
		}{true, r.Data})
	}
	return json.Marshal(struct {
		Success bool        `json:"success"`
		Error   *BatchError `json:"error"`
	}{false, r.Error})
}
