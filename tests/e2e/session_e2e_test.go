// Copyright 2026 Decker.
// This file is part of wicket-pg, an independent original work.

// Package e2e_test pins the session store's behavior against the real
// schema: the expires column semantics (a non-zero Expires lands as-is, a
// zero value lands as NULL and is never cleaned up), the subject_id column
// (a non-empty subject lands as-is, an empty subject lands as NULL and can
// never match a query), the client_ids column as the authoritative
// ClientIDs source (nil normalizes to the empty array, AddClientID appends
// and deduplicates at column level, reads override the payload snapshot),
// the full-column refresh of UpdateSession, and the MAY-level equivalences
// the session suite cannot run (concurrent appends all collected,
// concurrent create single winner, ConformsTo). The suite is gated behind
// WICKET_PG_TEST_DATABASE_URL so the plain test run stays green without a
// database.
package e2e_test

import (
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/gonewx/wicket-pg/migrations"
	"github.com/gonewx/wicket-pg/store"
	"github.com/gonewx/wicket/session"
	"github.com/gonewx/wicket/session/sessiontest"
	"github.com/jackc/pgx/v5/pgxpool"
)

// TestE2ESessionExpiresColumn asserts the expiry semantics directly at the
// column level: a non-zero Expires lands in expires as-is, while a zero
// Expires lands as NULL (never expires) — the exact distinction
// DeleteExpired's IS NOT NULL guard relies on.
func TestE2ESessionExpiresColumn(t *testing.T) {
	pool := newScratchPool(t)
	if err := migrations.Up(t.Context(), pool); err != nil {
		t.Fatalf("Up: %v", err)
	}
	s := store.NewSessionStore(pool, discardLogger())

	nonZero := &session.Record{
		SessionID: "sess-exp-1",
		Expires:   fixedAlive,
	}
	if err := s.CreateSession(t.Context(), nonZero); err != nil {
		t.Fatalf("CreateSession (non-zero expires): %v", err)
	}
	assertSessionExpires(t, pool, "sess-exp-1", fixedAlive)

	zero := &session.Record{SessionID: "sess-exp-zero"}
	if err := s.CreateSession(t.Context(), zero); err != nil {
		t.Fatalf("CreateSession (zero expires): %v", err)
	}
	assertSessionExpiresNull(t, pool, "sess-exp-zero")
}

// TestE2ESessionSubjectIDColumn asserts the subject column semantics
// directly: a non-empty subject lands as-is, an empty subject lands as NULL
// (mirroring the in-memory "empty subject is never indexed" rule, so a
// query for "" can never match), and the subject batch read path returns
// the session with an empty-but-non-nil result for an unknown subject.
func TestE2ESessionSubjectIDColumn(t *testing.T) {
	pool := newScratchPool(t)
	if err := migrations.Up(t.Context(), pool); err != nil {
		t.Fatalf("Up: %v", err)
	}
	s := store.NewSessionStore(pool, discardLogger())

	withSubject := &session.Record{
		SessionID: "sess-subj-1",
		SubjectID: "user-42",
		Expires:   fixedAlive,
	}
	if err := s.CreateSession(t.Context(), withSubject); err != nil {
		t.Fatalf("CreateSession (with subject): %v", err)
	}
	assertSessionSubjectID(t, pool, "sess-subj-1", "user-42")

	emptySubject := &session.Record{SessionID: "sess-subj-empty"}
	if err := s.CreateSession(t.Context(), emptySubject); err != nil {
		t.Fatalf("CreateSession (empty subject): %v", err)
	}
	assertSessionSubjectIDNull(t, pool, "sess-subj-empty")

	bySubject, err := s.GetSessionsBySubjectID(t.Context(), "user-42")
	if err != nil {
		t.Fatalf("GetSessionsBySubjectID: %v", err)
	}
	if len(bySubject) != 1 || bySubject[0].SessionID != "sess-subj-1" {
		t.Errorf("GetSessionsBySubjectID(user-42) = %v, want one session sess-subj-1", bySubject)
	}

	unknown, err := s.GetSessionsBySubjectID(t.Context(), "no-such-subject")
	if err != nil {
		t.Fatalf("GetSessionsBySubjectID(unknown): %v", err)
	}
	if unknown == nil || len(unknown) != 0 {
		t.Errorf("GetSessionsBySubjectID(unknown) = %#v, want empty non-nil slice", unknown)
	}
}

