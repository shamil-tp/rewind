// SPDX-License-Identifier: Apache-2.0

package server

import (
	"bufio"
	"bytes"
	"context"
	"crypto/rand"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"testing/fstest"
	"time"

	"github.com/shamil-tp/rewind/internal/testaudio"
)

// shortDeadlines shortens the write deadlines for one test.
func shortDeadlines(t *testing.T, write, idle time.Duration) {
	t.Helper()
	oldW, oldI := writeTimeout, streamWriteIdle
	writeTimeout, streamWriteIdle = write, idle
	t.Cleanup(func() { writeTimeout, streamWriteIdle = oldW, oldI })
}

// serveReal runs the full handler on a real loopback socket.
func serveReal(t *testing.T, h http.Handler) string {
	t.Helper()
	lns, err := Listen([]string{"127.0.0.1:0"}, false)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { Serve(ctx, lns, h, slog.New(slog.DiscardHandler)); close(done) }()
	t.Cleanup(func() { cancel(); <-done })
	return lns[0].Addr().String()
}

// rawGet sends a request on a connection with a small receive buffer, so the
// server's writes block soon after the client stops reading. The buffer stays
// larger than one loopback segment (64 KiB on Linux): below that, the kernel
// drains the already-sent data through window probes, which took a minute
// per test on Linux, although the server had cut the connection after the
// deadline.
func rawGet(t *testing.T, addr, path string, header string) (net.Conn, *bufio.Reader) {
	t.Helper()
	c, err := net.Dial("tcp", addr)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { c.Close() })
	c.(*net.TCPConn).SetReadBuffer(128 << 10)
	fmt.Fprintf(c, "GET %s HTTP/1.1\r\nHost: localhost:8090\r\n%s\r\n", path, header)
	return c, bufio.NewReaderSize(c, 4096)
}

func randomBytes(t *testing.T, n int) []byte {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		t.Fatal(err)
	}
	return b
}

// streamEnv indexes one track and replaces its file with size random bytes
// (streaming serves the file as stored; its content is not re-parsed).
func streamEnv(t *testing.T, size int) (*testEnv, *http.Cookie, string, []byte) {
	t.Helper()
	e := newTestEnv(t)
	c := e.doSetup(t)
	dir := t.TempDir()
	path := testaudio.Write(t, dir, "big.flac", 1, map[string][]string{"TITLE": {"Big"}})
	e.scanNow(t, e.addLibrary(t, dir)[dir])
	data := randomBytes(t, size)
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}
	return e, c, path, data
}

func TestStalledReaderOfStaticFileIsDisconnected(t *testing.T) {
	shortDeadlines(t, 300*time.Millisecond, time.Minute)
	big := randomBytes(t, 16<<20)
	e := newTestEnv(t)
	opts := e.opts
	fe := testFrontend()
	fe["assets/big.js"] = &fstest.MapFile{Data: big}
	opts.Frontend = fe
	h, err := New(opts)
	if err != nil {
		t.Fatal(err)
	}
	_, r := rawGet(t, serveReal(t, h), "/assets/big.js", "")
	time.Sleep(time.Second) // stop reading for longer than the deadline
	resp, err := http.ReadResponse(r, nil)
	if err != nil {
		t.Fatal(err)
	}
	n, _ := io.Copy(io.Discard, resp.Body)
	if n >= int64(len(big)) {
		t.Fatalf("received all %d bytes: the stalled connection was not cut off", n)
	}
}

func TestStalledStreamIsDisconnected(t *testing.T) {
	shortDeadlines(t, time.Minute, 300*time.Millisecond)
	e, c, _, data := streamEnv(t, 16<<20)
	_, r := rawGet(t, serveReal(t, e.handler), "/api/tracks/1/stream", "Cookie: rewind_session="+c.Value+"\r\n")
	time.Sleep(time.Second)
	resp, err := http.ReadResponse(r, nil)
	if err != nil {
		t.Fatal(err)
	}
	n, _ := io.Copy(io.Discard, resp.Body)
	if resp.StatusCode != http.StatusOK || n >= int64(len(data)) {
		t.Fatalf("status %d, received %d of %d bytes: the stalled stream was not cut off", resp.StatusCode, n, len(data))
	}
}

