module github.com/gonewx/wicket-pg

go 1.27rc1

// The wicket dependency is added in the handoff step that follows the
// publication of wicket's first semver tag. Until then this module builds
// against the standard library only, which is all the lineage gates need.
