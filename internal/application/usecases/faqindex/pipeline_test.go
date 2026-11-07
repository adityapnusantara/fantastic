package faqindex

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/adityapratamanusantara/fantastic/internal/application/ports/driven"
)

// The fakes for embedding.Embedder and driven.FAQIndexer live in
// testfakes_test.go, following the chat usecase's testfake convention.

func testUnits() []Unit {
	q1, a1 := "Berapa biaya top up?", "Top up gratis untuk semua channel."
	q2, a2 := "Bagaimana cara daftar?", "Unduh aplikasi lalu registrasi nomor HP."
	q3, a3 := "Artikel panjang", longTestAnswer(1000) // windowed unit
	return []Unit{
		{ID: "art-a_01", SourceURL: "https://www.linkaja.id/news/art-a", Question: q1, Answer: a1, Category: "FAQ LinkAja", ContentHash: UnitContentHash(q1, a1), ScrapedAt: "2026-11-06T09:00:00Z"},
		{ID: "art-a_02", SourceURL: "https://www.linkaja.id/news/art-a", Question: q2, Answer: a2, Category: "FAQ LinkAja", ContentHash: UnitContentHash(q2, a2), ScrapedAt: "2026-11-06T09:00:00Z"},
		{ID: "art-b_01", SourceURL: "https://www.linkaja.id/news/art-b", Question: q3, Answer: a3, Category: "Soft News", ContentHash: UnitContentHash(q3, a3), ScrapedAt: "2026-11-06T10:00:00Z"},
	}
}

// runTestPipeline runs a pipeline against fakes and returns the result
// plus the manifest path it wrote.
func runTestPipeline(t *testing.T, corpus *Corpus, emb *fakeEmbedder, store *fakeIndexer, opts IndexOptions) (*Result, string) {
	t.Helper()
	if opts.Model == "" {
		opts.Model = "test-model"
	}
	if opts.Dims == 0 {
		opts.Dims = 4
	}
	if opts.ManifestPath == "" {
		opts.ManifestPath = filepath.Join(t.TempDir(), "index-manifest.json")
	}
	p := NewPipeline(emb, store, 100)
	res, err := p.IndexCorpus(context.Background(), corpus, opts)
	if err != nil {
		t.Fatalf("IndexCorpus: %v", err)
	}
	return res, opts.ManifestPath
}

type rawManifest struct {
	SchemaVersion int    `json:"schema_version"`
	IndexedAt     string `json:"indexed_at"`
	Model         string `json:"model"`
	Dims          int    `json:"dims"`
	Documents     []struct {
		SourceURL   string `json:"source_url"`
		ContentHash string `json:"content_hash"`
		ChunkCount  int    `json:"chunk_count"`
		Chunks      []struct {
			ChunkIndex  int    `json:"chunk_index"`
			ContentHash string `json:"content_hash"`
		} `json:"chunks"`
	} `json:"documents"`
}

func readManifest(t *testing.T, path string) rawManifest {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read manifest: %v", err)
	}
	var m rawManifest
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatalf("parse manifest: %v", err)
	}
	return m
}

