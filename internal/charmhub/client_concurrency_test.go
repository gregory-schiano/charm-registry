package charmhub

import (
	"context"
	"io"
	"sync"
	"testing"
)

// TestConcurrentDownloadToIsRaceFree guards against the data race that existed
// when CheckRedirect was assigned per-request on the shared http.Client. The
// sync worker downloads resource artifacts concurrently on one client, so this
// mirrors that pattern. It must pass under `go test -race`.
func TestConcurrentDownloadToIsRaceFree(t *testing.T) {
	t.Parallel()
	// baseURL host matches the download host so the allowlist permits it; the
	// connection is refused quickly (no network dependency).
	client := New("https://127.0.0.1:9")
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, _ = client.DownloadTo(context.Background(), "https://127.0.0.1:9/artifact.charm", io.Discard)
		}()
	}
	wg.Wait()
}
