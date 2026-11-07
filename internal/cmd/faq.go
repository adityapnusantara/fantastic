package cmd

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/cloudwego/eino-ext/components/embedding/openai"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/urfave/cli/v3"

	"github.com/adityapratamanusantara/fantastic/config"
	"github.com/adityapratamanusantara/fantastic/internal/adapters/repositories/postgres"
	"github.com/adityapratamanusantara/fantastic/internal/application/usecases/faqindex"
	"github.com/adityapratamanusantara/fantastic/internal/application/usecases/faqscrape"
)

// minFaqRequestDelayMS is the politeness floor of inventory section 7
// rule 2 (design D6): >= 1.0 s between the start of consecutive requests.
// Config defaults to exactly this value; a flag may raise it, never lower
// it.
const minFaqRequestDelayMS = 1000

// faqCommand is the FAQ RAG command group: `faq scrape` (crawl the
// linkaja.id /news surface, emit the curated corpus) and `faq index`
// (chunk + embed + upsert into pgvector, emit the index manifest).
func faqCommand() *cli.Command {
	return &cli.Command{
		Name:  "faq",
		Usage: "FAQ RAG corpus tooling",
		Commands: []*cli.Command{
			faqScrapeCommand(),
			faqIndexCommand(),
		},
	}
}

// faqScrapeCommand crawls the FAQ surface and writes the curated corpus
// JSON (design D5/D6). Politeness is config-driven (faq.scrape.*): the
// flags below only override the cache location and the request delay, and
// the delay may never drop below the contract floor.
func faqScrapeCommand() *cli.Command {
	return &cli.Command{
		Name:   "scrape",
		Usage:  "crawl the linkaja.id /news FAQ surface and write the curated corpus JSON",
		Action: runFaqScrape,
		Flags: []cli.Flag{
			&cli.StringFlag{
				Name:  "cache-dir",
				Usage: "raw HTML cache directory (default: faq.scrape.cache_dir from config)",
			},
			&cli.IntFlag{
				Name:  "delay-ms",
				Usage: "minimum ms between request starts (default: faq.scrape.request_delay_ms from config; floor 1000 per the politeness contract)",
			},
			&cli.BoolFlag{
				Name:  "dry-run",
				Usage: "discover + extract + report, but do not write the corpus file",
			},
		},
	}
}

func runFaqScrape(ctx context.Context, cmd *cli.Command) error {
	log := newLogger()

	cfg, err := loadConfig(cmd, log)
	if err != nil {
		return err
	}

	cacheDir := cmd.String("cache-dir")
	if cacheDir == "" {
		cacheDir = cfg.FAQ.Scrape.CacheDir
	}
	delayMS := cfg.FAQ.Scrape.RequestDelayMS
	if cmd.Int("delay-ms") > 0 {
		delayMS = cmd.Int("delay-ms")
	}
	if delayMS < minFaqRequestDelayMS {
		return fmt.Errorf("faq scrape: request delay %dms is below the politeness contract floor of %dms (inventory section 7 rule 2)", delayMS, minFaqRequestDelayMS)
	}

	opts := faqscrape.Options{
		UserAgent:    cfg.FAQ.Scrape.UserAgent,
		RequestDelay: time.Duration(delayMS) * time.Millisecond,
		CacheDir:     cacheDir,
		AllowedHosts: cfg.FAQ.Scrape.AllowedHosts,
	}
	log.Info("faq scrape starting",
		slog.String("user_agent", opts.UserAgent),
		slog.Int("request_delay_ms", delayMS),
		slog.String("cache_dir", opts.CacheDir),
	)

	corpus, stats, err := faqscrape.Run(ctx, opts)
	if err != nil {
		return err
	}

	if cmd.Bool("dry-run") {
		log.Info("faq scrape dry run: corpus NOT written",
			slog.Int("units", stats.Units),
			slog.Int("articles_fetched", stats.ArticlesFetchedOK),
		)
		return nil
	}

	if err := faqscrape.WriteCorpus(cfg.FAQ.Corpus.Path, corpus); err != nil {
		return err
	}

	log.Info("faq scrape complete",
		slog.Int("pages", stats.Pages),
		slog.Int("articles_discovered", stats.ArticlesDiscovered),
		slog.Int("panduan_added", stats.PanduanAdded),
		slog.Int("articles_fetched_ok", stats.ArticlesFetchedOK),
		slog.Int("units", stats.Units),
		slog.Int("network_fetches", stats.NetworkFetches),
		slog.Int("cache_hits", stats.CacheHits),
		slog.String("corpus", cfg.FAQ.Corpus.Path),
		slog.String("scraped_at", corpus.ScrapedAt),
	)
	for _, a := range stats.Anomalies {
		log.Warn("scrape anomaly", slog.String("detail", a))
	}
	for _, f := range stats.Failures {
		log.Warn("fetch failure", slog.String("detail", f))
	}
	return nil
}

