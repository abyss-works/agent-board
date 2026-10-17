// Package e2e holds the end-to-end suite that exercises a running agb
// server over HTTP against spec/api.md.
//
// The suite is behind the build tag e2e so that plain "go test ./..." does
// not run it. It needs a server reachable at AGB_E2E_URL and its token in
// AGB_E2E_TOKEN:
//
//	go test -tags e2e -count=1 ./e2e/
//
// Every run creates projects with a fresh random key prefix, so the suite
// can be run repeatedly against the same database. TestPersistence is the
// exception: it runs only when AGB_E2E_PERSIST_KEY names a project key and
// AGB_E2E_PERSIST_PHASE is "write" (before a server restart) or "verify"
// (after it).
package e2e
