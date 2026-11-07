package postgres

import (
	"context"
	"log/slog"
	"os"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/adityapratamanusantara/fantastic/internal/application/ports/driven"
	"github.com/adityapratamanusantara/fantastic/internal/domain"
	"github.com/adityapratamanusantara/fantastic/migrations"
)

const (
	alice = "alice"
	bob   = "bob"
)

var testStore driven.ConversationStore

func TestMain(m *testing.M) {
	url := os.Getenv("TEST_DATABASE_URL")
	if url == "" {
		url = "postgres://fantastic:fantastic-dev@localhost:5555/fantastic_test"
	}

	if err := migrations.Up(url, slog.Default()); err != nil {
		panic("test migrations: " + err.Error())
	}

	pool, err := pgxpool.New(context.Background(), url)
	if err != nil {
		panic(err)
	}
	testStore = NewStore(pool)

	code := m.Run()
	pool.Close()
	os.Exit(code)
}

// truncate clears all rows between tests.
func truncate(t *testing.T) {
	t.Helper()
	_, err := testPool().Exec(context.Background(), `TRUNCATE messages, conversations CASCADE`)
	require.NoError(t, err)
}

func testPool() *pgxpool.Pool {
	return poolInstance
}

var poolInstance = func() *pgxpool.Pool {
	url := os.Getenv("TEST_DATABASE_URL")
	if url == "" {
		url = "postgres://fantastic:fantastic-dev@localhost:5555/fantastic_test"
	}
	pool, err := pgxpool.New(context.Background(), url)
	if err != nil {
		panic(err)
	}
	return pool
}()

func newConvFor(t *testing.T, userID string) *domain.Conversation {
	t.Helper()
	return newConvForAgent(t, userID, "general")
}

// newConvForAgent inserts a conversation bound to the given agent.
func newConvForAgent(t *testing.T, userID, agentID string) *domain.Conversation {
	t.Helper()
	conv := domain.NewConversation(userID, agentID)
	require.NoError(t, testStore.CreateConversation(context.Background(), conv))
	return conv
}

func TestGetConversationOwnership(t *testing.T) {
	truncate(t)
	conv := newConvFor(t, alice)

	got, err := testStore.GetConversation(context.Background(), conv.ID, alice)
	require.NoError(t, err)
	assert.Equal(t, conv.ID, got.ID)
	assert.Equal(t, alice, got.UserID)

	_, err = testStore.GetConversation(context.Background(), conv.ID, bob)
	assert.ErrorIs(t, err, domain.ErrNotFound)

	_, err = testStore.GetConversation(context.Background(), uuid.New(), alice)
	assert.ErrorIs(t, err, domain.ErrNotFound)
}

func TestAppendMessageAssignsSeqAndIdempotent(t *testing.T) {
	truncate(t)
	conv := newConvFor(t, alice)
	ctx := context.Background()

	m1 := domain.NewMessage(conv.ID, domain.RoleUser, "hello")
	m2 := domain.NewMessage(conv.ID, domain.RoleAssistant, "hi there")

	seq1, err := testStore.AppendMessage(ctx, conv.ID, alice, m1, nil)
	require.NoError(t, err)
	seq2, err := testStore.AppendMessage(ctx, conv.ID, alice, m2, nil)
	require.NoError(t, err)
	assert.Equal(t, int64(1), seq1)
	assert.Equal(t, int64(2), seq2)
	assert.Equal(t, int64(1), m1.Seq)

	// duplicate id is ignored, no new row, no seq consumed
	seqDup, err := testStore.AppendMessage(ctx, conv.ID, alice, m1, nil)
	require.NoError(t, err)
	assert.Equal(t, int64(3), seqDup) // next seq is still 3

	msgs, err := testStore.GetMessages(ctx, conv.ID, alice)
	require.NoError(t, err)
	assert.Len(t, msgs, 2)
	assert.Equal(t, domain.RoleUser, msgs[0].Role)
	assert.Equal(t, "hello", msgs[0].Content)
}

