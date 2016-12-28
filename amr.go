// Copyright (c) 2016 Denis Bernard <db047h@gmail.com>
// Use of this source code is governed by the MIT license
// which can be found in the LICENSE file.

package amr

import (
	"io"
	"os"
	"runtime"
	"sync"
)

const bufSize = 32 * 1024 // same buffer size as io.copy

// A Writer manages writing and creation of asynchronous readers on a given os.File.
// It implements io.CloseWriter.
//
type Writer struct {
	c   *sync.Cond // sync with readers
	f   *os.File   // file descriptor
	w   int64      // bytes written so far
	err error      // last error
}

type reader struct {
	f   *os.File
	w   *Writer
	r   int64 // bytes read so far
	err error // last error
}

// ReadCloser wraps an io.ReadCloser with the Err method that returns the first error that occurred in Read().
type ReadCloser interface {
	io.ReadCloser

	// Err returns the first error that occurred in Read(). This function is not thread-safe.
	Err() error
}

// Create creates the named file with mode 0666 (before umask). Create fails if
// the file already exists. If successful, methods on the returned Writer can be
// used for output; the associated file descriptor has mode O_WRONLY. If there
// is an error, it will be of type *PathError.
//
// Clients must call Close() on the returned writer to unregister the file as
// active and to allow read operations on the same file to complete successfully
// (i.e. readers will not receive io.EOF until the writer is closed).
//
func Create(name string) (*Writer, error) {
	f, err := os.OpenFile(name, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0666)
	if err != nil {
		return nil, err
	}
	w := &Writer{
		c: sync.NewCond(&sync.Mutex{}),
		f: f,
	}
	return w, nil
}

// Wrap returns a Writer for the given os.File. The file must be writable and
// the writer will assume that it will start writing at the given offset (no
// Seek will be performed by this function).
//
// Callers must make sure that all further io operations on that file will be
// performed through the returned Writer. Failing to do so will result in
// unexpected behavior from the Writer.
//
func Wrap(f *os.File, offset int64) *Writer {
	return &Writer{
		c: sync.NewCond(&sync.Mutex{}),
		f: f,
		w: offset,
	}
}

// Name returns the name of the file as presented to Create
func (w *Writer) Name() string {
	return w.f.Name()
}

// Err returns the first error encountered by the writer, or nil if no erorrs occurred.
// May return io.EOF if the writer has been closed cleanly with no other errors.
func (w *Writer) Err() error {
	w.c.L.Lock()
	err := w.err
	w.c.L.Unlock()
	return err
}

func (w *Writer) Write(p []byte) (n int, err error) {
	// split the write in bufSize chunks so that we can update readers at regular intervals
	tot := len(p)
	for n < tot && err == nil {
		var t int
		if len(p) > bufSize {
			t, err = w.f.Write(p[:bufSize])
		} else {
			t, err = w.f.Write(p)
		}
		n += t
		w.c.L.Lock()
		w.w += int64(t)
		if err != nil {
			w.err = err
		}
		w.c.L.Unlock()
		w.c.Broadcast()
		p = p[t:]
	}
	return n, err
}

// Close closes the underlying os.File, rendering it unusable for I/O. It also
// sets Err to io.EOF, notifying all readers reading from the same file that the
// file is complete. It returns an error, if any.
//
func (w *Writer) Close() error {
	// notify readers
	w.c.L.Lock()
	if w.err == nil {
		w.err = io.EOF
	}
	w.c.L.Unlock()
	w.c.Broadcast()
	return w.f.Close()
}

// Cancel cancels the writer as well as all readers reading the same file.
// The given error will be propagated to all pending and future Read/Write calls.
//
func (w *Writer) Cancel(err error) {
	w.c.L.Lock()
	w.err = err
	w.c.L.Unlock()
	w.c.Broadcast()
}

// NewReader returns a ReadCloser that reads from the same file name that
// was used in the call to Create. In some cases it may be advisable to call
// path.Abs() on the file name before calling Create and NewReader.
//
func (w *Writer) NewReader() (ReadCloser, error) {
	w.c.L.Lock()
	defer w.c.L.Unlock()

	if w.err != nil && w.err != io.EOF {
		return nil, w.err
	}
	f, err := os.Open(w.f.Name())
	if err != nil {
		return nil, err
	}
	r := &reader{f, w, 0, nil}
	runtime.SetFinalizer(r, (*reader).Close)
	return r, nil
}

// done removes reference to writer and sets error.
func (r *reader) done(err error) {
	if r.err == nil {
		r.err = err
	}
	if r.w != nil {
		r.w = nil
	}
}

func (r *reader) Err() error {
	return r.err
}

func (r *reader) Read(p []byte) (n int, err error) {
	w := r.w

	if r.err != nil {
		return 0, r.err
	}

	if w == nil {
		panic("Nil writer with no error set")
	}

	// get bytes written so far && wait for more if needed
	w.c.L.Lock()
	if r.r == w.w && w.err == io.EOF {
		// wirter EOF and we've read all written bytes, we're done
		w.c.L.Unlock()
		r.done(io.EOF)
		return 0, io.EOF
	}
	// wait for state change
	for w.err == nil && r.r == w.w {
		w.c.Wait()
	}
	tot := w.w
	if w.err != nil && (w.err != io.EOF || tot == r.r) {
		// again, ignore EOF unless we have no unread bytes
		err = w.err
	}
	w.c.L.Unlock()

	if err != nil {
		r.done(err)
		return 0, err
	}

	max := tot - r.r // we may be reading files over 4GB, this needs to be int64
	if int64(len(p)) >= max {
		n, err = r.f.Read(p[:max])
	} else {
		n, err = r.f.Read(p)
	}
	r.r += int64(n)
	if err != nil {
		r.done(err)
	}
	return n, err
}

func (r *reader) Close() error {
	// do not call done. Should be triggered by an error on the next read.
	return r.f.Close()
}
