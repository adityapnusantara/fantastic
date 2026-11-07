# Spec Delta

## Purpose

Lets the chat agent answer LinkAja FAQ questions from the official public FAQ content of linkaja.id with source citations, instead of from ungrounded model priors, with retrieval quality proven against a frozen golden set before the ingestion path is considered stable.

## ADDED Requirements

### Requirement: Robots-compliant FAQ scraping
The system SHALL fetch only linkaja.id paths permitted by the recorded robots.txt verdict, apply the configured politeness rules (identifiable user agent, minimum request interval), and cache raw responses locally. When a needed path is disallowed, the system SHALL surface the restriction for an owner decision instead of fetching it.

#### Scenario: Allowed page fetched politely
- **WHEN** the scraper runs against a page permitted by the robots.txt verdict
- **THEN** requests carry the configured user agent, honor the minimum request interval, and the raw response is cached locally

#### Scenario: Disallowed path is escalated, not bypassed
- **WHEN** a candidate FAQ path is disallowed by the robots.txt verdict
- **THEN** the scraper skips it and the restriction is reported for an owner decision

### Requirement: Curated FAQ corpus with attribution
The system SHALL produce a JSON corpus of FAQ question-and-answer units in which each unit carries its source URL, a stable content hash, and a language tag. Re-scrapes SHALL be deduplicated by content hash. The corpus SHALL be a committed artifact so evaluations are reproducible, while the raw HTML cache remains machine-local.

#### Scenario: Corpus unit carries attribution
- **WHEN** the corpus is generated from the FAQ surface
- **THEN** every Q&A unit includes its source URL, content hash, and language tag

#### Scenario: Re-scrape deduplicates
- **WHEN** a re-scrape produces a unit whose content hash already exists
- **THEN** the corpus keeps one copy of that unit

### Requirement: Idempotent vector indexing
The system SHALL chunk the corpus (one chunk per Q&A unit; bounded windows only for long-form pages), embed each chunk with the configured embedding model, and store the embeddings in the vector store keyed by content hash so re-indexing replaces existing entries without duplicating them. The indexing step SHALL emit a manifest of per-document chunk counts and chunk content hashes as the integrity reference for verification.

#### Scenario: First indexing run
- **WHEN** the corpus is indexed against an empty vector store
- **THEN** every corpus unit yields one stored chunk with its embedding, and the manifest matches the stored chunk set

#### Scenario: Re-index replaces instead of duplicating
- **WHEN** indexing runs again over unchanged corpus content
- **THEN** chunk counts stay identical and no duplicate chunks are created

### Requirement: Citation-bearing FAQ answers in chat
The chat agent SHALL have a FAQ search tool available that returns the top matching FAQ chunks with their source URLs and scores. For questions answered from FAQ content, the agent's answer SHALL cite the source URL of the chunk it used, and the tool call SHALL be observable in the chat stream as a tool result event. Answers SHALL be in the user's language (Indonesian by default).

#### Scenario: FAQ question answered with a citation
- **WHEN** a user asks a question covered by the FAQ corpus in the chat
- **THEN** the agent invokes the FAQ search tool, the stream shows the tool result event, and the answer cites the matching source URL

#### Scenario: Answer cites only retrieved sources
- **WHEN** the agent answers from FAQ content
- **THEN** every cited URL is a source URL returned by the FAQ search tool, not an invented link

### Requirement: Grounded fallback when FAQ content is absent
When the FAQ search tool returns no relevant chunk for a question, the agent SHALL state that it lacks the information rather than presenting ungrounded facts as if they came from the FAQ.

#### Scenario: Out-of-corpus question
- **WHEN** a user asks a question with no relevant FAQ chunk in the top results
- **THEN** the agent says it cannot answer from the FAQ knowledge base instead of fabricating an answer

### Requirement: Configuration-driven FAQ settings
The embedding model, embedding endpoint, embedding credential, retrieval top-k, scraping politeness limits, and cache locations SHALL be read from the FAQ configuration section. Credentials SHALL be resolved from environment variables so that no secret is written to the configuration file.

#### Scenario: Credential resolved from environment
- **WHEN** the FAQ configuration references an environment variable for the embedding credential
- **THEN** the resolved requests carry the environment value and the configuration file contains no secret

#### Scenario: Retrieval honors configured top-k
- **WHEN** the FAQ search tool runs with top-k configured as N
- **THEN** it returns at most N chunks

### Requirement: Retrieval quality gate
The capability SHALL be evaluated against a frozen, owner-reviewed golden set of questions scored by the chunk-level matching rule, producing an evaluation report with per-question results, hit@k, and MRR. The productionized ingestion commands SHALL be considered stable only after that report meets the agreed quality bar; on a NO-GO verdict, productionization work SHALL NOT proceed.

#### Scenario: Evaluation report is produced
- **WHEN** the evaluation runs against the frozen golden set
- **THEN** the report records per-question pass/fail, hit@k, and MRR, and states the chunk-level matching rule verbatim

#### Scenario: NO-GO halts productionization
- **WHEN** the evaluation report does not meet the agreed bar
- **THEN** the ingestion CLI commands are not declared stable and no productionization tasks start