// TestE2ESessionClientIDsColumnBehavior asserts the client_ids column as
// the authoritative ClientIDs source: a nil ClientIDs is stored as the
// empty array (the column is NOT NULL), AddClientID appends and
// deduplicates at column level, and GetSession reads the column back,
// overriding the payload's write-time snapshot.
func TestE2ESessionClientIDsColumnBehavior(t *testing.T) {
	pool := newScratchPool(t)
	if err := migrations.Up(t.Context(), pool); err != nil {
		t.Fatalf("Up: %v", err)
	}
	s := store.NewSessionStore(pool, discardLogger())

	nilClients := &session.Record{
		SessionID:   "sess-cli-nil",
		ClientIDs:   nil,
		Expires:     fixedAlive,
		DisplayName: "nil clients",
	}
	if err := s.CreateSession(t.Context(), nilClients); err != nil {
		t.Fatalf("CreateSession (nil client ids): %v", err)
	}
	assertSessionClientIDs(t, pool, "sess-cli-nil", []string{})

	// The record carries a payload-snapshot ClientIDs of [c1]; AddClientID
	// only touches the column, so a read must reflect the column.
	withClients := &session.Record{
		SessionID:   "sess-cli-1",
		ClientIDs:   []string{"c1"},
		DisplayName: "client snapshot",
		Expires:     fixedAlive,
	}
	if err := s.CreateSession(t.Context(), withClients); err != nil {
		t.Fatalf("CreateSession (with client ids): %v", err)
	}

	if err := s.AddClientID(t.Context(), "sess-cli-1", "c1"); err != nil {
		t.Fatalf("AddClientID (duplicate c1): %v", err)
	}
	if err := s.AddClientID(t.Context(), "sess-cli-1", "c2"); err != nil {
		t.Fatalf("AddClientID (c2): %v", err)
	}
	assertSessionClientIDs(t, pool, "sess-cli-1", []string{"c1", "c2"})

	got, err := s.GetSession(t.Context(), "sess-cli-1")
	if err != nil {
		t.Fatalf("GetSession after AddClientID: %v", err)
	}
	if len(got.ClientIDs) != 2 || got.ClientIDs[0] != "c1" || got.ClientIDs[1] != "c2" {
		t.Errorf("GetSession ClientIDs = %v, want [c1 c2] from the column (payload snapshot overridden)", got.ClientIDs)
	}
}

