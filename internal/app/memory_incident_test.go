package app

import (
	"compress/gzip"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

func TestLargeStoreVisitorAndStreamingCompaction(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "sample.snapshot.json.gz")
	st, err := OpenStore(path)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	for i := 0; i < 1500; i++ {
		c := Candidate{Host: fmt.Sprintf("8.%d.%d.%d", (i/10000)%256, (i/250)%256, (i%250)+1),
			Port: 1000 + i, Protocol: "http", Source: "test"}
		st.UpsertCandidate(c, now)
	}
	total := 0
	st.VisitRecords(func(p *ProxyRecord) {
		total++
		if p.Key == "" {
			t.Error("empty key")
		}
	})
	if total != 1500 {
		t.Fatalf("visit scanned %d, want 1500", total)
	}
	if err := st.Compact(); err != nil {
		t.Fatal(err)
	}
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	gz, err := gzip.NewReader(f)
	if err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	f.Close()
	if err := st.Close(); err != nil {
		t.Fatal(err)
	}
	restored, err := OpenStore(path)
	if err != nil {
		t.Fatal(err)
	}
	defer restored.Close()
	if restored.Count() != 1500 {
		t.Fatalf("compact/load lost entries: %d", restored.Count())
	}
}

func TestConcurrentCompactAndUpdatesKeepAllRecords(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "sample.snapshot.json.gz")
	st, err := OpenStore(path)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	for i := 0; i < 1500; i++ {
		st.UpsertCandidate(Candidate{Host: fmt.Sprintf("4.0.%d.%d", i/250, (i%250)+1),
			Port: 8000 + i, Protocol: "http", Source: "alpha"}, now)
	}
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < 300; i++ {
			st.UpsertCandidate(Candidate{Host: fmt.Sprintf("4.1.%d.%d", i/250, (i%250)+1),
				Port: 9000 + i, Protocol: "socks5", Source: "beta"}, now)
		}
	}()
	if err := st.Compact(); err != nil {
		t.Fatal(err)
	}
	wg.Wait()
	if err := st.Close(); err != nil {
		t.Fatal(err)
	}
	reread, err := OpenStore(path)
	if err != nil {
		t.Fatal(err)
	}
	if reread.Count() != 1800 {
		t.Errorf("concurrent compaction lost records: %d/1800", reread.Count())
	}
	reread.Close()
}
