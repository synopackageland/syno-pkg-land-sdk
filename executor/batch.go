package main

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"net"
	"time"
)

const maxBatchRootCalls = 32
const maxBatchDuration = 60 * time.Second

type batchVerifiedKey struct{}
type batchVerified struct {
	subject Subject
	auth    Auth
}

func readBatchLine(r *bufio.Reader, limit int) ([]byte, error) {
	var line []byte
	for {
		part, err := r.ReadSlice('\n')
		line = append(line, part...)
		if len(line) > limit {
			return line, errors.New("batch line limit")
		}
		if err == nil {
			return line, nil
		}
		if err != bufio.ErrBufferFull {
			return nil, err
		}
	}
}

func (e *Executor) batchConnection(parent context.Context, c net.Conn, reader *bufio.Reader, req Request, peerUID, packageUID int) {
	answer := func(out Response) bool {
		c.SetWriteDeadline(time.Now().Add(5 * time.Second))
		return json.NewEncoder(c).Encode(out) == nil
	}
	var in struct {
		Version   int   `json:"v"`
		TimeoutMS int64 `json:"timeout_ms"`
	}
	if !contains(e.Policy.Operations, "root.run") && !contains(e.Policy.Operations, "root.jobs.list") && !contains(e.Policy.Operations, "root.start") && !contains(e.Policy.Operations, "status") {
		answer(deny("POLICY_DENIED"))
		return
	}
	if peerUID != packageUID {
		answer(deny("PEER_DENIED"))
		return
	}
	if req.Version != 1 || strictJSON(req.Params, &in) != nil || in.Version != 1 || in.TimeoutMS < 1 || in.TimeoutMS > maxBatchDuration.Milliseconds() {
		answer(deny("INVALID_REQUEST"))
		return
	}
	ctx, cancel := context.WithTimeout(parent, time.Duration(in.TimeoutMS)*time.Millisecond)
	defer cancel()
	ctx = withRequestAddress(ctx, req.Auth)
	if e.starting.Load() {
		answer(deny("EXECUTOR_STARTING"))
		return
	}
	admitted, release, code := e.admitRequest(ctx, req)
	if code != "" {
		answer(deny(code))
		return
	}
	defer release()
	ctx = admitted
	if !answer(success(map[string]any{"v": 1})) {
		return
	}
	stop := context.AfterFunc(ctx, func() { c.Close() })
	defer stop()
	type incoming struct {
		line []byte
		err  error
	}
	incomingSteps := make(chan incoming, 1)
	go func() {
		for count := 0; count < maxBatchRootCalls; count++ {
			line, err := readBatchLine(reader, maxRootRequest)
			if err != nil {
				cancel()
				return
			}
			select {
			case incomingSteps <- incoming{line, nil}:
			case <-ctx.Done():
				return
			}
		}
	}()
	for count := 0; count < maxBatchRootCalls; count++ {
		deadline, _ := ctx.Deadline()
		c.SetDeadline(deadline)
		var line []byte
		select {
		case in := <-incomingSteps:
			line = in.line
		case <-ctx.Done():
			return
		}

		var step Request
		if strictJSON(line, &step) != nil || step.Auth.present() {
			answer(deny("INVALID_REQUEST"))
			return
		}
		if !contains([]string{"status", "root.run", "root.start", "root.jobs.list", "root.jobs.get", "root.jobs.stop"}, step.Op) {
			answer(deny("POLICY_DENIED"))
			continue
		}
		step.Auth = req.Auth
		// Every step still enters the ordinary policy, schema, privilege and slot
		// checks. Only the verified identity is scoped to this bounded request.
		if !answer(e.handlePeer(ctx, step, peerUID, packageUID)) {
			return
		}
	}
}
