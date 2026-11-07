// Package faq provides search_faq, the chat agent's window into the FAQ
// vector store: a local eino tool (design D10 — the first local tool in
// the repo, appended at bootstrap beside the MCP tools; the MCP registry
// stays MCP-only).
package faq

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/cloudwego/eino/components/embedding"
	"github.com/cloudwego/eino/components/tool"
	toolutils "github.com/cloudwego/eino/components/tool/utils"

	"github.com/adityapratamanusantara/fantastic/internal/application/ports/driven"
)

// Name is the tool's wire name (what the model sees and calls).
const Name = "search_faq"

// Description tells the agent WHEN to call the tool and that answers must
// cite the returned source_url (spec "Citation-bearing FAQ answers").
const Description = "Search the official LinkAja FAQ knowledge base (help content from linkaja.id). " +
	"Call this tool whenever the user asks about LinkAja products, features, fees or limits, " +
	"how-to steps (top up, transfer, bill payment, activation), or account matters " +
	"(registration, PIN, security, KYC). It returns the most relevant FAQ snippets with " +
	"their source_url, title and similarity score. Every claim taken from the results MUST " +
	"cite the snippet's source_url. If the tool returns no results, say you do not have " +
	"that information in the FAQ knowledge base instead of guessing."

// PromptAddendum extends the chat system prompt while the search_faq tool
// is wired (design D9: answer in the user's language with Indonesian as
// the default, cite linkaja.id source URLs, never present ungrounded facts
// as FAQ-based answers). Appended by bootstrap next to the tool itself so
// the rules always match the tool's availability.
const PromptAddendum = `

FAQ answering rules (search_faq tool):
- Answer in the user's language; Indonesian by default.
- For any question about LinkAja (products, features, fees, how-tos, account matters), call search_faq BEFORE answering and answer from its results.
- When you answer from search_faq results, cite the linkaja.id source_url of the snippet(s) you used.
- Only cite source URLs returned by search_faq — never invent or alter links.
- If search_faq returns no relevant results, say that the FAQ knowledge base does not cover it; do not present your own knowledge as if it came from the FAQ.`

// searchInput is the tool's whole input schema: one string field.
type searchInput struct {
	Query string `json:"query" jsonschema:"description=The question to search the LinkAja FAQ for, in the user's own words"`
}

// hit is one retrieval result exposed to the model (design D7: chunk text
// + source URL + title + score so the agent can cite; content_hash keeps
// the eval pin visible end to end).
type hit struct {
	Text        string  `json:"text"`
	SourceURL   string  `json:"source_url"`
	Title       string  `json:"title"`
	Score       float64 `json:"score"`
	ContentHash string  `json:"content_hash"`
}

// NewSearchTool builds the local search_faq tool over the FAQSearcher port
// and the configured embedder. topK is the retrieval width (faq.search.
// top_k); it must be positive. The tool never panics: every downstream
// failure (embedder, store) is returned as an error the agent can see.
func NewSearchTool(searcher driven.FAQSearcher, embedder embedding.Embedder, topK int) (tool.BaseTool, error) {
	if searcher == nil {
		return nil, errors.New("faq tool: searcher is required")
	}
	if embedder == nil {
		return nil, errors.New("faq tool: embedder is required")
	}
	if topK <= 0 {
		return nil, fmt.Errorf("faq tool: top_k must be positive, got %d", topK)
	}

	fn := func(ctx context.Context, in searchInput) ([]hit, error) {
		return search(ctx, searcher, embedder, topK, in.Query)
	}
	return toolutils.InferTool(Name, Description, fn)
}

// search embeds the question and pulls the top-k chunks. Zero hits return
// an explicit empty array (not an error): the grounded-fallback behavior
// is the system prompt's job (spec "Grounded fallback when FAQ content is
// absent").
func search(ctx context.Context, searcher driven.FAQSearcher, embedder embedding.Embedder, topK int, query string) ([]hit, error) {
	if strings.TrimSpace(query) == "" {
		return nil, errors.New("search_faq: query is required")
	}

	vecs, err := embedder.EmbedStrings(ctx, []string{query})
	if err != nil {
		return nil, fmt.Errorf("search_faq: embed query: %w", err)
	}
	if len(vecs) != 1 || len(vecs[0]) == 0 {
		return nil, fmt.Errorf("search_faq: embedder returned %d vectors (want 1)", len(vecs))
	}
	queryEmbedding := make([]float32, len(vecs[0]))
	for i, v := range vecs[0] {
		queryEmbedding[i] = float32(v)
	}

	faqHits, err := searcher.Search(ctx, queryEmbedding, topK)
	if err != nil {
		return nil, fmt.Errorf("search_faq: %w", err)
	}

	// Explicit empty result when nothing matches.
	hits := make([]hit, 0, len(faqHits))
	for _, h := range faqHits {
		hits = append(hits, hit{
			Text:        h.ChunkText,
			SourceURL:   h.SourceURL,
			Title:       h.Title,
			Score:       h.Score,
			ContentHash: h.ContentHash,
		})
	}
	return hits, nil
}
