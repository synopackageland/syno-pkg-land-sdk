package main

import (
	"context"
	"errors"
	"log"
	"time"
)

type moveValidationStageKey struct{}

type moveValidationObservation struct {
	stage    string
	deadline bool
}

func moveValidationStage(ctx context.Context, stage string) {
	if out, ok := ctx.Value(moveValidationStageKey{}).(*moveValidationObservation); ok {
		out.stage = stage
	}
}

func moveValidationDeadline(ctx context.Context, err error) bool {
	deadline := ctx.Err() == context.DeadlineExceeded || errors.Is(err, context.DeadlineExceeded)
	if deadline {
		if out, ok := ctx.Value(moveValidationStageKey{}).(*moveValidationObservation); ok {
			out.deadline = true
		}
	}
	return deadline
}

// Only fixed stage/code labels and a validated job identifier are logged. No
// account, request, credential, grant, path or DSM command output is included.
// Each operation emits at most eight diagnostics, even for a 24-hour move.
func (e *Executor) observeMoveValidation(ctx context.Context, d Delegation, job string, remaining *int) (Subject, string, bool) {
	observation := moveValidationObservation{stage: "policy"}
	started := time.Now()
	s, code := e.validateMove(context.WithValue(ctx, moveValidationStageKey{}, &observation), d)
	deadline := observation.deadline || ctx.Err() == context.DeadlineExceeded
	if *remaining > 0 {
		log.Printf("move validation job=%s stage=%s code=%q elapsed_ms=%d deadline=%t canceled=%t", job, observation.stage, code, time.Since(started).Milliseconds(), deadline, ctx.Err() == context.Canceled)
		*remaining--
	}
	return s, code, code == "EXECUTOR_PAUSED" && deadline && ctx.Err() != context.Canceled
}