// faqIndexCommand chunks + embeds the curated corpus and upserts it into
// pgvector idempotently (by content_hash), emitting the index manifest.
// The corpus/manifest flags are overrides; the defaults come from the
// faq.corpus config section (design D5).
func faqIndexCommand() *cli.Command {
	return &cli.Command{
		Name:   "index",
		Usage:  "chunk, embed and index the FAQ corpus (idempotent upsert)",
		Action: runFaqIndex,
		Flags: []cli.Flag{
			&cli.StringFlag{
				Name:    flagDatabaseURL,
				Usage:   "database connection url (overrides config)",
				Sources: cli.EnvVars(envDatabaseURL),
			},
			&cli.StringFlag{
				Name:  "corpus",
				Usage: "path to the curated corpus JSON (default: faq.corpus.path from config)",
			},
			&cli.StringFlag{
				Name:  "manifest",
				Usage: "path to write the index manifest JSON (default: faq.corpus.manifest_path from config)",
			},
		},
	}
}

func runFaqIndex(ctx context.Context, cmd *cli.Command) error {
	log := newLogger()

	cfg, err := loadConfig(cmd, log)
	if err != nil {
		return err
	}
	url := databaseURL(cmd, cfg)

	apiKey, err := cfg.FAQEmbeddingAPIKey()
	if err != nil {
		return err
	}

	corpusPath := cmd.String("corpus")
	if corpusPath == "" {
		corpusPath = cfg.FAQ.Corpus.Path
	}
	manifestPath := cmd.String("manifest")
	if manifestPath == "" {
		manifestPath = cfg.FAQ.Corpus.ManifestPath
	}

	corpus, err := faqindex.LoadCorpus(corpusPath)
	if err != nil {
		return err
	}

	poolCfg, err := pgxpool.ParseConfig(url)
	if err != nil {
		return err
	}
	pool, err := pgxpool.NewWithConfig(ctx, poolCfg)
	if err != nil {
		return err
	}
	defer pool.Close()

	embedder, err := faqEmbedder(ctx, cfg, apiKey)
	if err != nil {
		return err
	}

	pipeline := faqindex.NewPipeline(embedder, postgres.NewFaqIndexStore(pool), 100)
	res, err := pipeline.IndexCorpus(ctx, corpus, faqindex.IndexOptions{
		Model:        cfg.FAQ.Embedding.Model,
		Dims:         cfg.FAQ.Embedding.Dims,
		ManifestPath: manifestPath,
	})
	if err != nil {
		return err
	}

	log.Info("faq index complete",
		slog.Int("units", res.Units),
		slog.Int("documents", res.Documents),
		slog.Int("chunks", res.Chunks),
		slog.Int("windowed_units", res.WindowedUnits),
		slog.Int("embed_calls", res.EmbedCalls),
		slog.String("model", cfg.FAQ.Embedding.Model),
		slog.Int("dims", cfg.FAQ.Embedding.Dims),
		slog.String("manifest", manifestPath),
	)
	return nil
}

// faqEmbedder builds the eino-ext OpenAI embedder from the faq.embedding
// config section (design D2). A non-empty base_url targets any
// OpenAI-compatible gateway; ByAzure stays false so custom base URLs are
// honored (eino-ext libs/acl/openai honors BaseURL for non-Azure).
func faqEmbedder(ctx context.Context, cfg *config.Config, apiKey string) (*openai.Embedder, error) {
	return openai.NewEmbedder(ctx, &openai.EmbeddingConfig{
		APIKey:  apiKey,
		BaseURL: cfg.FAQ.Embedding.BaseURL,
		Model:   cfg.FAQ.Embedding.Model,
		Timeout: 2 * time.Minute,
	})
}
