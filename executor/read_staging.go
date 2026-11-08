package main

// Small read-only raw bodies may use tmpfs. Both one body and all unconsumed
// tmpfs handoffs are bounded by this cap; larger bodies use persistent staging.
const maxMemoryReadStaging = 1 << 20
const maxMemoryReadFiles = 256

// keepMemoryRaw attempts an optional handoff without consuming body. A failed
// create, write or sync is cleaned up so the caller can persist the same bytes
// without repeating the DSM request.
func (e *Executor) keepMemoryRaw(body []byte) (string, bool) {
	if e.ReadStaging == nil || len(body) > maxMemoryReadStaging {
		return "", false
	}
	e.rootMu.Lock()
	defer e.rootMu.Unlock()
	staging, ok := e.ReadStaging.(interface{ Available() (int64, error) })
	if !ok {
		return "", false
	}
	free, err := staging.Available()
	if err != nil || free < int64(len(body)) {
		return "", false
	}
	out := &rootOutput{limit: int64(len(body)), staging: e.ReadStaging, stagingLimit: maxMemoryReadStaging}
	defer out.discard()
	// spill and keep run under the same lock as other tmpfs handoffs, so the
	// aggregate cap includes every completed file before another is created.
	out.mem.Write(body)
	out.n = int64(len(body))
	name, _, err := out.keep()
	if err != nil {
		return "", false
	}
	return name, true
}