// TestE2ESessionUpdateRefreshesAllColumns asserts the full-column refresh of
// UpdateSession: payload, client_ids, and subject_id are replaced together;
// a subject change redirects the batch queries (the old subject comes back
// empty, the new one hits); and updating a missing session is a no-op.
func TestE2ESessionUpdateRefreshesAllColumns(t *testing.T) {
	pool := newScratchPool(t)
	if err := migrations.Up(t.Context(), pool); err != nil {
		t.Fatalf("Up: %v", err)
	}
	s := store.NewSessionStore(pool, discardLogger())

	initial := &session.Record{
		SessionID:   "sess-upd-1",
		SubjectID:   "user-old",
		DisplayName: "before",
		ClientIDs:   []string{"c1"},
		Expires:     fixedExpired,
	}
	if err := s.CreateSession(t.Context(), initial); err != nil {
		t.Fatalf("CreateSession: %v", err)
	}

	updated := &session.Record{
		SessionID:   "sess-upd-1",
		SubjectID:   "user-new",
		DisplayName: "after",
		ClientIDs:   []string{"c9"},
		Expires:     fixedAlive,
	}
	if err := s.UpdateSession(t.Context(), updated); err != nil {
		t.Fatalf("UpdateSession: %v", err)
	}

	assertSessionSubjectID(t, pool, "sess-upd-1", "user-new")
	assertSessionClientIDs(t, pool, "sess-upd-1", []string{"c9"})
	assertSessionExpires(t, pool, "sess-upd-1", fixedAlive)

	got, err := s.GetSession(t.Context(), "sess-upd-1")
	if err != nil {
		t.Fatalf("GetSession after update: %v", err)
	}
	if got.DisplayName != "after" {
		t.Errorf("DisplayName after update = %q, want after (payload replaced)", got.DisplayName)
	}

	oldSubject, err := s.GetSessionsBySubjectID(t.Context(), "user-old")
	if err != nil {
		t.Fatalf("GetSessionsBySubjectID(old subject): %v", err)
	}
	if len(oldSubject) != 0 {
		t.Errorf("old subject still matches %d sessions after update, want 0", len(oldSubject))
	}
	newSubject, err := s.GetSessionsBySubjectID(t.Context(), "user-new")
	if err != nil {
		t.Fatalf("GetSessionsBySubjectID(new subject): %v", err)
	}
	if len(newSubject) != 1 {
		t.Errorf("new subject matches %d sessions after update, want 1", len(newSubject))
	}

	// Updating a missing session is a no-op: no error, no new row.
	missing := &session.Record{
		SessionID:   "sess-absent",
		SubjectID:   "user-new",
		DisplayName: "ghost",
		ClientIDs:   []string{"c1"},
		Expires:     fixedAlive,
	}
	if err := s.UpdateSession(t.Context(), missing); err != nil {
		t.Fatalf("UpdateSession on missing session: %v", err)
	}
	var count int
	if err := pool.QueryRow(t.Context(),
		"SELECT count(*) FROM sessions WHERE session_id = 'sess-absent'").Scan(&count); err != nil {
		t.Fatalf("count sessions: %v", err)
	}
	if count != 0 {
		t.Errorf("sessions holds %d rows after no-op update, want 0", count)
	}
}

// TestE2ESessionSubjectLogoutAndExpiredNullRows asserts the batch deletion
// by subject with its row count, and that DeleteExpired never touches rows
// whose expires is NULL (zero-value Expires): the reclaim count is zero and
// the row survives.
func TestE2ESessionSubjectLogoutAndExpiredNullRows(t *testing.T) {
	pool := newScratchPool(t)
	if err := migrations.Up(t.Context(), pool); err != nil {
		t.Fatalf("Up: %v", err)
	}
	s := store.NewSessionStore(pool, discardLogger())

	for _, id := range []string{"sess-logout-1", "sess-logout-2", "sess-logout-3"} {
		if err := s.CreateSession(t.Context(), &session.Record{
			SessionID: id,
			SubjectID: "user-logout",
			Expires:   fixedAlive,
		}); err != nil {
			t.Fatalf("CreateSession %s: %v", id, err)
		}
	}
	if err := s.CreateSession(t.Context(), &session.Record{
		SessionID: "sess-logout-other",
		SubjectID: "user-other",
		Expires:   fixedAlive,
	}); err != nil {
		t.Fatalf("CreateSession other subject: %v", err)
	}
	// A zero-Expires row with an empty subject must survive both the
	// subject batch deletion (empty subject is stored as NULL and never
	// matches an equality query) and DeleteExpired (NULL expires is never
	// cleaned).
	if err := s.CreateSession(t.Context(), &session.Record{
		SessionID: "sess-noexpiry",
	}); err != nil {
		t.Fatalf("CreateSession no expiry: %v", err)
	}

	removed, err := s.DeleteSessionsBySubjectID(t.Context(), "user-logout")
	if err != nil {
		t.Fatalf("DeleteSessionsBySubjectID: %v", err)
	}
	if removed != 3 {
		t.Errorf("DeleteSessionsBySubjectID removed %d rows, want 3", removed)
	}

	bySubject, err := s.GetSessionsBySubjectID(t.Context(), "user-logout")
	if err != nil {
		t.Fatalf("GetSessionsBySubjectID after logout: %v", err)
	}
	if len(bySubject) != 0 {
		t.Errorf("user-logout still matches %d sessions after logout, want 0", len(bySubject))
	}
	other, err := s.GetSessionsBySubjectID(t.Context(), "user-other")
	if err != nil {
		t.Fatalf("GetSessionsBySubjectID(user-other): %v", err)
	}
	if len(other) != 1 || other[0].SessionID != "sess-logout-other" {
		t.Errorf("other subject sessions = %v, want only sess-logout-other", other)
	}

	// The empty-subject row was not hit by the batch deletion; the empty
	// subject query is equally a no-op.
	if _, err := s.DeleteSessionsBySubjectID(t.Context(), ""); err != nil {
		t.Fatalf("DeleteSessionsBySubjectID(empty subject): %v", err)
	}
	expired, err := s.DeleteExpired(t.Context(), fixedNow)
	if err != nil {
		t.Fatalf("DeleteExpired: %v", err)
	}
	if expired != 0 {
		t.Errorf("DeleteExpired removed %d rows, want 0 (NULL expiry never cleaned)", expired)
	}
	assertSessionExpiresNull(t, pool, "sess-noexpiry")
}

