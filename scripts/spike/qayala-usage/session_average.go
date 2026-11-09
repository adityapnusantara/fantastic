package main

import (
	"context"
	"fmt"
	"path/filepath"
	"sync"
	"time"

	"github.com/adityapratamanusantara/fantastic/internal/application/ports/driven"
	"github.com/adityapratamanusantara/fantastic/internal/domain"
	"github.com/google/uuid"
)

// The benchmark adapter applies the intended ten-Q/A memory boundary even
// when a turn contains several tool calls. It does not change production code.
type completeTurnStore struct {
	driven.ConversationStore
	turns int
}

func recentUserTurns(messages []domain.Message, limit int) []domain.Message {
	if limit <= 0 {
		return nil
	}
	seen := 0
	for i := len(messages) - 1; i >= 0; i-- {
		if messages[i].Role == domain.RoleUser {
			seen++
			if seen == limit {
				return messages[i:]
			}
		}
	}
	return messages
}

func (s completeTurnStore) LoadWindow(ctx context.Context, id uuid.UUID, userID string, _ int) (*domain.Conversation, []domain.Message, error) {
	conv, err := s.GetConversation(ctx, id, userID)
	if err != nil {
		return nil, nil, err
	}
	messages, err := s.GetMessages(ctx, id, userID)
	if err != nil {
		return nil, nil, err
	}
	return conv, recentUserTurns(messages, s.turns), nil
}

func runSessionAverage(parent context.Context, r *runner, fx fixture, models []string, repeats int, outDir, database string) error {
	ctx, cancel := context.WithCancel(parent)
	defer cancel()
	meta := map[string]any{
		"generated_at": time.Now().UTC(), "source_url": fx.SourceURL, "source_version": fx.Version,
		"models": models, "efforts": []string{"low", "medium"}, "repeats": repeats,
		"session_lengths": []int{1, 3, 5, 15}, "session_weights": []float64{.25, .25, .25, .25},
		"session_question_sequences": fx.Sessions[:repeats], "history_qa_turns": 10,
		"memory_adapter": "benchmark-only completeTurnStore; preserves ten whole Q/A turns including tools",
		"empty_start":    true, "prefix_reuse": true, "parallel_tool_calls_disabled": true,
		"caching": "disabled", "max_output_tokens": r.cfg.LLM.MaxTokens, "top_k": r.cfg.FAQ.Search.TopK,
		"instruction_tokens_measured_approx": 210, "instruction_tokens_planned": 520,
		"database_isolation_verified": true, "database_name": database,
		"indexing_requests": r.rec.forCase("indexing"),
	}
	if err := writeJSON(filepath.Join(outDir, "session-metadata.json"), meta); err != nil {
		return err
	}
	var mu sync.Mutex
	var results []caseResult
	var firstError error
	var wg sync.WaitGroup
	for _, modelName := range models {
		for _, effort := range []string{"low", "medium"} {
			wg.Add(1)
			go func(modelName, effort string) {
				defer wg.Done()
				local := *r
				local.store = completeTurnStore{ConversationStore: r.store, turns: 10}
				fail := func(err error) {
					mu.Lock()
					if firstError == nil {
						firstError = err
						cancel()
					}
					mu.Unlock()
				}
				for repeat := 1; repeat <= repeats; repeat++ {
					conv := domain.NewConversation("qayala-session-benchmark", agentID)
					if err := local.store.CreateConversation(ctx, conv); err != nil {
						fail(err)
						return
					}
					svc, err := local.service(ctx, modelName, effort)
					if err != nil {
						fail(err)
						return
					}
					for turn, question := range fx.Sessions[repeat-1] {
						id := fmt.Sprintf("%s-%s-session%d-turn%d", modelName, effort, repeat, turn+1)
						turnCtx := context.WithValue(ctx, caseKey{}, id)
						res, err := local.turn(turnCtx, svc, conv, question)
						res.Model, res.Effort, res.Repeat, res.SessionTurn = modelName, effort, repeat, turn+1
						if err != nil {
							res.Error = err.Error()
						}
						mu.Lock()
						results = append(results, res)
						writeErr := writeJSON(filepath.Join(outDir, "session-results.json"), results)
						mu.Unlock()
						fmt.Printf("%s: history=%d user/%d final answers, tools=%d, requests=%d, summary=%d->%d, error=%q\n", id, res.HistoryRoles["user"], res.HistoryAnswers, res.ToolResults, len(res.Requests), res.BeforeSummary, res.AfterSummary, res.Error)
						if err != nil || writeErr != nil {
							if err == nil {
								err = writeErr
							}
							fail(fmt.Errorf("session case %s: %w", id, err))
							return
						}
					}
				}
			}(modelName, effort)
		}
	}
	wg.Wait()
	if firstError != nil {
		return firstError
	}
	meta["completed_at"] = time.Now().UTC()
	meta["measured_turns"] = len(results)
	return writeJSON(filepath.Join(outDir, "session-metadata.json"), meta)
}
