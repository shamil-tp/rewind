// SPDX-License-Identifier: Apache-2.0

package server

import (
	"net/http"

	"github.com/shamil-tp/rewind/internal/plugin"
)

type pluginStatusResponse struct {
	// Configured is false when REWIND_PLUGIN_DIR is not set.
	Configured bool            `json:"configured"`
	APIVersion int             `json:"api_version"`
	Items      []plugin.Status `json:"items"`
}

// pluginStatus reports installed plugins: manifest metadata and state only,
// never paths, configuration or a plugin's own error text. It changes
// nothing and runs no plugin code.
func pluginStatus(m *plugin.Manager) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if _, ok := queryParams(w, r); !ok {
			return
		}
		res := pluginStatusResponse{APIVersion: plugin.APIVersion, Items: []plugin.Status{}}
		if m != nil {
			res.Configured = m.Configured()
			res.Items = append(res.Items, m.Status()...)
		}
		writeJSON(w, http.StatusOK, res)
	}
}