// TestPipelineEndToEnd: grouping, chunk rows, doc rows and the manifest
// agree with each other (spec 6.2) on a mixed corpus (2 units in one
// article + 1 windowed unit in another).
func TestPipelineEndToEnd(t *testing.T) {
	units := testUnits()
	corpus := &Corpus{SchemaVersion: 1, Units: units}
	emb := &fakeEmbedder{dims: 4}
	store := &fakeIndexer{}

	res, manifestPath := runTestPipeline(t, corpus, emb, store, IndexOptions{})
	if res.Units != 3 || res.Documents != 2 {
		t.Fatalf("result units/documents = %d/%d, want 3/2", res.Units, res.Documents)
	}
	if res.WindowedUnits != 1 {
		t.Fatalf("windowed units = %d, want 1", res.WindowedUnits)
	}
	if len(store.batches) != 1 {
		t.Fatalf("store must receive exactly one atomic batch, got %d", len(store.batches))
	}
	batch := store.batches[0]

	// Documents: one per article, hash = derived from unit hashes,
	// title = category, scraped_at = max unit timestamp.
	if len(batch.Documents) != 2 {
		t.Fatalf("batch documents = %d, want 2", len(batch.Documents))
	}
	docA := batch.Documents[0]
	docB := batch.Documents[1]
	if docA.SourceURL != units[0].SourceURL || docB.SourceURL != units[2].SourceURL {
		t.Fatalf("unexpected document urls: %s / %s", docA.SourceURL, docB.SourceURL)
	}
	if want := DocumentContentHash([]string{units[0].ContentHash, units[1].ContentHash}); docA.ContentHash != want {
		t.Errorf("article A content_hash = %s, want %s", docA.ContentHash, want)
	}
	if docA.Title != "FAQ LinkAja" || docB.Title != "Soft News" {
		t.Errorf("titles = %q / %q, want the article categories", docA.Title, docB.Title)
	}
	wantA := mustTime(t, "2026-11-06T09:00:00Z")
	wantB := mustTime(t, "2026-11-06T10:00:00Z")
	if !docA.ScrapedAt.Equal(wantA) || !docB.ScrapedAt.Equal(wantB) {
		t.Errorf("scraped_at = %v / %v, want the max unit timestamps", docA.ScrapedAt, docB.ScrapedAt)
	}

	// Chunks: 1 + 1 + N(windows), all mapped to their article doc,
	// per-article indices contiguous from 0.
	if len(batch.Chunks) < 4 {
		t.Fatalf("batch chunks = %d; the windowed unit must yield >= 2 chunks", len(batch.Chunks))
	}
	if batch.Chunks[0].DocContentHash != docA.ContentHash || batch.Chunks[1].DocContentHash != docA.ContentHash {
		t.Error("article A chunks must reference article A's document hash")
	}
	// per-article indices must be contiguous from 0 within each document
	perDoc := make(map[string]int, len(batch.Documents))
	for i, c := range batch.Chunks {
		if c.Index != perDoc[c.DocContentHash] {
			t.Errorf("chunk %d: per-article index = %d, want %d (contiguous within each document)", i, c.Index, perDoc[c.DocContentHash])
		}
		perDoc[c.DocContentHash]++
		if len(c.Embedding) != 4 {
			t.Errorf("chunk %d: embedding dims = %d, want 4", i, len(c.Embedding))
		}
	}
	byHash := make(map[string]driven.FaqChunkRow, len(batch.Chunks))
	for _, c := range batch.Chunks {
		byHash[c.ContentHash] = c
	}
	if _, ok := byHash[units[0].ContentHash]; !ok {
		t.Error("unit 1's chunk hash must equal the unit content_hash on the 1:1 path")
	}
	if _, ok := byHash[units[2].ContentHash]; ok {
		t.Error("the windowed unit's windows must NOT reuse the unit content_hash (documented deviation)")
	}

	// Manifest matches the batch (the integrity contract, spec 6.2).
	m := readManifest(t, manifestPath)
	if m.SchemaVersion != 1 || m.Model != "test-model" || m.Dims != 4 {
		t.Errorf("manifest header = v%d model=%s dims=%d, want v1 test-model 4", m.SchemaVersion, m.Model, m.Dims)
	}
	if _, err := time.Parse(time.RFC3339, m.IndexedAt); err != nil {
		t.Errorf("manifest indexed_at %q is not RFC3339: %v", m.IndexedAt, err)
	}
	if len(m.Documents) != 3 {
		t.Fatalf("manifest documents = %d, want 3 (one entry per corpus UNIT)", len(m.Documents))
	}
	for i, entry := range m.Documents {
		if entry.SourceURL != units[i].SourceURL || entry.ContentHash != units[i].ContentHash {
			t.Errorf("manifest doc %d = (%s, %s), want the corpus unit identity", i, entry.SourceURL, entry.ContentHash)
		}
		if len(entry.Chunks) != entry.ChunkCount {
			t.Errorf("manifest doc %d: chunk_count %d != len(chunks) %d", i, entry.ChunkCount, len(entry.Chunks))
		}
		for j, ch := range entry.Chunks {
			if ch.ChunkIndex != j {
				t.Errorf("manifest doc %d chunk %d: index %d, want per-unit 0-based %d", i, j, ch.ChunkIndex, j)
			}
			if _, ok := byHash[ch.ContentHash]; !ok {
				t.Errorf("manifest doc %d chunk %d hash %s not present in the stored batch", i, j, ch.ContentHash)
			}
		}
	}
	if m.Documents[0].ChunkCount != 1 || m.Documents[1].ChunkCount != 1 || m.Documents[2].ChunkCount < 2 {
		t.Errorf("chunk counts = %d/%d/%d, want 1/1/N>=2", m.Documents[0].ChunkCount, m.Documents[1].ChunkCount, m.Documents[2].ChunkCount)
	}
}

