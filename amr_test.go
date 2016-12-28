package amr_test

import (
	"bytes"
	"crypto/rand"
	"errors"
	"fmt"
	"io"
	"io/ioutil"
	"os"
	"path"
	"runtime"
	"sync"
	"testing"
	"time"

	"github.com/db47h/amr"
)

func testFileName() string {
	d, err := ioutil.TempDir("", "amr")
	if err != nil {
		panic(err)
	}
	return path.Join(d, "amr_test")
}

func removeTestFile(name string) {
	d := path.Dir(name)
	// make sure we only delete our own stuff
	t := os.TempDir()
	if len(d) <= len(t) || d[:len(t)] != t {
		panic(fmt.Sprintf("removeTestFile: path mismatch: %q not a file in a subdirectory of %q", name, t))
	}
	os.RemoveAll(d)
}

func Example() {
	// Create a temp file
	tf, err := ioutil.TempFile("", "amr_test")
	if err != nil {
		panic(err)
	}
	defer os.Remove(tf.Name())

	// wrap the file and spawn a new go routine that will write to it
	w := amr.Wrap(tf, 0)

	// start a few readers
	f := func(wg *sync.WaitGroup) {
		r, err := w.NewReader()
		if err != nil {
			panic(err)
		}
		defer r.Close()
		var b = new(bytes.Buffer)
		n, err := io.Copy(b, r)
		if err != nil {
			panic(err)
		}
		fmt.Printf("Read %d bytes: %s\n", n, b.String())
		wg.Done()
	}

	var wg sync.WaitGroup
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go f(&wg)
	}

	// Just for the example's sake, start the writer goroutine last, although
	// there is no guarantee about which goroutine will really start first.

	// start writer
	go func(w io.WriteCloser) {
		// we must close the writer once done
		defer w.Close()
		// write some stuff
		w.Write([]byte("Hello, World!"))
	}(w)

	// wait for readers to finish
	wg.Wait()

	// Output:
	// Read 13 bytes: Hello, World!
	// Read 13 bytes: Hello, World!
	// Read 13 bytes: Hello, World!
	// Read 13 bytes: Hello, World!
}

// Demonstrates a typical usage pattern in a caching system. The idea is to always
// assume that the cached data needs to be constructed and call Create first.
func Example_cache() {
	// global file registry
	var fLock sync.Mutex
	var files = make(map[string]*amr.Writer)
	// these two lines just create a temp directory and file and remove it on exit
	testFile := testFileName()
	defer removeTestFile(testFile)

	// genCacheData generates the cached data
	genCacheData := func(dst *amr.Writer, src io.Reader) {
		defer dst.Close()
		_, err := io.Copy(dst, src)
		if err != nil {
			dst.Cancel(err)
		}
		time.Sleep(100 * time.Millisecond)
	}

	// open returns a ReadCloser to some file that may be cached
	open := func(name string) (io.ReadCloser, error) {
		fLock.Lock()
		defer fLock.Unlock()

		// do we have a writer for that file?
		if w := files[name]; w != nil {
			return w.NewReader()
		}

		// no: create file and generate data
		w, err := amr.Create(name)
		if err != nil {
			return nil, err
		}
		files[name] = w
		go genCacheData(w, bytes.NewReader([]byte("Hello, World!")))
		return w.NewReader()
	}

	// start a bunnch of goroutines trying to read the cached data
	var wg sync.WaitGroup

	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() {
			var b bytes.Buffer
			r, err := open(testFile)
			if err != nil {
				wg.Done()
				panic(err)
			}
			defer func() { r.Close(); wg.Done() }()
			n, err := io.Copy(&b, r)
			if err != nil {
				panic(err)
			}
			fmt.Printf("%s\n", b.Bytes()[:n])
		}()
	}
	wg.Wait()

	// Output:
	// Hello, World!
	// Hello, World!
	// Hello, World!
	// Hello, World!
}

