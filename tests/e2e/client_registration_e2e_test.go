// Copyright 2026 Decker.
// This file is part of wicket-pg, an independent original work.

// Package e2e_test pins the client registration store's semantics that the
// conformance suite cannot reach, by querying the client_registrations table
// directly: the visible columns written at registration, their refresh on
// update, the tombstone shape a removal leaves, nil allowed origins stored
// as an empty array, sub-microsecond registration times surviving the round
// trip through payload, and reads of an undecodable row. The suite is gated
// behind WICKET_PG_TEST_DATABASE_URL so the plain test run stays green
// without a database.
package e2e_test

import (
	"errors"
	"reflect"
	"slices"
	"testing"
	"time"

	"github.com/gonewx/wicket"
	"github.com/gonewx/wicket-pg/migrations"
	"github.com/gonewx/wicket-pg/store"
	"github.com/gonewx/wicket/storage"
	"github.com/gonewx/wicket/storage/models"
	"github.com/jackc/pgx/v5/pgxpool"
)

// newDCRStore migrates a scratch database and returns the store with its
// pool, so a test can both drive the ports and query the table.
func newDCRStore(t *testing.T) (*store.ClientRegistrationStore, *pgxpool.Pool) {
	t.Helper()
	pool := newScratchPool(t)
	if err := migrations.Up(t.Context(), pool); err != nil {
		t.Fatalf("Up: %v", err)
	}
	return store.NewClientRegistrationStore(pool, discardLogger()), pool
}

// dcrRegistration builds a registration of clientId with the given origins
// and digest, registered at fixedNow.
func dcrRegistration(t *testing.T, clientId, digest string, origins ...string) *models.ClientRegistration {
	t.Helper()
	client := models.NewClient()
	client.ClientId = clientId
	client.ClientName = "Client " + clientId
	client.ClientSecrets = []*models.Secret{{Description: "primary", Value: "hashed-secret", Type: wicket.SecretTypesSharedSecret}}
	client.RedirectUris = []string{"https://" + clientId + ".example/cb"}
	client.AllowedScopes = []string{"openid"}
	client.AllowedCorsOrigins = origins
	if err := client.SetAllowedGrantTypes([]string{wicket.GrantTypeAuthorizationCode}); err != nil {
		t.Fatalf("SetAllowedGrantTypes: %v", err)
	}
	return &models.ClientRegistration{
		Client:                        client,
		RegistrationAccessTokenDigest: digest,
		RegistrationTime:              fixedNow,
	}
}

// dcrRow is the raw content of one client_registrations row.
type dcrRow struct {
	revision         int64
	removed          bool
	digest           *string
	registrationTime time.Time
	origins          []string
	payloadIsNull    bool
}

func readDCRRow(t *testing.T, pool *pgxpool.Pool, clientId string) dcrRow {
	t.Helper()
	var row dcrRow
	if err := pool.QueryRow(t.Context(),
		`SELECT revision, removed, registration_access_token_digest, registration_time,
		        allowed_cors_origins, payload IS NULL
		 FROM client_registrations WHERE client_id = $1`,
		clientId).Scan(&row.revision, &row.removed, &row.digest, &row.registrationTime, &row.origins, &row.payloadIsNull); err != nil {
		t.Fatalf("read client_registrations row %s: %v", clientId, err)
	}
	return row
}

// TestE2EClientRegistrationVisibleColumns pins the write-time copy of the
// visible columns at registration: the digest, the registration time and
// the allowed origins equal the model values, the revision is 1 and the
// row is live.
func TestE2EClientRegistrationVisibleColumns(t *testing.T) {
	s, pool := newDCRStore(t)
	registration := dcrRegistration(t, "client-a", "digest-a", "https://a.example", "https://b.example")
	registration.Revision = 42
	if _, err := s.RegisterClient(t.Context(), registration); err != nil {
		t.Fatalf("RegisterClient: %v", err)
	}

	row := readDCRRow(t, pool, "client-a")
	if row.revision != 1 {
		t.Errorf("revision = %d, want 1", row.revision)
	}
	if row.removed {
		t.Error("removed = true, want false")
	}
	if row.digest == nil || *row.digest != "digest-a" {
		t.Errorf("registration_access_token_digest = %v, want %q", row.digest, "digest-a")
	}
	if !row.registrationTime.Equal(fixedNow) {
		t.Errorf("registration_time = %v, want %v", row.registrationTime, fixedNow)
	}
	if want := []string{"https://a.example", "https://b.example"}; !slices.Equal(row.origins, want) {
		t.Errorf("allowed_cors_origins = %v, want %v", row.origins, want)
	}
	if row.payloadIsNull {
		t.Error("payload is NULL on a live row")
	}
}

