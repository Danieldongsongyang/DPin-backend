package service

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestAvailableDiskBytes(t *testing.T) {
	directory := t.TempDir()
	available, err := availableDiskBytes(directory)
	require.NoError(t, err)
	assert.Greater(t, available, uint64(0))

	_, err = availableDiskBytes(filepath.Join(directory, "missing"))
	assert.Error(t, err)
}

func TestFeishuDurablePrivateFiles(t *testing.T) {
	path := filepath.Join(t.TempDir(), "receipt.json")
	file, err := createFeishuPrivateFile(path)
	require.NoError(t, err)
	_, err = file.WriteString(`{"confirmed":true}`)
	require.NoError(t, err)
	require.NoError(t, file.Sync())
	require.NoError(t, file.Close())
	require.NoError(t, syncFeishuCreatedFile(path))
	data, err := os.ReadFile(path)
	require.NoError(t, err)
	assert.Equal(t, `{"confirmed":true}`, string(data))
	duplicate, err := createFeishuPrivateFile(path)
	if duplicate != nil {
		duplicate.Close()
	}
	assert.ErrorIs(t, err, os.ErrExist, "a persisted receipt must not be overwritten")
	assert.Error(t, syncFeishuCreatedFile(filepath.Join(filepath.Dir(path), "missing.json")))
}
