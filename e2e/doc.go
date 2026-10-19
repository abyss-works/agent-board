// Package e2e holds the end-to-end suite that exercises a running agb
// server over HTTP against spec/api.md.
//
// The suite is behind the build tag e2e so that plain "go test ./..." does
// not run it. It needs a server reachable at AGB_E2E_URL and its token in
// AGB_E2E_TOKEN:
//
//	go test -tags e2e -count=1 ./e2e/
//
// AGB_E2E_TOKEN is the shared token, which only user actors may use. The
// server must declare owner as a user and the agents the suite acts as:
//
//	AGB_ACTORS=owner:user,claude:agent,codex:agent,gemini:agent,opencode:agent,hermes:agent
//
// Before the first test the suite logs in every agent the server declares,
// the way the CLI will: it asks for a device authorization, approves it as
// owner with the shared token and exchanges the device code. Each agent then
// works with the actor token it received. Every run logs in again on the same
// device, which revokes the tokens of the run before.
//
// Every run creates projects with a fresh random key prefix, so the suite
// can be run repeatedly against the same database. TestPersistence is the
// exception: it runs only when AGB_E2E_PERSIST_KEY names a project key and
// AGB_E2E_PERSIST_PHASE is "write" (before a server restart) or "verify"
// (after it).
package e2e