func TestAppendMessagesBatchConsecutiveSeq(t *testing.T) {
	truncate(t)
	conv := newConvFor(t, alice)
	ctx := context.Background()

	m1 := domain.NewMessage(conv.ID, domain.RoleUser, "q")
	m2 := domain.NewMessage(conv.ID, domain.RoleAssistant, "a with tool")
	m2.ToolCalls = []domain.ToolCall{{ID: "call-1", Name: "list_posts", Arguments: `{"account":"x"}`}}
	m3 := domain.NewMessage(conv.ID, domain.RoleTool, `[{"id":"p1"}]`)
	m3.ToolCallID = "call-1"

	seqs, err := testStore.AppendMessages(ctx, conv.ID, alice, []*domain.Message{m1, m2, m3}, nil)
	require.NoError(t, err)
	assert.Equal(t, []int64{1, 2, 3}, seqs)

	msgs, err := testStore.GetMessages(ctx, conv.ID, alice)
	require.NoError(t, err)
	require.Len(t, msgs, 3)
	require.Len(t, msgs[1].ToolCalls, 1)
	assert.Equal(t, "list_posts", msgs[1].ToolCalls[0].Name)
	assert.Equal(t, "call-1", msgs[2].ToolCallID)
}

func TestOwnershipRequiredOnWrites(t *testing.T) {
	truncate(t)
	conv := newConvFor(t, alice)
	ctx := context.Background()
	msg := domain.NewMessage(conv.ID, domain.RoleUser, "secret")

	_, err := testStore.AppendMessage(ctx, conv.ID, bob, msg, nil)
	assert.ErrorIs(t, err, domain.ErrNotFound)

	msgs, err := testStore.GetMessages(ctx, conv.ID, alice)
	require.NoError(t, err)
	assert.Empty(t, msgs, "foreign user must not inject messages")
}

func TestLoadWindow(t *testing.T) {
	truncate(t)
	conv := newConvFor(t, alice)
	ctx := context.Background()

	for range 7 {
		msg := domain.NewMessage(conv.ID, domain.RoleUser, "m")
		_, err := testStore.AppendMessage(ctx, conv.ID, alice, msg, nil)
		require.NoError(t, err)
	}

	_, got, err := testStore.LoadWindow(ctx, conv.ID, alice, 3)
	require.NoError(t, err)
	require.Len(t, got, 3)
	assert.Equal(t, int64(5), got[0].Seq)
	assert.Equal(t, int64(7), got[2].Seq)

	// ownership
	_, _, err = testStore.LoadWindow(ctx, conv.ID, bob, 3)
	assert.ErrorIs(t, err, domain.ErrNotFound)
}

func TestSaveSummary(t *testing.T) {
	truncate(t)
	conv := newConvFor(t, alice)
	ctx := context.Background()

	require.NoError(t, testStore.SaveSummary(ctx, conv.ID, alice, "earlier chat summary", 5))
	got, err := testStore.GetConversation(ctx, conv.ID, alice)
	require.NoError(t, err)
	assert.Equal(t, "earlier chat summary", got.RollingSummary)
	assert.Equal(t, int64(5), got.SummaryUntilSeq)

	err = testStore.SaveSummary(ctx, conv.ID, bob, "hacked", 99)
	assert.ErrorIs(t, err, domain.ErrNotFound)
}

func TestTitleSetOnce(t *testing.T) {
	truncate(t)
	conv := newConvFor(t, alice)
	ctx := context.Background()

	m1 := domain.NewMessage(conv.ID, domain.RoleUser, "what is the first question I asked about postgres?")
	title := "what is the first question I asked about postgres?"
	_, err := testStore.AppendMessage(ctx, conv.ID, alice, m1, &title)
	require.NoError(t, err)

	got, err := testStore.GetConversation(ctx, conv.ID, alice)
	require.NoError(t, err)
	assert.Equal(t, title, got.Title)

	// second append without title pointer keeps the existing title
	m2 := domain.NewMessage(conv.ID, domain.RoleAssistant, "answer")
	_, err = testStore.AppendMessage(ctx, conv.ID, alice, m2, nil)
	require.NoError(t, err)

	got, err = testStore.GetConversation(ctx, conv.ID, alice)
	require.NoError(t, err)
	assert.Equal(t, title, got.Title)
}

func TestListConversationsNewestFirst(t *testing.T) {
	truncate(t)
	ctx := context.Background()

	first := newConvFor(t, alice)
	second := newConvFor(t, alice)
	newConvFor(t, bob) // other user's conversation

	// bump first's activity so it becomes the most recent
	msg := domain.NewMessage(first.ID, domain.RoleUser, "hi")
	_, err := testStore.AppendMessage(ctx, first.ID, alice, msg, nil)
	require.NoError(t, err)

	got, err := testStore.ListConversations(ctx, alice, nil)
	require.NoError(t, err)
	require.Len(t, got, 2)
	assert.Equal(t, first.ID, got[0].ID, "conversation with latest activity comes first")
	assert.Equal(t, second.ID, got[1].ID)
}

