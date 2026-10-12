// SPDX-License-Identifier: Apache-2.0

//go:build unix

package library

import (
	"os"
	"syscall"
)

// openFlags opens track files read-only and non-blocking, so opening a FIFO
// (which would otherwise wait for a writer) returns at once and is then
// rejected as not a regular file. O_NONBLOCK has no effect on regular files.
const openFlags = os.O_RDONLY | syscall.O_NONBLOCK
