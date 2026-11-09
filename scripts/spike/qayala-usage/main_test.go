package main

import (
	"testing"

	"github.com/adityapratamanusantara/fantastic/internal/domain"
	"github.com/jackc/pgx/v5"
)

func TestRecentUserTurnsPreservesWholeTurnsWithDifferentToolCounts(t *testing.T) {
	roles := []domain.Role{
		domain.RoleUser, domain.RoleAssistant, domain.RoleTool, domain.RoleAssistant,
		domain.RoleUser, domain.RoleAssistant, domain.RoleTool, domain.RoleAssistant, domain.RoleTool, domain.RoleAssistant,
		domain.RoleUser, domain.RoleAssistant,
	}
	var messages []domain.Message
	for i, role := range roles {
		messages = append(messages, domain.Message{Seq: int64(i + 1), Role: role})
	}
	recent := recentUserTurns(messages, 2)
	if len(recent) != 8 || recent[0].Seq != 5 || recent[0].Role != domain.RoleUser {
		t.Fatalf("incomplete turn boundary: %#v", recent)
	}
	if len(recentUserTurns(messages, 10)) != len(messages) {
		t.Fatal("short history lost messages")
	}
	if len(recentUserTurns(nil, 10)) != 0 {
		t.Fatal("empty history should remain empty")
	}
}

func TestIsolatedDatabaseURLTargetsBenchmarkDatabase(t *testing.T) {
	const original = "postgres://bench:dummy@localhost:5555/application?sslmode=disable"
	const target = "qayala_bench_abc123"
	result, err := isolatedDatabaseURL(original, target)
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := pgx.ParseConfig(result)
	if err != nil {
		t.Fatal(err)
	}
	if parsed.Database != target {
		t.Fatalf("connection resolves to %q, want isolated database %q", parsed.Database, target)
	}
	if parsed.Host != "localhost" || parsed.Port != 5555 || parsed.User != "bench" || parsed.Password != "dummy" {
		t.Fatal("database replacement changed another connection setting")
	}
}

func TestHistoryFingerprintChecksContentAndSummaryButIgnoresCloneIDs(t *testing.T) {
	m := domain.NewMessage(domain.NewConversation("user", "agent").ID, domain.RoleUser, "Apakah Qayala bisa offline?")
	clone := domain.NewMessage(domain.NewConversation("other", "agent").ID, m.Role, m.Content)
	first := historyFingerprint("Ringkasan", []domain.Message{*m})
	if first != historyFingerprint("Ringkasan", []domain.Message{*clone}) {
		t.Fatal("clone identity changed the history fingerprint")
	}
	clone.Content = "Pertanyaan berbeda"
	if first == historyFingerprint("Ringkasan", []domain.Message{*clone}) {
		t.Fatal("different history content has the same fingerprint")
	}
	if first == historyFingerprint("Ringkasan berbeda", []domain.Message{*m}) {
		t.Fatal("different summary has the same fingerprint")
	}
}

func TestIsolatedDatabaseURLRejectsDatabaseOverrides(t *testing.T) {
	for _, raw := range []string{
		"postgres://localhost/application?dbname=application",
		"postgres://localhost/application?database=application",
		"https://localhost/application",
	} {
		if _, err := isolatedDatabaseURL(raw, "qayala_bench_abc123"); err == nil {
			t.Fatal("accepted a connection URL that could override the isolated target")
		}
	}
	if _, err := isolatedDatabaseURL("postgres://localhost/application", "application"); err == nil {
		t.Fatal("accepted an application database as a benchmark target")
	}
}
