// SPDX-License-Identifier: Apache-2.0

//go:build !unix

package library

import "os"

// openFlags opens track files read-only.
const openFlags = os.O_RDONLY
