package azbmetrics

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestInstrumentAzureBlobClientRecordsMetrics(t *testing.T) {
	client := InstrumentAzureBlobClient(fakeBlobClient{})
	ctx := context.Background()

	if err := client.StoreBlob(ctx, "container", "prefix/blob", []byte("abc")); err != nil {
		t.Fatal(err)
	}
	if _, err := client.LoadBlob(ctx, "container", "prefix/blob"); err != nil {
		t.Fatal(err)
	}

	waitForMetric(t, `azb_sqlite_wal_write_operations_total{operation="store",kind="write",result="ok"}`)
	waitForMetric(t, `azb_sqlite_wal_read_operations_total{operation="load",kind="read",result="ok"}`)
}

func TestMetricsHandler(t *testing.T) {
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/metrics", nil)

	serveMetrics(recorder, request)

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", recorder.Code, http.StatusOK)
	}
	if got := recorder.Header().Get("Content-Type"); !strings.Contains(got, "text/plain") {
		t.Fatalf("Content-Type = %q", got)
	}
	if body := recorder.Body.String(); !strings.Contains(body, "azb_sqlite_wal_read_operations_total") {
		t.Fatalf("metrics body missing read counter: %s", body)
	}
}

func TestStartServerExposesMetricsPort(t *testing.T) {
	if err := StartServer(); err != nil {
		t.Fatal(err)
	}

	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		resp, err := http.Get("http://127.0.0.1:9190/metrics")
		if err != nil {
			time.Sleep(time.Millisecond)
			continue
		}
		body, readErr := io.ReadAll(resp.Body)
		resp.Body.Close()
		if readErr != nil {
			t.Fatal(readErr)
		}
		if resp.StatusCode == http.StatusOK && strings.Contains(string(body), "azb_sqlite_wal_read_operations_total") {
			return
		}
		t.Fatalf("unexpected metrics response: status=%d body=%s", resp.StatusCode, body)
	}
	t.Fatal("metrics endpoint did not start on 127.0.0.1:9190")
}

func waitForMetric(t *testing.T, needle string) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if strings.Contains(Render(), needle) {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("metric %q not found in:\n%s", needle, Render())
}

type fakeBlobClient struct{}

func (fakeBlobClient) StoreBlob(context.Context, string, string, []byte) error { return nil }
func (fakeBlobClient) LoadBlob(context.Context, string, string) ([]byte, error) {
	return []byte("abc"), nil
}
func (fakeBlobClient) DeleteBlob(context.Context, string, string) error { return nil }
func (fakeBlobClient) ListBlobs(context.Context, string, string) ([]string, error) {
	return []string{"prefix/blob"}, nil
}
func (fakeBlobClient) URL() string { return "https://example.blob.core.windows.net/" }
