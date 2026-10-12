// SPDX-License-Identifier: Apache-2.0

package plugin

import (
	"bytes"
	"errors"
	"testing"
)

// FuzzParseManifest feeds arbitrary bytes to the manifest parser. It must
// never panic, must reject with a ManifestError, and anything it accepts must
// pass validation again.
//
//	go test ./internal/plugin -run '^$' -fuzz FuzzParseManifest -fuzztime 60s
func FuzzParseManifest(f *testing.F) {
	for _, seed := range []string{
		validManifest,
		manifestFor("a", "1.0.0"),
		`{"schema":1,"id":"x","command":"sh -c id"}`,
		`{"schema":2}`, `[]`, `null`, `{`, ``,
		`{"schema":1,"id":"a","name":"<script>","version":"1.0.0","rewind_api":1,"capabilities":["events.library_scan"],"homepage":"https://a.b"}`,
	} {
		f.Add([]byte(seed))
	}
	f.Fuzz(func(t *testing.T, data []byte) {
		m, err := ParseManifest(bytes.NewReader(data))
		if err != nil {
			var me *ManifestError
			if !errors.As(err, &me) {
				t.Fatalf("error %v is not a ManifestError", err)
			}
			return
		}
		if err := m.Validate(); err != nil {
			t.Fatalf("accepted manifest fails validation: %v", err)
		}
		if !ValidID(m.ID) {
			t.Fatalf("accepted invalid id %q", m.ID)
		}
	})
}
