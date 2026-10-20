package background

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"unicode/utf8"

	"github.com/synopackageland/syno-pkg-land-sdk/jsonlimits"
)

const (
	// MaxResultBytes bounds encoded JSON, including HTML escaping. Larger App
	// datasets should be paged through an owner-authorized artifact API.
	MaxResultBytes        = 16 << 10
	MaxResultPageItems    = 100
	MaxProgressPhaseBytes = 64
)

var ErrResultUnavailable = errors.New("job has no completed structured result")

// validateResult bounds both JSON and the whole receipt before the journal is
// written. An invalid worker result becomes a terminal failure, not a spool error.
func validateResult(result *Result) string {
	if len(result.JSON) > MaxResultBytes {
		return "result-too-large"
	}
	if len(result.JSON) != 0 {
		if !utf8.Valid(result.JSON) || !json.Valid(result.JSON) || !jsonlimits.WithinLimits(result.JSON) {
			return "result-invalid"
		}
		encoded, err := json.Marshal(result.JSON)
		if err != nil {
			return "result-invalid"
		}
		if len(encoded) > MaxResultBytes {
			return "result-too-large"
		}
	}
	encoded, err := json.Marshal(result)
	if err != nil {
		return "result-invalid"
	}
	if len(encoded) > resultReserve {
		return "result-too-large"
	}
	return ""
}

// ResultFor returns a successful structured result, retaining JSON null as a
// value. Every external call must construct access from fresh authentication.
// Missing, hidden and unauthorized jobs have the same ErrNotFound response.
func (s *Store) ResultFor(ctx context.Context, id string, access Access) (json.RawMessage, error) {
	job, err := s.GetFor(ctx, id, access)
	if err != nil {
		return nil, err
	}
	if job.State != StateSucceeded || job.Result == nil || len(job.Result.JSON) == 0 {
		return nil, ErrResultUnavailable
	}
	return job.Result.JSON, nil
}

// ResultPage is a stable slice of an immutable successful JSON array result.
// Offset counts array elements. A nil NextOffset means the result is exhausted.
type ResultPage struct {
	Items      []json.RawMessage `json:"items"`
	Offset     int               `json:"offset"`
	Total      int               `json:"total"`
	NextOffset *int              `json:"nextOffset,omitempty"`
}

// ResultPageFor reauthorizes every page. Non-array results are read with
// ResultFor. Pages never exceed MaxResultPageItems or the bounded JSON result.
func (s *Store) ResultPageFor(ctx context.Context, id string, access Access, offset, limit int) (ResultPage, error) {
	raw, err := s.ResultFor(ctx, id, access)
	if err != nil {
		return ResultPage{}, err
	}
	if offset < 0 || limit < 1 || limit > MaxResultPageItems || !bytes.HasPrefix(bytes.TrimSpace(raw), []byte("[")) {
		return ResultPage{}, ErrInvalid
	}
	var items []json.RawMessage
	if err := json.Unmarshal(raw, &items); err != nil {
		return ResultPage{}, ErrInvalid
	}
	if offset > len(items) {
		return ResultPage{}, ErrInvalid
	}
	end := offset + min(limit, len(items)-offset)
	page := ResultPage{Items: items[offset:end], Offset: offset, Total: len(items)}
	if end < len(items) {
		page.NextOffset = &end
	}
	return page, nil
}

// resultCharge releases unused admission reservation when a job finishes.
func resultCharge(result *Result) int64 {
	encoded, _ := json.Marshal(result)
	return int64(max(512, len(encoded)))
}
