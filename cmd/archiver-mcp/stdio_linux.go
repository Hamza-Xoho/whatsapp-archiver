//go:build linux

package main

import (
	"context"
	"errors"
	"io"
	"os"
	"sync"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"golang.org/x/sys/unix"
)

type closeWriter struct{ io.Writer }

func (w closeWriter) Close() error {
	if closer, ok := w.Writer.(io.Closer); ok {
		return closer.Close()
	}
	return nil
}

type terminalReader struct {
	io.ReadCloser
	ended   chan struct{}
	readErr error
	once    sync.Once
}

func (r *terminalReader) Read(p []byte) (int, error) {
	n, err := r.ReadCloser.Read(p)
	if err != nil {
		r.once.Do(func() {
			r.readErr = err
			close(r.ended)
		})
	}
	return n, err
}

// pollableCopy owns a duplicate, never the caller's descriptor. Inherited
// stdin/stdout are initially blocking and os.NewFile alone does not register
// them with Go's poller. Set O_NONBLOCK before NewFile so Close can interrupt
// both a blocked read and a blocked pipe write. Dup shares the file description,
// so restore its original nonblocking bit after all transport I/O has stopped.
// The caller lends these streams exclusively for the duration of serveStdio.
func pollableCopy(file *os.File) (*os.File, func(), error) {
	raw, err := file.SyscallConn()
	if err != nil {
		return nil, nil, err
	}
	fd, flags := -1, 0
	var fdErr error
	err = raw.Control(func(original uintptr) {
		flags, fdErr = unix.FcntlInt(original, unix.F_GETFL, 0)
		if fdErr != nil {
			return
		}
		fd, fdErr = unix.FcntlInt(original, unix.F_DUPFD_CLOEXEC, 0)
		if fdErr == nil {
			fdErr = unix.SetNonblock(fd, true)
		}
	})
	if err != nil || fdErr != nil {
		if fd >= 0 {
			_ = unix.Close(fd)
		}
		return nil, nil, errors.Join(err, fdErr)
	}
	owned := os.NewFile(uintptr(fd), file.Name())
	release := func() {
		_ = owned.Close()
		_ = raw.Control(func(original uintptr) {
			_ = unix.SetNonblock(int(original), flags&unix.O_NONBLOCK != 0)
		})
	}
	return owned, release, nil
}

func serveStdio(ctx context.Context, server *mcp.Server, input io.ReadCloser, output io.Writer) error {
	if file, ok := input.(*os.File); ok {
		owned, release, err := pollableCopy(file)
		if err != nil {
			return err
		}
		defer release()
		input = owned
	}
	if file, ok := output.(*os.File); ok {
		owned, release, err := pollableCopy(file)
		if err != nil {
			return err
		}
		defer release()
		output = owned
	}
	reader := &terminalReader{ReadCloser: input, ended: make(chan struct{})}
	writer := closeWriter{output}
	var closeOnce sync.Once
	interrupt := func() {
		closeOnce.Do(func() {
			// Output must be interrupted before waiting for SDK drain. The
			// SDK otherwise waits for active writes before closing its stream.
			_ = writer.Close()
			_ = reader.Close()
		})
	}
	finished, stopped := make(chan struct{}), make(chan struct{})
	go func() {
		defer close(stopped)
		select {
		case <-ctx.Done():
			interrupt()
		case <-reader.ended:
			interrupt()
		case <-finished:
		}
	}()
	err := server.Run(ctx, &mcp.IOTransport{Reader: reader, Writer: writer, MaxLineLength: 128 * 1024})
	close(finished)
	<-stopped
	interrupt()
	if ctx.Err() != nil {
		return ctx.Err()
	}
	select {
	case <-reader.ended:
		// EOF can interrupt a pending response before the SDK observes the
		// read EOF. Preserve malformed-frame errors; only normalize I/O close.
		if errors.Is(reader.readErr, io.EOF) && (errors.Is(err, os.ErrClosed) || errors.Is(err, io.ErrClosedPipe)) {
			return io.EOF
		}
	default:
	}
	return err
}
