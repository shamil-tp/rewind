// SPDX-License-Identifier: Apache-2.0

//go:build !unix

package server

import "errors"

func mkfifo(string) error { return errors.New("not supported") }
