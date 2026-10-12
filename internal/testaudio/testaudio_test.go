// SPDX-License-Identifier: Apache-2.0

package testaudio

import (
	"testing"
	"time"

	"go.senan.xyz/taglib"
)

// TestGeneratedFilesAreReadable guards the generators: TagLib must read the
// expected length from every format, before and after tagging.
func TestGeneratedFilesAreReadable(t *testing.T) {
	dir := t.TempDir()
	for ext := range Generators {
		path := Write(t, dir, "x"+ext, 3, map[string][]string{"TITLE": {"probe"}})
		props, err := taglib.ReadProperties(path)
		if err != nil {
			t.Errorf("%s: ReadProperties: %v", ext, err)
			continue
		}
		if props.Length < 2900*time.Millisecond || props.Length > 3100*time.Millisecond {
			t.Errorf("%s: length = %v, want about 3s", ext, props.Length)
		}
		tags, err := taglib.ReadTags(path)
		if err != nil || len(tags["TITLE"]) != 1 || tags["TITLE"][0] != "probe" {
			t.Errorf("%s: tags = %v, %v", ext, tags, err)
		}
	}
}
