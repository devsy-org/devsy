package tunnel

import (
	"context"
	"sync"
	"testing"
	"time"

	config2 "github.com/devsy-org/devsy/pkg/devcontainer/config"
	"github.com/stretchr/testify/assert"
)

// openedRecorder records opened URLs behind a mutex, since maybeOpenBrowser
// invokes openURLFunc from a spawned goroutine.
type openedRecorder struct {
	mu   sync.Mutex
	urls []string
}

func (r *openedRecorder) Snapshot() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string(nil), r.urls...)
}

func (r *openedRecorder) Len() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.urls)
}

func (r *openedRecorder) record(url string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.urls = append(r.urls, url)
}

func withFakeOpener(t *testing.T) *openedRecorder {
	t.Helper()
	recorder := &openedRecorder{}
	original := openURLFunc
	openURLFunc = func(_ context.Context, url string) error {
		recorder.record(url)
		return nil
	}
	t.Cleanup(func() { openURLFunc = original })
	return recorder
}

func TestMaybeOpenBrowser_OpenBrowserAction_Opens(t *testing.T) {
	opened := withFakeOpener(t)
	f := &forwarder{openedOnce: map[string]bool{}}
	f.maybeOpenBrowser(context.Background(), "3000", config2.PortAttribute{
		OnAutoForward: config2.AutoForwardOpenBrowser,
	})
	assert.Eventually(t, func() bool {
		return opened.Len() == 1
	}, time.Second, 10*time.Millisecond)
	assert.Equal(t, []string{"http://localhost:3000"}, opened.Snapshot())
}

func TestMaybeOpenBrowser_NotifyAction_DoesNotOpen(t *testing.T) {
	opened := withFakeOpener(t)
	f := &forwarder{openedOnce: map[string]bool{}}
	f.maybeOpenBrowser(context.Background(), "3000", config2.PortAttribute{
		OnAutoForward: config2.AutoForwardNotify,
	})
	time.Sleep(50 * time.Millisecond)
	assert.Empty(t, opened.Snapshot())
}

func TestMaybeOpenBrowser_SilentAction_DoesNotOpen(t *testing.T) {
	opened := withFakeOpener(t)
	f := &forwarder{openedOnce: map[string]bool{}}
	f.maybeOpenBrowser(context.Background(), "3000", config2.PortAttribute{
		OnAutoForward: config2.AutoForwardSilent,
	})
	time.Sleep(50 * time.Millisecond)
	assert.Empty(t, opened.Snapshot())
}

func TestMaybeOpenBrowser_OpenPreviewAction_Opens(t *testing.T) {
	opened := withFakeOpener(t)
	f := &forwarder{openedOnce: map[string]bool{}}
	f.maybeOpenBrowser(context.Background(), "8080", config2.PortAttribute{
		OnAutoForward: config2.AutoForwardOpenPreview,
	})
	assert.Eventually(t, func() bool {
		return opened.Len() == 1
	}, time.Second, 10*time.Millisecond)
	assert.Equal(t, []string{"http://localhost:8080"}, opened.Snapshot())
}

func TestMaybeOpenBrowser_OpenBrowserOnce_OpensOnlyFirstTime(t *testing.T) {
	opened := withFakeOpener(t)
	f := &forwarder{openedOnce: map[string]bool{}}
	attr := config2.PortAttribute{OnAutoForward: config2.AutoForwardOpenBrowserOnce}

	f.maybeOpenBrowser(context.Background(), "4000", attr)
	f.maybeOpenBrowser(context.Background(), "4000", attr)
	f.maybeOpenBrowser(context.Background(), "4000", attr)

	assert.Eventually(t, func() bool {
		return opened.Len() == 1
	}, time.Second, 10*time.Millisecond)
	assert.Equal(t, []string{"http://localhost:4000"}, opened.Snapshot())
}

func TestMaybeOpenBrowser_OpenBrowserOnce_OpensEachDistinctPortOnce(t *testing.T) {
	opened := withFakeOpener(t)
	f := &forwarder{openedOnce: map[string]bool{}}
	attr := config2.PortAttribute{OnAutoForward: config2.AutoForwardOpenBrowserOnce}

	f.maybeOpenBrowser(context.Background(), "4000", attr)
	f.maybeOpenBrowser(context.Background(), "4001", attr)

	assert.Eventually(t, func() bool {
		return opened.Len() == 2
	}, time.Second, 10*time.Millisecond)
	assert.ElementsMatch(
		t,
		[]string{"http://localhost:4000", "http://localhost:4001"},
		opened.Snapshot(),
	)
}

func TestMaybeOpenBrowser_NilOpenedOnceMap_DoesNotPanic(t *testing.T) {
	opened := withFakeOpener(t)
	f := &forwarder{}
	assert.NotPanics(t, func() {
		f.maybeOpenBrowser(context.Background(), "5000", config2.PortAttribute{
			OnAutoForward: config2.AutoForwardOpenBrowserOnce,
		})
	})
	// A late-running goroutine could still hold the old closure and leak
	// an append into whichever test's opened slice is live when it
	// finally executes, so wait for it to finish before t.Cleanup swaps
	// openURLFunc back.
	assert.Eventually(t, func() bool {
		return opened.Len() == 1
	}, time.Second, 10*time.Millisecond)
}
