package azbmetrics

import (
	"context"
	"log/slog"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/Hoverhuang-er/azbdb/pkg/kv"
)

const listenAddr = ":9190"

type opIndex int

const (
	opLoad opIndex = iota
	opList
	opStore
	opDelete
	opCount
)

var opNames = [...]string{"load", "list", "store", "delete"}
var opKinds = [...]string{"read", "read", "write", "write"}

type opMetrics struct {
	ok       uint64
	err      uint64
	bytes    uint64
	nanos    uint64
	inFlight int64
}

type eventKind uint8

const (
	eventBegin eventKind = iota
	eventFinish
)

type metricEvent struct {
	op     opIndex
	kind   eventKind
	bytes  uint64
	nanos  uint64
	failed bool
}

var collector = struct {
	once   sync.Once
	events chan metricEvent
	mu     sync.RWMutex
	ops    [opCount]opMetrics
}{
	events: make(chan metricEvent, 4096),
}

var server = struct {
	mu      sync.Mutex
	started bool
}{}

func StartServer() error {
	startCollector()

	server.mu.Lock()
	defer server.mu.Unlock()
	if server.started {
		return nil
	}
	listener, err := net.Listen("tcp", listenAddr)
	if err != nil {
		slog.Warn("azb metrics listener unavailable", slog.String("addr", listenAddr), slog.Any("error", err))
		return nil
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/metrics", serveMetrics)
	server.started = true
	go func() {
		if err := http.Serve(listener, mux); err != nil {
			slog.Warn("azb metrics server stopped", slog.Any("error", err))
		}
	}()
	return nil
}

func startCollector() {
	collector.once.Do(func() { go collectMetrics(collector.events) })
}

func InstrumentAzureBlobClient(client kv.AzureBlobClient) kv.AzureBlobClient {
	startCollector()
	return instrumentedAzureBlobClient{client: client}
}

type instrumentedAzureBlobClient struct {
	client kv.AzureBlobClient
}

func (c instrumentedAzureBlobClient) StoreBlob(ctx context.Context, containerName, blobName string, value []byte) error {
	start := beginOperation(opStore)
	err := c.client.StoreBlob(ctx, containerName, blobName, value)
	finishOperation(opStore, len(value), start, err)
	return err
}

func (c instrumentedAzureBlobClient) LoadBlob(ctx context.Context, containerName, blobName string) ([]byte, error) {
	start := beginOperation(opLoad)
	value, err := c.client.LoadBlob(ctx, containerName, blobName)
	finishOperation(opLoad, len(value), start, err)
	return value, err
}

func (c instrumentedAzureBlobClient) DeleteBlob(ctx context.Context, containerName, blobName string) error {
	start := beginOperation(opDelete)
	err := c.client.DeleteBlob(ctx, containerName, blobName)
	finishOperation(opDelete, 0, start, err)
	return err
}

func (c instrumentedAzureBlobClient) ListBlobs(ctx context.Context, containerName, prefix string) ([]string, error) {
	start := beginOperation(opList)
	values, err := c.client.ListBlobs(ctx, containerName, prefix)
	finishOperation(opList, 0, start, err)
	return values, err
}

func (c instrumentedAzureBlobClient) URL() string { return c.client.URL() }

func collectMetrics(events <-chan metricEvent) {
	for event := range events {
		collector.mu.Lock()
		m := &collector.ops[event.op]
		switch event.kind {
		case eventBegin:
			m.inFlight++
		case eventFinish:
			m.inFlight--
			m.bytes += event.bytes
			m.nanos += event.nanos
			if event.failed {
				m.err++
			} else {
				m.ok++
			}
		}
		collector.mu.Unlock()
	}
}

func beginOperation(op opIndex) time.Time {
	collector.events <- metricEvent{op: op, kind: eventBegin}
	return time.Now()
}

func finishOperation(op opIndex, bytes int, start time.Time, err error) {
	event := metricEvent{
		op:     op,
		kind:   eventFinish,
		nanos:  uint64(time.Since(start)),
		failed: err != nil,
	}
	if bytes > 0 {
		event.bytes = uint64(bytes)
	}
	collector.events <- event
}

func serveMetrics(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/metrics" {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "text/plain; version=0.0.4; charset=utf-8")
	_, _ = w.Write([]byte(Render()))
}

func Render() string {
	collector.mu.RLock()
	defer collector.mu.RUnlock()

	var b strings.Builder
	b.WriteString("# HELP azb_sqlite_wal_read_operations_total Azure Blob SQLite WAL read operations.\n")
	b.WriteString("# TYPE azb_sqlite_wal_read_operations_total counter\n")
	writeOperationCounters(&b, "azb_sqlite_wal_read_operations_total", "read")
	b.WriteString("# HELP azb_sqlite_wal_write_operations_total Azure Blob SQLite WAL write operations.\n")
	b.WriteString("# TYPE azb_sqlite_wal_write_operations_total counter\n")
	writeOperationCounters(&b, "azb_sqlite_wal_write_operations_total", "write")
	b.WriteString("# HELP azb_sqlite_wal_bytes_total Azure Blob SQLite WAL bytes transferred.\n")
	b.WriteString("# TYPE azb_sqlite_wal_bytes_total counter\n")
	for op := opIndex(0); op < opCount; op++ {
		writeSample(&b, "azb_sqlite_wal_bytes_total", op, "", collector.ops[op].bytes)
	}
	b.WriteString("# HELP azb_sqlite_wal_operation_seconds_total Total time spent in Azure Blob SQLite WAL operations.\n")
	b.WriteString("# TYPE azb_sqlite_wal_operation_seconds_total counter\n")
	for op := opIndex(0); op < opCount; op++ {
		writeFloatSample(&b, "azb_sqlite_wal_operation_seconds_total", op, float64(collector.ops[op].nanos)/float64(time.Second))
	}
	b.WriteString("# HELP azb_sqlite_wal_operations_in_flight Azure Blob SQLite WAL operations currently running.\n")
	b.WriteString("# TYPE azb_sqlite_wal_operations_in_flight gauge\n")
	for op := opIndex(0); op < opCount; op++ {
		writeSignedSample(&b, "azb_sqlite_wal_operations_in_flight", op, collector.ops[op].inFlight)
	}
	return b.String()
}

func writeOperationCounters(b *strings.Builder, metricName, kind string) {
	for op := opIndex(0); op < opCount; op++ {
		if opKinds[op] != kind {
			continue
		}
		writeSample(b, metricName, op, "ok", collector.ops[op].ok)
		writeSample(b, metricName, op, "error", collector.ops[op].err)
	}
}

func writeSample(b *strings.Builder, metricName string, op opIndex, result string, value uint64) {
	b.WriteString(metricName)
	writeLabels(b, op, result)
	b.WriteByte(' ')
	b.WriteString(strconv.FormatUint(value, 10))
	b.WriteByte('\n')
}

func writeSignedSample(b *strings.Builder, metricName string, op opIndex, value int64) {
	b.WriteString(metricName)
	writeLabels(b, op, "")
	b.WriteByte(' ')
	b.WriteString(strconv.FormatInt(value, 10))
	b.WriteByte('\n')
}

func writeFloatSample(b *strings.Builder, metricName string, op opIndex, value float64) {
	b.WriteString(metricName)
	writeLabels(b, op, "")
	b.WriteByte(' ')
	b.WriteString(strconv.FormatFloat(value, 'f', -1, 64))
	b.WriteByte('\n')
}

func writeLabels(b *strings.Builder, op opIndex, result string) {
	b.WriteString("{operation=\"")
	b.WriteString(opNames[op])
	b.WriteString("\",kind=\"")
	b.WriteString(opKinds[op])
	if result != "" {
		b.WriteString("\",result=\"")
		b.WriteString(result)
	}
	b.WriteString("\"}")
}