func TestSteadySlowReaderGetsWholeStream(t *testing.T) {
	// Total time is several times the idle deadline, but each chunk is read
	// well within it: the deadline is per chunk, not per stream.
	shortDeadlines(t, 200*time.Millisecond, 700*time.Millisecond)
	e, c, _, data := streamEnv(t, 6<<20)
	_, r := rawGet(t, serveReal(t, e.handler), "/api/tracks/1/stream", "Cookie: rewind_session="+c.Value+"\r\n")
	resp, err := http.ReadResponse(r, nil)
	if err != nil {
		t.Fatal(err)
	}
	var got bytes.Buffer
	start := time.Now()
	buf := make([]byte, 256<<10)
	for {
		n, err := io.ReadFull(resp.Body, buf)
		got.Write(buf[:n])
		if err != nil {
			break
		}
		time.Sleep(80 * time.Millisecond)
	}
	if !bytes.Equal(got.Bytes(), data) {
		t.Fatalf("received %d of %d bytes after %s", got.Len(), len(data), time.Since(start))
	}
	if time.Since(start) < 1400*time.Millisecond {
		t.Errorf("transfer took only %s; the test needs it to outlast the idle deadline", time.Since(start))
	}
}

func TestStreamRangesOverRealConnection(t *testing.T) {
	e, c, _, data := streamEnv(t, 5<<20)
	addr := serveReal(t, e.handler)
	for _, tc := range []struct {
		rng        string
		start, end int // inclusive
	}{
		{"bytes=0-", 0, len(data) - 1},
		{"bytes=1000-2999999", 1000, 2999999},
		{"bytes=-4096", len(data) - 4096, len(data) - 1},
		{fmt.Sprintf("bytes=%d-", len(data)-1), len(data) - 1, len(data) - 1},
	} {
		_, r := rawGet(t, addr, "/api/tracks/1/stream", "Cookie: rewind_session="+c.Value+"\r\nRange: "+tc.rng+"\r\n")
		resp, err := http.ReadResponse(r, nil)
		if err != nil {
			t.Fatal(err)
		}
		body, err := io.ReadAll(resp.Body)
		if err != nil {
			t.Fatalf("%s: %v", tc.rng, err)
		}
		wantRange := fmt.Sprintf("bytes %d-%d/%d", tc.start, tc.end, len(data))
		if resp.StatusCode != http.StatusPartialContent || resp.Header.Get("Content-Range") != wantRange ||
			resp.ContentLength != int64(tc.end-tc.start+1) || !bytes.Equal(body, data[tc.start:tc.end+1]) {
			t.Errorf("%s: status %d, Content-Range %q, length %d, body ok %v", tc.rng, resp.StatusCode,
				resp.Header.Get("Content-Range"), resp.ContentLength, bytes.Equal(body, data[tc.start:tc.end+1]))
		}
	}
}

func TestDisconnectReleasesFileAndGoroutines(t *testing.T) {
	e, c, path, _ := streamEnv(t, 16<<20)
	addr := serveReal(t, e.handler)
	before := runtime.NumGoroutine()
	for i := 0; i < 5; i++ {
		conn, r := rawGet(t, addr, "/api/tracks/1/stream", "Cookie: rewind_session="+c.Value+"\r\n")
		resp, err := http.ReadResponse(r, nil)
		if err != nil {
			t.Fatal(err)
		}
		io.CopyN(io.Discard, resp.Body, 64<<10) // start playing, then leave
		conn.Close()
	}
	deadline := time.Now().Add(5 * time.Second)
	for runtime.NumGoroutine() > before+2 && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
	}
	if g := runtime.NumGoroutine(); g > before+2 {
		t.Errorf("goroutines: %d before, %d after the clients left", before, g)
	}
	// On Windows an open file cannot be renamed away: this also proves the
	// handles were closed.
	var err error
	for time.Now().Before(deadline) {
		if err = os.Rename(path, filepath.Join(filepath.Dir(path), "moved.flac")); err == nil {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if err != nil {
		t.Errorf("file still held open after the clients disconnected: %v", err)
	}
}

// Streaming copies the file in small chunks (or with sendfile): a 32 MiB
// track must not allocate anywhere near its size.
func TestStreamingMemoryIsBounded(t *testing.T) {
	e, c, _, data := streamEnv(t, 32<<20)
	addr := serveReal(t, e.handler)
	stream := func() {
		_, r := rawGet(t, addr, "/api/tracks/1/stream", "Cookie: rewind_session="+c.Value+"\r\n")
		resp, err := http.ReadResponse(r, nil)
		if err != nil {
			t.Fatal(err)
		}
		if n, _ := io.Copy(io.Discard, resp.Body); n != int64(len(data)) {
			t.Fatalf("streamed %d of %d bytes", n, len(data))
		}
	}
	stream() // warm up pools
	runtime.GC()
	var before, after runtime.MemStats
	runtime.ReadMemStats(&before)
	stream()
	runtime.ReadMemStats(&after)
	alloc := after.TotalAlloc - before.TotalAlloc
	t.Logf("allocated %d KiB to stream %d MiB", alloc>>10, len(data)>>20)
	if alloc > 4<<20 {
		t.Errorf("streaming %d MiB allocated %d KiB: the file is being buffered", len(data)>>20, alloc>>10)
	}
}
