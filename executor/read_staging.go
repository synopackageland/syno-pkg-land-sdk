package main

// A private tmpfs spool never grows with unconsumed downloads indefinitely.
// The protocol's transfer limit is separate from available staging capacity.
const maxMemoryReadStaging int64 = maxWebAPIStaging
const maxMemoryReadFiles = 256

func (e *Executor) reserveReadStaging(limit int64) (func(), error) {
	e.rootMu.Lock()
	defer e.rootMu.Unlock()
	staging, ok := e.ReadStaging.(interface{ Available() (int64, error) })
	if !ok {
		return nil, errRootStaging
	}
	free, err := staging.Available()
	if err != nil {
		return nil, err
	}
	if free < e.rawReserved+limit {
		return nil, errRawSpace
	}
	e.rawReserved += limit
	return func() {
		e.rootMu.Lock()
		e.rawReserved -= limit
		e.rootMu.Unlock()
	}, nil
}
