// Copyright (c) 2016 Denis Bernard <db047h@gmail.com>
// Use of this source code is governed by the MIT license
// which can be found in the LICENSE file.

// Package amr implements am asynchronous multi-reader: a single writer writes
// to a file while multiple readers can read from that same file concurrently.
// Only data that has already been written can be read. Reads will block until
// more data is available.
//
// This package can be used for example in a file cache where one goroutine
// writes the data to be cached while other goroutines can immediately start to
// read the cached data.
//
// Readers and Writers provide an Err() method that returns the first error
// that occurred during a Read or Write respectively. This enables chaining
// multiple read or writes and checking errors only once.
//
// TODO:
//
//  - Implement Seek for readers
//  - Wrapper with notification once all readers are done with a file
//  - Enable use of generic io.Reader/Writer as backend storage instead of jus files.
//
package amr