func TestListConversationsAgentFilter(t *testing.T) {
	truncate(t)
	ctx := context.Background()

	// the filter target must be a real agents row (FK): create one, then
	// remove it with the test's conversation rows — the bound conversations
	// must go first, or the agent delete is FK-blocked and the row leaks
	social := &domain.Agent{ID: "zz-agent-test-filter", DisplayName: "filter target"}
	_, err := NewAgentStore(testPool()).CreateIfAbsent(ctx, social)
	require.NoError(t, err)
	t.Cleanup(func() {
		_, _ = testPool().Exec(context.Background(), `TRUNCATE messages, conversations CASCADE`)
		_ = NewAgentStore(testPool()).Delete(ctx, social.ID)
	})

	newConvFor(t, alice) // default agent
	socialConv := newConvForAgent(t, alice, social.ID)
	newConvFor(t, bob)

	got, err := testStore.ListConversations(ctx, alice, nil)
	require.NoError(t, err)
	require.Len(t, got, 2, "no filter returns every conversation")

	got, err = testStore.ListConversations(ctx, alice, strPtr(social.ID))
	require.NoError(t, err)
	require.Len(t, got, 1, "the filter keeps only the bound agent's conversations")
	assert.Equal(t, socialConv.ID, got[0].ID)

	got, err = testStore.ListConversations(ctx, alice, strPtr("nonexistent"))
	require.NoError(t, err)
	assert.Empty(t, got, "an unknown agent matches nothing")
}

func TestDeleteConversationCascade(t *testing.T) {
	truncate(t)
	conv := newConvFor(t, alice)
	ctx := context.Background()

	msg := domain.NewMessage(conv.ID, domain.RoleUser, "to be deleted")
	_, err := testStore.AppendMessage(ctx, conv.ID, alice, msg, nil)
	require.NoError(t, err)

	assert.ErrorIs(t, testStore.DeleteConversation(ctx, conv.ID, bob), domain.ErrNotFound)

	require.NoError(t, testStore.DeleteConversation(ctx, conv.ID, alice))

	_, err = testStore.GetConversation(ctx, conv.ID, alice)
	assert.ErrorIs(t, err, domain.ErrNotFound)

	msgs, err := testStore.GetMessages(ctx, conv.ID, alice)
	require.NoError(t, err)
	assert.Empty(t, msgs, "messages must cascade with the conversation")
}

// TestCountByAgent exercises the agent-admin delete-protection query on
// the real database. Like agent_store_test.go it creates its own unique
// zz- agent rows for the FK and cleans them up afterwards, so it never
// disturbs the seeded 'general' agent or the other tests.
func TestCountByAgent(t *testing.T) {
	ctx := context.Background()

	agentID := "zz-agent-test-count"
	otherID := "zz-agent-test-count-other"
	for _, id := range []string{agentID, otherID} {
		_, err := NewAgentStore(testPool()).CreateIfAbsent(ctx, &domain.Agent{ID: id, DisplayName: "count fixture"})
		require.NoError(t, err)
		cleanupAgent(t, id)
	}

	// start each fixture agent from zero conversations (a crashed earlier
	// run must not skew the counts)
	_, err := testPool().Exec(ctx, `DELETE FROM conversations WHERE agent_id IN ($1, $2)`, agentID, otherID)
	require.NoError(t, err)
	t.Cleanup(func() {
		_, err := testPool().Exec(context.Background(), `DELETE FROM conversations WHERE agent_id IN ($1, $2)`, agentID, otherID)
		require.NoError(t, err)
	})

	n, err := testStore.CountByAgent(ctx, agentID)
	require.NoError(t, err)
	assert.Zero(t, n, "an agent without conversations counts zero")

	require.NoError(t, testStore.CreateConversation(ctx, domain.NewConversation(alice, agentID)))
	require.NoError(t, testStore.CreateConversation(ctx, domain.NewConversation(bob, agentID)))
	require.NoError(t, testStore.CreateConversation(ctx, domain.NewConversation(alice, otherID)))

	n, err = testStore.CountByAgent(ctx, agentID)
	require.NoError(t, err)
	assert.Equal(t, int64(2), n, "only conversations bound to the agent count")

	n, err = testStore.CountByAgent(ctx, otherID)
	require.NoError(t, err)
	assert.Equal(t, int64(1), n)

	n, err = testStore.CountByAgent(ctx, "zz-agent-test-count-missing")
	require.NoError(t, err)
	assert.Zero(t, n, "an unknown agent counts zero conversations")
}