// TestE2EClientRegistrationUpdateRefreshesColumns pins that an update
// refreshes the visible columns in the same statement that bumps the
// revision: no stale digest, time or origin survives it.
func TestE2EClientRegistrationUpdateRefreshesColumns(t *testing.T) {
	s, pool := newDCRStore(t)
	if _, err := s.RegisterClient(t.Context(), dcrRegistration(t, "client-a", "digest-a", "https://a.example")); err != nil {
		t.Fatalf("RegisterClient: %v", err)
	}
	updated := dcrRegistration(t, "client-a", "digest-b", "https://moved.example")
	updated.RegistrationTime = fixedNow.Add(time.Hour)
	revision, err := s.UpdateClientRegistration(t.Context(), updated, 1)
	if err != nil {
		t.Fatalf("UpdateClientRegistration: %v", err)
	}
	if revision != 2 {
		t.Errorf("UpdateClientRegistration revision = %d, want 2", revision)
	}

	row := readDCRRow(t, pool, "client-a")
	if row.revision != 2 {
		t.Errorf("revision = %d, want 2", row.revision)
	}
	if row.digest == nil || *row.digest != "digest-b" {
		t.Errorf("registration_access_token_digest = %v, want %q", row.digest, "digest-b")
	}
	if !row.registrationTime.Equal(updated.RegistrationTime) {
		t.Errorf("registration_time = %v, want %v", row.registrationTime, updated.RegistrationTime)
	}
	if want := []string{"https://moved.example"}; !slices.Equal(row.origins, want) {
		t.Errorf("allowed_cors_origins = %v, want %v", row.origins, want)
	}
}

// TestE2EClientRegistrationTombstoneShape pins what a removal leaves: the
// row stays (it blocks re-registration through the primary key), flagged
// removed, with payload and digest NULL and the origins emptied, so no
// secret hash or digest survives; client_id, revision and registration_time
// are kept.
func TestE2EClientRegistrationTombstoneShape(t *testing.T) {
	s, pool := newDCRStore(t)
	if _, err := s.RegisterClient(t.Context(), dcrRegistration(t, "client-a", "digest-a", "https://a.example")); err != nil {
		t.Fatalf("RegisterClient: %v", err)
	}
	if err := s.RemoveClientRegistration(t.Context(), "client-a", 1); err != nil {
		t.Fatalf("RemoveClientRegistration: %v", err)
	}

	row := readDCRRow(t, pool, "client-a")
	if !row.removed {
		t.Error("removed = false after removal, want true")
	}
	if !row.payloadIsNull {
		t.Error("payload is not NULL on a tombstone")
	}
	if row.digest != nil {
		t.Errorf("registration_access_token_digest = %q on a tombstone, want NULL", *row.digest)
	}
	if row.origins == nil || len(row.origins) != 0 {
		t.Errorf("allowed_cors_origins = %#v on a tombstone, want {}", row.origins)
	}
	if row.revision != 1 {
		t.Errorf("revision = %d on a tombstone, want 1 (kept)", row.revision)
	}
	if !row.registrationTime.Equal(fixedNow) {
		t.Errorf("registration_time = %v on a tombstone, want %v (kept)", row.registrationTime, fixedNow)
	}
}

