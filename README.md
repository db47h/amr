# amr - Asynchronous Multi Reader

[![Build Status](https://travis-ci.org/db47h/amr.svg?branch=master)](https://travis-ci.org/db47h/amr)
[![Go Report Card](https://goreportcard.com/badge/github.com/db47h/amr)](https://goreportcard.com/report/github.com/db47h/amr)
[![Coverage Status](https://coveralls.io/repos/github/db47h/amr/badge.svg)](https://coveralls.io/github/db47h/amr)  [![GoDoc](https://godoc.org/github.com/db47h/amr?status.svg)](https://godoc.org/github.com/db47h/amr)

The amr package implements am asynchronous multi-reader: a single writer writes
to a file while multiple readers can read from that same file concurrently.
Only data that has already been written can be read. Reads will block until
more data is available.

This package can be used for example in a file cache where one goroutine
writes the data to be cached while other goroutines can immediately start to
read the cached data.

Readers and Writers provide an Err() method that returns the first error
that occurred during a Read or Write respectively. This enables chaining
multiple read or writes and checking errors only once.

## TODO:
	- Implement Seek for readers
  - Wrapper with notification once all readers are done with a file
  - Enable use of generic io.Reader/Writer as backend storage instead of jus files.