// TestE2ESessionConcurrentAddClientIDAllCollected is the e2e equivalent of
// the MAY case ConcurrentAddClientIDAllCollected: eight goroutines appending
// eight distinct client ids to one session must all land — the single-row
// UPDATE serializes on the row lock and array_append re-evaluates against
// the latest committed version, so no append is lost.
func TestE2ESessionConcurrentAddClientIDAllCollected(t *testing.T) {
	pool := newScratchPool(t)
	if err := migrations.Up(t.Context(), pool); err != nil {
		t.Fatalf("Up: %v", err)
	}
	s := store.NewSessionStore(pool, discardLogger())

	if err := s.CreateSession(t.Context(), &session.Record{SessionID: "sess-conc-append"}); err != nil {
		t.Fatalf("CreateSession: %v", err)
	}

	const workers = 8
	var wg sync.WaitGroup
	errs := make(chan error, workers)
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			errs <- s.AddClientID(t.Context(), "sess-conc-append", fmt.Sprintf("client-%d", n))
		}(i)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatalf("concurrent AddClientID: %v", err)
		}
	}

	got, err := s.GetSession(t.Context(), "sess-conc-append")
	if err != nil {
		t.Fatalf("GetSession: %v", err)
	}
	if len(got.ClientIDs) != workers {
		t.Errorf("concurrent appends collected %d client ids, want %d", len(got.ClientIDs), workers)
	}
	seen := make(map[string]bool)
	for _, id := range got.ClientIDs {
		if seen[id] {
			t.Errorf("client id %s duplicated in concurrent appends", id)
		}
		seen[id] = true
	}
}

// TestE2ESessionConcurrentCreateSingleWinner is the e2e equivalent of the
// MAY case ConcurrentCreateSessionSingleWinner: eight goroutines creating
// the same session id must leave exactly one winner — the primary key
// rejects the other seven with a unique violation.
func TestE2ESessionConcurrentCreateSingleWinner(t *testing.T) {
	pool := newScratchPool(t)
	if err := migrations.Up(t.Context(), pool); err != nil {
		t.Fatalf("Up: %v", err)
	}
	s := store.NewSessionStore(pool, discardLogger())

	const workers = 8
	var wg sync.WaitGroup
	results := make(chan error, workers)
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			results <- s.CreateSession(t.Context(), &session.Record{
				SessionID: "sess-conc-create",
				SubjectID: fmt.Sprintf("user-%d", n),
			})
		}(i)
	}
	wg.Wait()
	close(results)

	winners := 0
	for err := range results {
		if err == nil {
			winners++
		}
	}
	if winners != 1 {
		t.Errorf("concurrent create had %d winners, want exactly 1", winners)
	}

	var count int
	if err := pool.QueryRow(t.Context(),
		"SELECT count(*) FROM sessions WHERE session_id = 'sess-conc-create'").Scan(&count); err != nil {
		t.Fatalf("count sessions: %v", err)
	}
	if count != 1 {
		t.Errorf("sessions holds %d rows for concurrent id, want 1", count)
	}
}

