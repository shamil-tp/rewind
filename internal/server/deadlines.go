// SPDX-License-Identifier: Apache-2.0

package server

import (
	"io"
	"net/http"
	"time"
)

// Write deadlines bound how long a client that stops reading can hold a
// connection, its goroutine and (for audio) an open file. They are variables
// so tests can shorten them.
var (
	// writeTimeout bounds writing an ordinary response (API JSON, frontend
	// files).
	writeTimeout = 60 * time.Second
	// streamWriteIdle bounds each chunk of an audio stream. A playing browser
	// keeps reading, so the deadline keeps moving; a client that reads nothing
	// for this long (for example, playback paused for a long time) is
	// disconnected, and the browser reconnects with a Range request when it
	// needs more data.
	streamWriteIdle = 10 * time.Minute
)

// streamChunk is how much of a stream is written per deadline extension.
const streamChunk = 1 << 20

// boundWrites sets the write deadline for every response. Handlers that
// stream (see idleWriter) replace it.
func boundWrites(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Not supported by test recorders; real connections always support it.
		_ = http.NewResponseController(w).SetWriteDeadline(time.Now().Add(writeTimeout))
		next.ServeHTTP(w, r)
	})
}

// idleWriter extends the write deadline before each chunk, so a stream may
// last as long as the client keeps reading, but each chunk must be accepted
// within idle. ReadFrom keeps the server's zero-copy path (sendfile on Linux)
// by passing the file through in bounded chunks.
type idleWriter struct {
	http.ResponseWriter
	rc   *http.ResponseController
	idle time.Duration
}

func newIdleWriter(w http.ResponseWriter, idle time.Duration) *idleWriter {
	iw := &idleWriter{ResponseWriter: w, rc: http.NewResponseController(w), idle: idle}
	iw.extend()
	return iw
}

func (w *idleWriter) extend() { _ = w.rc.SetWriteDeadline(time.Now().Add(w.idle)) }

// Unwrap lets http.ResponseController reach the underlying writer.
func (w *idleWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }

func (w *idleWriter) Write(p []byte) (int, error) {
	var written int
	for len(p) > 0 {
		n := min(len(p), streamChunk)
		w.extend()
		m, err := w.ResponseWriter.Write(p[:n])
		written += m
		if err != nil {
			return written, err
		}
		p = p[n:]
	}
	return written, nil
}

func (w *idleWriter) ReadFrom(src io.Reader) (int64, error) {
	rf, ok := w.ResponseWriter.(io.ReaderFrom)
	if !ok {
		return io.Copy(writerOnly{w}, src)
	}
	// http.ServeContent passes an *io.LimitedReader around the file. Chunks
	// are cut from it without nesting another LimitedReader, which would hide
	// the file from the zero-copy path.
	if lr, ok := src.(*io.LimitedReader); ok {
		var total int64
		for lr.N > 0 {
			chunk := &io.LimitedReader{R: lr.R, N: min(lr.N, streamChunk)}
			want := chunk.N
			w.extend()
			n, err := rf.ReadFrom(chunk)
			lr.N -= n
			total += n
			if err != nil || n < want {
				return total, err
			}
		}
		return total, nil
	}
	var total int64
	for {
		w.extend()
		n, err := rf.ReadFrom(&io.LimitedReader{R: src, N: streamChunk})
		total += n
		if err != nil || n < streamChunk {
			return total, err
		}
	}
}

// writerOnly hides ReadFrom so io.Copy uses Write.
type writerOnly struct{ io.Writer }