// TestE2EClientRegistrationNilOriginsStoreEmptyArray pins that nil allowed
// origins are written as an empty array rather than NULL (the column is NOT
// NULL), and that the client still reads back equal to the one registered.
func TestE2EClientRegistrationNilOriginsStoreEmptyArray(t *testing.T) {
	s, pool := newDCRStore(t)
	registration := dcrRegistration(t, "client-a", "digest-a")
	if registration.Client.AllowedCorsOrigins != nil {
		t.Fatalf("fixture AllowedCorsOrigins = %#v, want nil", registration.Client.AllowedCorsOrigins)
	}
	if _, err := s.RegisterClient(t.Context(), registration); err != nil {
		t.Fatalf("RegisterClient with nil origins: %v", err)
	}

	var isEmpty bool
	if err := pool.QueryRow(t.Context(),
		"SELECT allowed_cors_origins = '{}' FROM client_registrations WHERE client_id = $1", "client-a").Scan(&isEmpty); err != nil {
		t.Fatalf("read allowed_cors_origins: %v", err)
	}
	if !isEmpty {
		t.Error("allowed_cors_origins for nil origins is not {}")
	}

	got, err := s.FindClientById(t.Context(), "client-a")
	if err != nil {
		t.Fatalf("FindClientById: %v", err)
	}
	if !reflect.DeepEqual(got, registration.Client) {
		t.Errorf("FindClientById = %+v, want %+v", got, registration.Client)
	}
}

// TestE2EClientRegistrationSubMicrosecondTime pins that a registration time
// carrying nanoseconds survives the round trip: reads decode it from
// payload, not from the microsecond registration_time column.
func TestE2EClientRegistrationSubMicrosecondTime(t *testing.T) {
	s, pool := newDCRStore(t)
	registration := dcrRegistration(t, "client-a", "digest-a", "https://a.example")
	registration.RegistrationTime = fixedNow.Add(123456789 * time.Nanosecond)
	if _, err := s.RegisterClient(t.Context(), registration); err != nil {
		t.Fatalf("RegisterClient: %v", err)
	}

	got, err := s.FindClientRegistration(t.Context(), "client-a")
	if err != nil {
		t.Fatalf("FindClientRegistration: %v", err)
	}
	if !got.RegistrationTime.Equal(registration.RegistrationTime) {
		t.Errorf("RegistrationTime = %v, want %v", got.RegistrationTime, registration.RegistrationTime)
	}
	// The column itself is truncated to microseconds, which is why it is
	// never read back.
	row := readDCRRow(t, pool, "client-a")
	if row.registrationTime.Equal(registration.RegistrationTime) {
		t.Errorf("registration_time column = %v kept nanoseconds; the test premise no longer holds", row.registrationTime)
	}
}

// TestE2EClientRegistrationUndecodablePayload pins reads of a row whose
// payload cannot be decoded: FindClientRegistration, FindClientById and the
// origin lookup all fail with a wrapped decode error, not
// storage.ErrNotFound and not an empty result.
func TestE2EClientRegistrationUndecodablePayload(t *testing.T) {
	s, pool := newDCRStore(t)
	if _, err := pool.Exec(t.Context(),
		`INSERT INTO client_registrations
			(client_id, revision, removed, registration_access_token_digest, registration_time, allowed_cors_origins, payload)
			VALUES ('client-bad', 1, false, 'digest-bad', $1, ARRAY['https://bad.example'],
			        '{"version":1,"dataProtected":false,"payload":"!!not-base64!!"}'::jsonb)`,
		fixedNow); err != nil {
		t.Fatalf("insert undecodable row: %v", err)
	}

	registration, err := s.FindClientRegistration(t.Context(), "client-bad")
	if err == nil || errors.Is(err, storage.ErrNotFound) {
		t.Errorf("FindClientRegistration = %+v, %v; want a decode error, not storage.ErrNotFound", registration, err)
	}
	if registration != nil {
		t.Errorf("FindClientRegistration record = %+v, want nil", registration)
	}
	client, err := s.FindClientById(t.Context(), "client-bad")
	if err == nil || errors.Is(err, storage.ErrNotFound) {
		t.Errorf("FindClientById = %+v, %v; want a decode error, not storage.ErrNotFound", client, err)
	}
	clients, err := s.FindClientsByAllowedOrigin(t.Context(), "https://bad.example")
	if err == nil || errors.Is(err, storage.ErrNotFound) {
		t.Errorf("FindClientsByAllowedOrigin = %v, %v; want a decode error", clients, err)
	}
	if clients != nil {
		t.Errorf("FindClientsByAllowedOrigin result = %v, want nil on error", clients)
	}
}