// TestE2ESessionConformsTo is the e2e equivalent of the MAY case
// ConformsToCredential: the adapter reports the session suite version.
func TestE2ESessionConformsTo(t *testing.T) {
	pool := newScratchPool(t)
	if err := migrations.Up(t.Context(), pool); err != nil {
		t.Fatalf("Up: %v", err)
	}
	if got := store.NewSessionStore(pool, discardLogger()).ConformsTo(); got != sessiontest.SuiteVersion {
		t.Errorf("ConformsTo() = %q, want %q", got, sessiontest.SuiteVersion)
	}
}

// assertSessionExpires asserts the expires column for the session id.
func assertSessionExpires(t *testing.T, pool *pgxpool.Pool, sessionID string, want time.Time) {
	t.Helper()
	var got time.Time
	err := pool.QueryRow(t.Context(),
		"SELECT expires FROM sessions WHERE session_id = $1", sessionID).Scan(&got)
	if err != nil {
		t.Fatalf("query expires for %s: %v", sessionID, err)
	}
	if !got.Equal(want) {
		t.Errorf("expires for %s = %v, want %v", sessionID, got, want)
	}
}

// assertSessionExpiresNull asserts the expires column is NULL for the
// session id (zero-value Expires never expires).
func assertSessionExpiresNull(t *testing.T, pool *pgxpool.Pool, sessionID string) {
	t.Helper()
	var got *time.Time
	err := pool.QueryRow(t.Context(),
		"SELECT expires FROM sessions WHERE session_id = $1", sessionID).Scan(&got)
	if err != nil {
		t.Fatalf("query expires for %s: %v", sessionID, err)
	}
	if got != nil {
		t.Errorf("expires for %s = %v, want NULL (zero-value Expires never expires)", sessionID, got)
	}
}

// assertSessionSubjectID asserts the subject_id column for the session id.
func assertSessionSubjectID(t *testing.T, pool *pgxpool.Pool, sessionID, want string) {
	t.Helper()
	var got string
	err := pool.QueryRow(t.Context(),
		"SELECT subject_id FROM sessions WHERE session_id = $1", sessionID).Scan(&got)
	if err != nil {
		t.Fatalf("query subject_id for %s: %v", sessionID, err)
	}
	if got != want {
		t.Errorf("subject_id for %s = %q, want %q", sessionID, got, want)
	}
}

// assertSessionSubjectIDNull asserts the subject_id column is NULL for the
// session id (empty subject is never indexed).
func assertSessionSubjectIDNull(t *testing.T, pool *pgxpool.Pool, sessionID string) {
	t.Helper()
	var got *string
	err := pool.QueryRow(t.Context(),
		"SELECT subject_id FROM sessions WHERE session_id = $1", sessionID).Scan(&got)
	if err != nil {
		t.Fatalf("query subject_id for %s: %v", sessionID, err)
	}
	if got != nil {
		t.Errorf("subject_id for %s = %q, want NULL (empty subject never indexed)", sessionID, *got)
	}
}

// assertSessionClientIDs asserts the client_ids column for the session id.
func assertSessionClientIDs(t *testing.T, pool *pgxpool.Pool, sessionID string, want []string) {
	t.Helper()
	var got []string
	err := pool.QueryRow(t.Context(),
		"SELECT client_ids FROM sessions WHERE session_id = $1", sessionID).Scan(&got)
	if err != nil {
		t.Fatalf("query client_ids for %s: %v", sessionID, err)
	}
	if len(got) != len(want) {
		t.Errorf("client_ids for %s = %v, want %v", sessionID, got, want)
		return
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("client_ids for %s = %v, want %v", sessionID, got, want)
			return
		}
	}
}
