module github.com/gonewx/wicket-pg

go 1.27rc1

// The wicket dependency is added in the handoff step that follows the
// publication of wicket's first semver tag. Until then this module builds
// against the standard library only, which is all the lineage gates need.

require github.com/jackc/pgx/v5 v5.10.0

require (
	github.com/jackc/pgpassfile v1.0.0 // indirect
	github.com/jackc/pgservicefile v0.0.0-20240606120523-5a60cdf6a761 // indirect
	github.com/jackc/puddle/v2 v2.2.2 // indirect
	golang.org/x/sync v0.17.0 // indirect
	golang.org/x/text v0.29.0 // indirect
)
