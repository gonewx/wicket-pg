// Copyright 2026 Decker.
// This file is part of wicket-pg, an independent original work.

package store

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"testing"
	"time"
)

// persistedPayload mimics the shape of a persisted grant model: an opaque
// []byte field and a timestamp, plus a nested struct.
type persistedPayload struct {
	Data     []byte    `json:"data"`
	IssuedAt time.Time `json:"issued_at"`
	Meta     meta      `json:"meta"`
}

type meta struct {
	ClientID string   `json:"client_id"`
	Scopes   []string `json:"scopes"`
}

func TestEncodePayloadContainerShape(t *testing.T) {
	codec := payloadCodec{}
	in := persistedPayload{Data: []byte("opaque"), IssuedAt: time.Unix(0, 0).UTC()}

	encoded, err := codec.encode(in)
	if err != nil {
		t.Fatalf("encode: %v", err)
	}

	var raw map[string]any
	if err := json.Unmarshal(encoded, &raw); err != nil {
		t.Fatalf("container is not valid JSON: %v", err)
	}

	// Exact field set: version, dataProtected, payload — nothing else.
	if len(raw) != 3 {
		t.Fatalf("container has %d fields, want exactly 3: %v", len(raw), raw)
	}
	if v, ok := raw["version"].(float64); !ok || v != 1 {
		t.Errorf("version = %v, want 1", raw["version"])
	}
	if dp, ok := raw["dataProtected"].(bool); !ok || dp {
		t.Errorf("dataProtected = %v, want false", raw["dataProtected"])
	}

	payload, ok := raw["payload"].(string)
	if !ok {
		t.Fatalf("payload field missing or not a string: %v", raw["payload"])
	}
	modelBytes, err := base64.StdEncoding.DecodeString(payload)
	if err != nil {
		t.Fatalf("payload is not valid base64: %v", err)
	}

	// The payload decodes to the model's JSON, not the container's.
	var modelCheck map[string]any
	if err := json.Unmarshal(modelBytes, &modelCheck); err != nil {
		t.Fatalf("payload does not decode to model JSON: %v", err)
	}
	if modelCheck["client_id"] != nil {
		t.Errorf("payload contains container-shaped data, want model JSON")
	}
}

func TestEncodeDecodeOpaqueBytesRoundTrip(t *testing.T) {
	codec := payloadCodec{}
	// Non-zero bytes with NUL and invalid UTF-8 must survive losslessly.
	data := []byte{0x00, 0xff, 0x80, 'a', 0x00, 0xfe}
	in := persistedPayload{Data: data, IssuedAt: time.Time{}}

	encoded, err := codec.encode(in)
	if err != nil {
		t.Fatalf("encode: %v", err)
	}

	var out persistedPayload
	if err := codec.decode(encoded, &out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !bytes.Equal(out.Data, data) {
		t.Errorf("data round trip = %v, want %v", out.Data, data)
	}
}

func TestEncodeDecodeTimeRoundTrip(t *testing.T) {
	codec := payloadCodec{}
	// time.Now() carries a monotonic clock reading; UTC location pins the
	// decoded location to UTC regardless of the system zone.
	orig := time.Now().UTC()
	in := persistedPayload{Data: []byte("t"), IssuedAt: orig}

	encoded, err := codec.encode(in)
	if err != nil {
		t.Fatalf("encode: %v", err)
	}

	var out persistedPayload
	if err := codec.decode(encoded, &out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !out.IssuedAt.Equal(orig) {
		t.Errorf("time round trip = %v, want instant of %v", out.IssuedAt, orig)
	}
	if out.IssuedAt.Location() != time.UTC {
		t.Errorf("decoded time location = %v, want UTC", out.IssuedAt.Location())
	}
	if out.IssuedAt != out.IssuedAt.Round(0) {
		t.Errorf("decoded time still carries a monotonic clock reading")
	}
}

func TestEncodeDecodeTimeNonUTCLocationRoundTrip(t *testing.T) {
	codec := payloadCodec{}
	// A non-UTC zone must still round-trip to an equal instant.
	orig := time.Unix(1754000000, 123456789).In(time.FixedZone("UTC+8", 8*3600))
	in := persistedPayload{Data: []byte("t"), IssuedAt: orig}

	encoded, err := codec.encode(in)
	if err != nil {
		t.Fatalf("encode: %v", err)
	}

	var out persistedPayload
	if err := codec.decode(encoded, &out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !out.IssuedAt.Equal(orig) {
		t.Errorf("time round trip = %v, want instant of %v", out.IssuedAt, orig)
	}
	if out.IssuedAt != out.IssuedAt.Round(0) {
		t.Errorf("decoded time still carries a monotonic clock reading")
	}
}

func TestEncodeDecodeZeroTimeStaysZero(t *testing.T) {
	codec := payloadCodec{}
	in := persistedPayload{Data: []byte("z"), IssuedAt: time.Time{}}

	encoded, err := codec.encode(in)
	if err != nil {
		t.Fatalf("encode: %v", err)
	}

	var out persistedPayload
	if err := codec.decode(encoded, &out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !out.IssuedAt.IsZero() {
		t.Errorf("zero time round trip = %v, want zero value", out.IssuedAt)
	}
}

func TestEncodeDecodeNestedStructRoundTrip(t *testing.T) {
	codec := payloadCodec{}
	in := persistedPayload{
		Data:     []byte("nested"),
		IssuedAt: time.Unix(1754000000, 0).UTC(),
		Meta:     meta{ClientID: "client-1", Scopes: []string{"openid", "offline"}},
	}

	encoded, err := codec.encode(in)
	if err != nil {
		t.Fatalf("encode: %v", err)
	}

	var out persistedPayload
	if err := codec.decode(encoded, &out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if out.Meta.ClientID != in.Meta.ClientID || len(out.Meta.Scopes) != 2 ||
		out.Meta.Scopes[0] != "openid" || out.Meta.Scopes[1] != "offline" {
		t.Errorf("nested struct round trip = %+v, want %+v", out.Meta, in.Meta)
	}
	if !out.IssuedAt.Equal(in.IssuedAt) {
		t.Errorf("time round trip = %v, want %v", out.IssuedAt, in.IssuedAt)
	}
}
