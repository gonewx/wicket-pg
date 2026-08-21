module github.com/gonewx/wicket-pg

go 1.27

// This module requires the published wicket module at v0.1.4 — the newest
// v0.1.x at release time, per this repo's release discipline. v0.1.3 was the
// first release whose ClaimsIdentity JSON round trip is lossless (v0.1.2's
// plain encoding/json codec silently dropped stored subject claims); v0.1.4
// carries no production code changes on top of it, only a toolchain pin bump
// to the Go 1.27.0 release and doc updates. Port sentinel errors and suite
// entry points are imported from wicket; this module never redefines them.

require (
	github.com/gonewx/wicket v0.1.4
	github.com/jackc/pgx/v5 v5.10.0
	gopkg.in/yaml.v3 v3.0.1
)

require (
	github.com/decred/dcrd/dcrec/secp256k1/v4 v4.4.1 // indirect
	github.com/goccy/go-json v0.10.6 // indirect
	github.com/jackc/pgpassfile v1.0.0 // indirect
	github.com/jackc/pgservicefile v0.0.0-20240606120523-5a60cdf6a761 // indirect
	github.com/jackc/puddle/v2 v2.2.2 // indirect
	github.com/kr/text v0.2.0 // indirect
	github.com/lestrrat-go/blackmagic v1.0.4 // indirect
	github.com/lestrrat-go/dsig v1.3.0 // indirect
	github.com/lestrrat-go/dsig-secp256k1 v1.0.0 // indirect
	github.com/lestrrat-go/httpcc v1.0.1 // indirect
	github.com/lestrrat-go/httprc/v3 v3.0.6 // indirect
	github.com/lestrrat-go/jwx/v3 v3.2.0 // indirect
	github.com/lestrrat-go/option/v2 v2.0.0 // indirect
	github.com/rogpeppe/go-internal v1.16.0 // indirect
	github.com/segmentio/asm v1.2.1 // indirect
	github.com/valyala/fastjson v1.6.10 // indirect
	golang.org/x/crypto v0.54.0 // indirect
	golang.org/x/sync v0.22.0 // indirect
	golang.org/x/sys v0.47.0 // indirect
	golang.org/x/text v0.40.0 // indirect
)
