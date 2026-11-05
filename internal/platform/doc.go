// Package platform provides shared infrastructure: configuration, logging,
// database and Redis connectivity, migration runner, clock, ID generation,
// and metrics setup. It is the foundation layer that all other internal
// packages depend on. It must never import from other internal packages.
package platform
