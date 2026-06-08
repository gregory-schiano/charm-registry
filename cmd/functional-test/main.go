// Command functional-test runs all functional scenarios against a running
// charm-registry instance and reports results. Designed for use by spread
// tests and charm integration suites that need to exercise shared endpoints
// without the Go test runner.
//
// Environment variables:
//
//	FTEST_API_URL       – API base URL           (default: http://localhost:8080)
//	FTEST_OCI_URL       – OCI registry URL       (default: https://localhost:15000)
//	FTEST_OCI_CERT_PATH – OCI CA cert PEM path
//	FTEST_ADMIN_SUBJECT – dev-auth subject        (default: "admin")
//	FTEST_ADMIN_USER    – dev-auth username        (default: "admin")
//
// Exit 0 if all scenarios pass, 1 otherwise.
package main

import (
	"fmt"
	"os"

	"github.com/gschiano/charm-registry/tests/functional"
)

func main() {
	c := functional.NewClientFromEnv()
	cfg := functional.ConfigFromEnv()
	fmt.Printf("functional-test: API=%s OCI=%s admin=%s/%s\n",
		cfg.APIURL, cfg.OCIURL, cfg.AdminSubject, cfg.AdminUsername)

	results := make([]functional.ScenarioResult, 0, 20)
	for _, fn := range functional.AllScenarios() {
		r := fn(c)
		results = append(results, r)
	}

	passed, failed := 0, 0
	for _, r := range results {
		if r.Passed {
			passed++
			fmt.Printf("  PASS  %s\n", r.Name)
		} else {
			failed++
			fmt.Printf("  FAIL  %s: %s\n", r.Name, r.Message)
		}
	}

	fmt.Printf("\n%d/%d passed, %d failed\n", passed, len(results), failed)
	if failed > 0 {
		os.Exit(1)
	}
}
