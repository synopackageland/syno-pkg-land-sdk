package main

import (
	"bufio"
	"errors"
	"io"
)

// errParentGone means the move parent closed or broke the control pipe during
// the copy: it exited or was killed, so no one can ever send publish.
var errParentGone = errors.New("parent gone")

type controlLine struct {
	line string
	err  error
}

// awaitControl reads the parent's next decision in the background. Pipe EOF
// is the parent-death signal on every kernel, including 3.10, and unlike
// PR_SET_PDEATHSIG it survives the child's identity drop and Go's threads.
func awaitControl(r *bufio.Reader) <-chan controlLine {
	next := make(chan controlLine, 1)
	go func() {
		line, err := r.ReadString('\n')
		next <- controlLine{line, err}
	}()
	return next
}

// copyUnlessParentGone copies exactly size bytes, checking control between
// chunks. The parent writes nothing during a copy, so any arrival ends it.
func copyUnlessParentGone(out io.Writer, src io.Reader, size int64, control <-chan controlLine) error {
	limited := io.LimitReader(src, size+1)
	buf := make([]byte, 1<<20)
	var n int64
	for {
		select {
		case <-control:
			return errParentGone
		default:
		}
		m, err := limited.Read(buf)
		if m > 0 {
			if _, werr := out.Write(buf[:m]); werr != nil {
				return werr
			}
			n += int64(m)
		}
		if err == io.EOF {
			break
		}
		if err != nil {
			return err
		}
	}
	if n != size {
		return errors.New("size mismatch")
	}
	return nil
}

func copyExact(out io.Writer, src io.Reader, size int64) error {
	return copyUnlessParentGone(out, src, size, nil)
}