func testCreate(delay bool, t *testing.T) {
	// TL;DR: users of the package do not need to use wait groups.
	// we use a wait group to make sure that the readers are started before the writer
	// starts to write data. Otherwise, the test may return false PASS results if the writer
	// gets a chance to write everything before the readers start.
	var wgInit, wgDone sync.WaitGroup
	var data = make([]byte, 2048*1024)
	var testFile = testFileName()

	// generate sample data
	rand.Read(data)

	w, err := amr.Create(testFile)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if delay {
			time.Sleep(200 * time.Millisecond)
		}
		w.Close()
		wgDone.Wait()
		removeTestFile(testFile)
	}()

	f := func(r io.ReadCloser) {
		var p int
		var b = make([]byte, 1024)

		defer func() {
			r.Close()
			wgDone.Done()
		}()

		wgInit.Done() // wg is just here to notify that the reader has actually started

		for {
			n, err := r.Read(b)
			if n > 0 {
				if bytes.Compare(data[p:p+n], b[:n]) != 0 {
					t.Fatal("Read data differs from reference data")
					return
				}
				p += n
				if p > len(data) {
					t.Fatalf("Read more bytes than expected. Expected %d, read %d", len(data), p)
					return
				}
			}
			if err != nil {
				if err != io.EOF {
					t.Fatal(err)
				}
				if p != len(data) {
					t.Errorf("Expected %d bytes, read %d", len(data), p)
				}
				return
			} else if n == 0 {
				t.Error("Got nil error with no bytes read.")
				return
			}
		}
	}

	const clients = 4
	wgInit.Add(clients)
	wgDone.Add(clients)
	// spawn readers
	for i := 0; i < clients; i++ {
		r, err := w.NewReader()
		if err != nil {
			t.Fatal(err)
		}
		go f(r)
	}
	wgInit.Wait() // wait for readers to be started
	io.Copy(w, bytes.NewReader(data))
}

func TestCreate_1(t *testing.T) {
	testCreate(false, t)
}

// same as above, but introduce a delay before closing the writer's close method
func TestCreate_2(t *testing.T) {
	testCreate(true, t)
}

// Make sure that os.O_EXCL works when creating the file
func TestCreate_excl(t *testing.T) {
	f1, err := os.OpenFile(testFileName(), os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0666)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		f1.Close()
		removeTestFile(f1.Name())
	}()
	f2, err := amr.Create(f1.Name())
	if err == nil {
		f2.Close()
		t.Error("Unexpected Create success")
	} else if !os.IsExist(err) {
		t.Error(err)
	}
}

func TestWriter_Name(t *testing.T) {
	name := testFileName()
	w, err := amr.Create(name)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { w.Close(); removeTestFile(name) }()
	if w.Name() != name {
		t.Fatalf("Filename mismatch: got %q, expected %q", w.Name(), name)
	}
}

func TestWriter_Cancel(t *testing.T) {
	c := make(chan struct{})
	cancelled := errors.New("CANCEL")
	f, err := ioutil.TempFile("", "amr_cancel")
	if err != nil {
		t.Fatal(err)
	}
	w := amr.Wrap(f, 0)
	defer func() {
		w.Close()
		os.Remove(f.Name())
	}()
	go func() {
		r, err := w.NewReader()
		if err != nil {
			t.Fatal(err)
		}
		defer r.Close()
		b := make([]byte, 1024)
		_, err = r.Read(b)
		if err != cancelled {
			t.Fatalf("Got unexpected error %v", err)
		}
		// again
		_, err = r.Read(b)
		if err != cancelled {
			t.Fatalf("Got unexpected error %v", err)
		}
		c <- struct{}{}
	}()
	runtime.Gosched()
	w.Cancel(cancelled)
	<-c
}

func TestWriter_NewReader_errors(t *testing.T) {
	f, err := ioutil.TempFile("", "amr_newreader_error")
	if err != nil {
		t.Fatal(err)
	}
	w := amr.Wrap(f, 0)
	w.Close()
	os.Remove(f.Name())

	_, err = w.NewReader()
	if err == nil {
		t.Fatalf("NewReader: unexpected success opening %v", f.Name())
	}
	if !os.IsNotExist(err) {
		t.Fatalf("NewReader: unexpected error opening %v", f.Name())
	}

	_, err = w.Write([]byte{'x'})
	if err == nil {
		t.Fatal("unexpected write success")
	}
	_, ok := err.(*os.PathError)
	if !ok {
		t.Fatalf("Unexpected write error %v", err)
	}

	_, err = w.NewReader()
	if err == nil {
		t.Fatal("NewReader: unexpected success opening")
	}
	if err != w.Err() {
		t.Fatalf("NewReader: unexpected error %v", err)
	}
}

func TestReader_Close(t *testing.T) {
	f, err := ioutil.TempFile("", "amr_reader_close")
	if err != nil {
		t.Fatal(err)
	}
	w := amr.Wrap(f, 0)
	defer func() {
		w.Close()
		os.Remove(f.Name())
	}()
	w.Write([]byte("foo"))
	w.Close()

	r, err := w.NewReader()
	if err != nil {
		t.Fatal(err)
	}
	r.Close()

	if r.Err() != nil {
		t.Fatalf("r.Err() == %v, != nil", r.Err())
	}

	b := make([]byte, 1024)
	_, err = r.Read(b)
	if err == nil {
		t.Fatal("Unexpected nil error from Read")
	}
}
