package task

import (
	"io"
	"testing"

	"github.com/stretchr/testify/require"
	"golang.org/x/sys/windows"
)

func TestGetWaitsForWriterHoldingExclusiveFileHandle(t *testing.T) {
	assertTaskReadWaitsForWriter(t, func(path string) func() {
		name, err := windows.UTF16PtrFromString(path)
		require.NoError(t, err)
		handle, err := windows.CreateFile(name, windows.GENERIC_READ, 0,
			nil, windows.OPEN_EXISTING, windows.FILE_ATTRIBUTE_NORMAL, 0)
		require.NoError(t, err)
		return func() { require.NoError(t, windows.CloseHandle(handle)) }
	})
}

func TestTaskReaderAllowsAtomicStateReplacement(t *testing.T) {
	store := newTestStore(t)
	tk, err := store.Create(CreateOptions{})
	require.NoError(t, err)
	path, err := store.path(tk.ID())
	require.NoError(t, err)
	reader, err := openTaskFile(path)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, reader.Close()) })
	before, err := io.ReadAll(reader)
	require.NoError(t, err)

	require.NoError(t, tk.Fail(ErrAbandoned))
	state, err := store.Get(tk.ID())
	require.NoError(t, err)
	require.Equal(t, StatusFailed, state.Status)
	_, err = reader.Seek(0, io.SeekStart)
	require.NoError(t, err)
	after, err := io.ReadAll(reader)
	require.NoError(t, err)
	require.Equal(t, before, after)
}
