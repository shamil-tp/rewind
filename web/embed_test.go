// SPDX-License-Identifier: Apache-2.0

package web

import (
	"io/fs"
	"strings"
	"testing"
)

func TestFrontendHasIndex(t *testing.T) {
	files, err := Frontend()
	if err != nil {
		t.Fatalf("Frontend() error = %v", err)
	}
	index, err := fs.ReadFile(files, "index.html")
	if err != nil {
		t.Fatalf("index.html: %v", err)
	}
	// The placeholder must identify itself so it is never mistaken for the
	// real application.
	isPlaceholder := strings.Contains(string(index), "frontend is not included in this build")
	if isPlaceholder == Embedded {
		t.Errorf("Embedded = %v but placeholder marker present = %v", Embedded, isPlaceholder)
	}
}
