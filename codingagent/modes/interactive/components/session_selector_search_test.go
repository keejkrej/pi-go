// Ported from packages/coding-agent/test/session-selector-search.test.ts (pi v1.5.2).

package components

import (
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/keejkrej/pi-go/codingagent/core"
)

func tsssMakeSession(id string, modified time.Time, allMessagesText string, name ...string) core.SessionInfo {
	var n *string
	if len(name) > 0 {
		v := name[0]
		n = &v
	}
	return core.SessionInfo{
		Path:            "/tmp/" + id + ".jsonl",
		Id:              id,
		Cwd:             "",
		Name:            n,
		Created:         time.Unix(0, 0).UTC(),
		Modified:        modified,
		MessageCount:    1,
		FirstMessage:    "(no messages)",
		AllMessagesText: allMessagesText,
	}
}

func tsssTime(iso string) time.Time {
	tm, err := time.Parse(time.RFC3339, iso)
	if err != nil {
		panic(err)
	}
	return tm
}

func tsssIds(sessions []core.SessionInfo) []string {
	out := make([]string, len(sessions))
	for i, s := range sessions {
		out[i] = s.Id
	}
	return out
}

func TestSessionSelectorSearch(t *testing.T) {
	t.Run("filters by quoted phrase with whitespace normalization", func(t *testing.T) {
		sessions := []core.SessionInfo{
			tsssMakeSession("a", tsssTime("2026-01-01T00:00:00Z"), "node\n\n   cve was discussed"),
			tsssMakeSession("b", tsssTime("2026-01-02T00:00:00Z"), "node something else"),
		}
		got := tsssIds(FilterAndSortSessions(sessions, `"node cve"`, SortModeRecent))
		if !slices.Equal(got, []string{"a"}) {
			t.Fatalf("got %v, want [a]", got)
		}
	})

	t.Run("filters by regex (re:) and is case-insensitive", func(t *testing.T) {
		sessions := []core.SessionInfo{
			tsssMakeSession("a", tsssTime("2026-01-02T00:00:00Z"), "Brave is great"),
			tsssMakeSession("b", tsssTime("2026-01-03T00:00:00Z"), "bravery is not the same"),
		}
		got := tsssIds(FilterAndSortSessions(sessions, `re:\bbrave\b`, SortModeRecent))
		if !slices.Equal(got, []string{"a"}) {
			t.Fatalf("got %v, want [a]", got)
		}
	})

	t.Run("recent sort preserves input order", func(t *testing.T) {
		sessions := []core.SessionInfo{
			tsssMakeSession("newer", tsssTime("2026-01-03T00:00:00Z"), "brave"),
			tsssMakeSession("older", tsssTime("2026-01-01T00:00:00Z"), "brave"),
			tsssMakeSession("nomatch", tsssTime("2026-01-04T00:00:00Z"), "something else"),
		}
		got := tsssIds(FilterAndSortSessions(sessions, `"brave"`, SortModeRecent))
		if !slices.Equal(got, []string{"newer", "older"}) {
			t.Fatalf("got %v, want [newer older]", got)
		}
	})

	t.Run("relevance sort orders by score and tie-breaks by modified desc", func(t *testing.T) {
		sessions := []core.SessionInfo{
			tsssMakeSession("message", tsssTime("2026-01-03T00:00:00Z"), "brave"),
			tsssMakeSession("name", tsssTime("2026-01-01T00:00:00Z"), "xxxx", "Brave work"),
		}
		got1 := tsssIds(FilterAndSortSessions(sessions, `"brave"`, SortModeRelevance))
		if !slices.Equal(got1, []string{"name", "message"}) {
			t.Fatalf("got1 %v, want [name message]", got1)
		}

		tieSessions := []core.SessionInfo{
			tsssMakeSession("newer", tsssTime("2026-01-03T00:00:00Z"), "brave"),
			tsssMakeSession("older", tsssTime("2026-01-01T00:00:00Z"), "brave"),
		}
		got2 := tsssIds(FilterAndSortSessions(tieSessions, `"brave"`, SortModeRelevance))
		if !slices.Equal(got2, []string{"newer", "older"}) {
			t.Fatalf("got2 %v, want [newer older]", got2)
		}
	})

	t.Run("ignores the position of a match in message text", func(t *testing.T) {
		filler := strings.Repeat("lorem ipsum dolor sit amet ", 400)
		sessions := []core.SessionInfo{
			tsssMakeSession("early", tsssTime("2026-01-01T00:00:00Z"), "opus "+filler),
			tsssMakeSession("deep", tsssTime("2026-01-02T00:00:00Z"), filler+" we discussed the opus model"),
			tsssMakeSession("scattered", tsssTime("2026-01-03T00:00:00Z"), filler),
		}
		got := tsssIds(FilterAndSortSessions(sessions, "opus", SortModeRelevance))
		if !slices.Equal(got, []string{"deep", "early"}) {
			t.Fatalf("got %v, want [deep early]", got)
		}
	})

	t.Run("does not match scattered characters in session ids", func(t *testing.T) {
		sessions := []core.SessionInfo{
			tsssMakeSession("019e9024-aaaa-7bbb-8ccc-dddddddddddd", tsssTime("2026-01-03T00:00:00Z"), "unrelated"),
			tsssMakeSession("01a1ffff-aaaa-7bbb-8ccc-dddddddddddd", tsssTime("2026-01-01T00:00:00Z"), "unrelated", "#1024 TDD bare env"),
		}
		res1 := FilterAndSortSessions(sessions, "1024", SortModeRelevance)
		if len(res1) != 1 || res1[0].Name == nil || *res1[0].Name != "#1024 TDD bare env" {
			t.Fatalf("unexpected res1 = %+v", res1)
		}
		res2 := tsssIds(FilterAndSortSessions(sessions, "9024-aa", SortModeRelevance))
		if !slices.Equal(res2, []string{"019e9024-aaaa-7bbb-8ccc-dddddddddddd"}) {
			t.Fatalf("unexpected res2 = %v", res2)
		}
	})

	t.Run("ranks name text, then message text, then scattered name matches", func(t *testing.T) {
		sessions := []core.SessionInfo{
			tsssMakeSession("scattered-name", tsssTime("2026-01-04T00:00:00Z"), "unrelated", "c-a-c-h-e notes"),
			tsssMakeSession("message", tsssTime("2026-01-03T00:00:00Z"), "A discussion mentioning the cache.", "Other work"),
			tsssMakeSession("name", tsssTime("2026-01-01T00:00:00Z"), "Summary", "Cache work"),
		}
		got := tsssIds(FilterAndSortSessions(sessions, "cache", SortModeRelevance))
		if !slices.Equal(got, []string{"name", "message", "scattered-name"}) {
			t.Fatalf("got %v, want [name message scattered-name]", got)
		}
	})

	t.Run("matches each token within a single field", func(t *testing.T) {
		sessions := []core.SessionInfo{
			tsssMakeSession("split", tsssTime("2026-01-01T00:00:00Z"), "search ranking", "Fuzzy"),
		}
		if got := tsssIds(FilterAndSortSessions(sessions, "fuzzy ranking", SortModeRelevance)); !slices.Equal(got, []string{"split"}) {
			t.Fatalf("got %v, want [split]", got)
		}
		if got := FilterAndSortSessions(sessions, "zzysea", SortModeRelevance); len(got) != 0 {
			t.Fatalf("expected empty, got %+v", got)
		}
		if got := FilterAndSortSessions(sessions, "re:fuzzy search", SortModeRelevance); len(got) != 0 {
			t.Fatalf("expected empty, got %+v", got)
		}
	})

	t.Run("ranks regex name matches first", func(t *testing.T) {
		sessions := []core.SessionInfo{
			tsssMakeSession("message", tsssTime("2026-01-02T00:00:00Z"), "brave"),
			tsssMakeSession("name", tsssTime("2026-01-01T00:00:00Z"), "xxxx", "Brave"),
		}
		got := tsssIds(FilterAndSortSessions(sessions, `re:\bbrave\b`, SortModeRelevance))
		if !slices.Equal(got, []string{"name", "message"}) {
			t.Fatalf("got %v, want [name message]", got)
		}
	})

	t.Run("returns empty list for invalid regex", func(t *testing.T) {
		sessions := []core.SessionInfo{
			tsssMakeSession("a", tsssTime("2026-01-01T00:00:00Z"), "brave"),
		}
		got := FilterAndSortSessions(sessions, "re:(", SortModeRecent)
		if len(got) != 0 {
			t.Fatalf("expected empty, got %+v", got)
		}
	})

	t.Run("name filter", func(t *testing.T) {
		sessions := []core.SessionInfo{
			tsssMakeSession("named1", tsssTime("2026-01-03T00:00:00Z"), "blueberry", "My Project"),
			tsssMakeSession("named2", tsssTime("2026-01-02T00:00:00Z"), "blueberry", "Another Named"),
			tsssMakeSession("other1", tsssTime("2026-01-04T00:00:00Z"), "blueberry"),
			tsssMakeSession("other2", tsssTime("2026-01-01T00:00:00Z"), "blueberry"),
		}
		if got := tsssIds(FilterAndSortSessions(sessions, "", SortModeRecent, NameFilterAll)); !slices.Equal(got, []string{"named1", "named2", "other1", "other2"}) {
			t.Fatalf("all got %v", got)
		}
		if got := tsssIds(FilterAndSortSessions(sessions, "", SortModeRecent, NameFilterNamed)); !slices.Equal(got, []string{"named1", "named2"}) {
			t.Fatalf("named got %v", got)
		}
		if got := tsssIds(FilterAndSortSessions(sessions, "blueberry", SortModeRecent, NameFilterNamed)); !slices.Equal(got, []string{"named1", "named2"}) {
			t.Fatalf("named+query got %v", got)
		}

		sessionsWithWhitespace := []core.SessionInfo{
			tsssMakeSession("whitespace", tsssTime("2026-01-01T00:00:00Z"), "test", "   "),
			tsssMakeSession("empty", tsssTime("2026-01-02T00:00:00Z"), "test", ""),
			tsssMakeSession("named", tsssTime("2026-01-03T00:00:00Z"), "test", "Real Name"),
		}
		if got := tsssIds(FilterAndSortSessions(sessionsWithWhitespace, "", SortModeRecent, NameFilterNamed)); !slices.Equal(got, []string{"named"}) {
			t.Fatalf("whitespace excluded got %v", got)
		}
	})
}
