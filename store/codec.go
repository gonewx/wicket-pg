// Copyright 2026 Decker.
// This file is part of wicket-pg, an independent original work.

package store

import (
	"encoding/base64"
	"encoding/json"
)

// payloadVersion is the version recorded in every payload container. The
// adapter writes only version 1.
const payloadVersion = 1

// payloadContainer is the adapter-owned JSON envelope used to persist model
// payloads. The field shape mirrors the port's container contract, but the
// struct is defined here on purpose: the adapter never imports a container
// type from the upstream module.
type payloadContainer struct {
	Version       int    `json:"version"`
	DataProtected bool   `json:"dataProtected"`
	Payload       string `json:"payload"`
}

// payloadCodec encodes and decodes model payloads through the JSON envelope.
// It is stateless; every store shares one instance via the base.
type payloadCodec struct{}

// encode serializes v to JSON, base64-encodes those bytes into the container
// payload field, and serializes the container itself. A []byte model field is
// therefore base64-encoded twice: once by encoding/json and once here. The
// round trip is lossless.
func (payloadCodec) encode(v any) ([]byte, error) {
	model, err := json.Marshal(v)
	if err != nil {
		return nil, err
	}
	container := payloadContainer{
		Version:       payloadVersion,
		DataProtected: false,
		Payload:       base64.StdEncoding.EncodeToString(model),
	}
	return json.Marshal(container)
}

// decode reverses encode: it parses the container, base64-decodes the
// payload field, and unmarshals the model bytes into v.
func (payloadCodec) decode(data []byte, v any) error {
	var container payloadContainer
	if err := json.Unmarshal(data, &container); err != nil {
		return err
	}
	model, err := base64.StdEncoding.DecodeString(container.Payload)
	if err != nil {
		return err
	}
	return json.Unmarshal(model, v)
}