// TestPipelineEmbedBatching: batches stay at the configured size and
// every text is embedded exactly once.
func TestPipelineEmbedBatching(t *testing.T) {
	var units []Unit
	for i := 0; i < 25; i++ {
		q, a := "q"+unitID(i)+"?", "jawaban singkat "+strings.Repeat("x", i)
		units = append(units, Unit{ID: "art_" + unitID(i), SourceURL: "https://www.linkaja.id/news/art", Question: q, Answer: a, ContentHash: UnitContentHash(q, a)})
	}
	corpus := &Corpus{SchemaVersion: 1, Units: units}
	emb := &fakeEmbedder{dims: 4}
	store := &fakeIndexer{}

	p := NewPipeline(emb, store, 10)
	if _, err := p.IndexCorpus(context.Background(), corpus, IndexOptions{
		Model: "test-model", Dims: 4, ManifestPath: filepath.Join(t.TempDir(), "m.json"),
	}); err != nil {
		t.Fatalf("IndexCorpus: %v", err)
	}
	if len(emb.calls) != 3 || emb.calls[0] != 10 || emb.calls[1] != 10 || emb.calls[2] != 5 {
		t.Fatalf("embed batch sizes = %v, want [10 10 5]", emb.calls)
	}
	if got := len(store.batches[0].Chunks); got != 25 {
		t.Fatalf("stored chunks = %d, want 25", got)
	}
}

// TestPipelineEmbedErrorPropagates: an embedder failure fails the run
// before anything reaches the store.
func TestPipelineEmbedErrorPropagates(t *testing.T) {
	corpus := &Corpus{SchemaVersion: 1, Units: testUnits()[:1]}
	emb := &fakeEmbedder{dims: 4, err: errors.New("boom")}
	store := &fakeIndexer{}
	p := NewPipeline(emb, store, 100)
	if _, err := p.IndexCorpus(context.Background(), corpus, IndexOptions{Model: "m", Dims: 4, ManifestPath: filepath.Join(t.TempDir(), "m.json")}); err == nil {
		t.Fatal("expected the embedder error to propagate")
	}
	if len(store.batches) != 0 {
		t.Fatal("store must not receive a batch when embedding fails")
	}
}

// TestPipelineDimMismatch: a wrong-dimension embedding fails loudly
// (a vector column is a (model, dims) pair, design D2).
func TestPipelineDimMismatch(t *testing.T) {
	corpus := &Corpus{SchemaVersion: 1, Units: testUnits()[:1]}
	emb := &fakeEmbedder{dims: 8}
	store := &fakeIndexer{}
	p := NewPipeline(emb, store, 100)
	_, err := p.IndexCorpus(context.Background(), corpus, IndexOptions{Model: "m", Dims: 4, ManifestPath: filepath.Join(t.TempDir(), "m.json")})
	if err == nil || !strings.Contains(err.Error(), "returned 8 dims") {
		t.Fatalf("want a dims mismatch error, got %v", err)
	}
}

// TestLoadCorpusRejectsTamperedHash: the loader fails loudly when a
// stored content_hash does not match the 6.3 recomputation.
func TestLoadCorpusRejectsTamperedHash(t *testing.T) {
	units := testUnits()[:1]
	units[0].ContentHash = strings.Repeat("0", 64)
	path := writeTempCorpus(t, &Corpus{SchemaVersion: 1, Units: units})
	if _, err := LoadCorpus(path); err == nil || !strings.Contains(err.Error(), "content_hash mismatch") {
		t.Fatalf("want a hash mismatch error, got %v", err)
	}
}

// TestLoadCorpusAcceptsValid: a well-formed corpus loads unchanged.
func TestLoadCorpusAcceptsValid(t *testing.T) {
	path := writeTempCorpus(t, &Corpus{SchemaVersion: 1, Units: testUnits()})
	corpus, err := LoadCorpus(path)
	if err != nil {
		t.Fatalf("LoadCorpus: %v", err)
	}
	if len(corpus.Units) != 3 {
		t.Fatalf("units = %d, want 3", len(corpus.Units))
	}
}

// TestLoadCorpusRejectsSchemaVersion: unknown major versions are
// rejected (spec 6).
func TestLoadCorpusRejectsSchemaVersion(t *testing.T) {
	path := writeTempCorpus(t, &Corpus{SchemaVersion: 2, Units: testUnits()})
	if _, err := LoadCorpus(path); err == nil || !strings.Contains(err.Error(), "schema_version") {
		t.Fatalf("want a schema_version error, got %v", err)
	}
}

// helpers

func mustTime(t *testing.T, s string) time.Time {
	t.Helper()
	tv, err := time.Parse(time.RFC3339, s)
	if err != nil {
		t.Fatalf("parse %s: %v", s, err)
	}
	return tv
}

func unitID(i int) string {
	return string(rune('a'+i%26)) + string(rune('0'+i/10))
}

func writeTempCorpus(t *testing.T, c *Corpus) string {
	t.Helper()
	raw, err := json.Marshal(c)
	if err != nil {
		t.Fatalf("marshal corpus: %v", err)
	}
	path := filepath.Join(t.TempDir(), "linkaja-faq.json")
	if err := os.WriteFile(path, raw, 0o644); err != nil {
		t.Fatalf("write corpus: %v", err)
	}
	return path
}
