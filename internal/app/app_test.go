// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at http://mozilla.org/MPL/2.0/.

package app_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/siderolabs/go-api-signature/pkg/serviceaccount"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/siderolabs/omni_exporter/internal/app"
)

func TestLoadServiceAccountKey(t *testing.T) {
	keyFile := filepath.Join(t.TempDir(), "key")
	require.NoError(t, os.WriteFile(keyFile, []byte("file-key\n"), 0o600))

	t.Setenv(serviceaccount.OmniServiceAccountKeyEnvVar, "env-key")

	key, err := app.LoadServiceAccountKey(keyFile)
	require.NoError(t, err)
	assert.Equal(t, "file-key", key, "the file must take precedence and be trimmed")

	key, err = app.LoadServiceAccountKey("")
	require.NoError(t, err)
	assert.Equal(t, "env-key", key)

	t.Setenv(serviceaccount.OmniServiceAccountKeyEnvVar, "")

	_, err = app.LoadServiceAccountKey("")
	require.Error(t, err)

	_, err = app.LoadServiceAccountKey(filepath.Join(t.TempDir(), "missing"))
	require.Error(t, err)
}

func TestBuildLogger(t *testing.T) {
	t.Parallel()

	for _, format := range []string{"json", "text"} {
		logger, err := app.BuildLogger("debug", format)
		require.NoError(t, err)
		require.NotNil(t, logger)
	}

	_, err := app.BuildLogger("not-a-level", "json")
	require.Error(t, err)
}
