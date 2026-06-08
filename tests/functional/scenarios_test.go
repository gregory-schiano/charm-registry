//go:build functional

package functional_test

import (
	"os"
	"testing"

	"github.com/gschiano/charm-registry/tests/functional"
)

func TestMain(m *testing.M) {
	os.Exit(m.Run())
}

func TestHealthReadiness(t *testing.T) {
	runScenario(t, functional.ScenarioHealthReadiness)
}

func TestRootDocument(t *testing.T) {
	runScenario(t, functional.ScenarioRootDocument)
}

func TestOpenAPIServed(t *testing.T) {
	runScenario(t, functional.ScenarioOpenAPIServed)
}

func TestDocsPage(t *testing.T) {
	runScenario(t, functional.ScenarioDocsPage)
}

func TestDevAuthWhoami(t *testing.T) {
	runScenario(t, functional.ScenarioDevAuthWhoami)
}

func TestTokenIssueExchangeRevoke(t *testing.T) {
	runScenario(t, functional.ScenarioTokenIssueExchangeRevoke)
}

func TestPackageRegister(t *testing.T) {
	runScenario(t, functional.ScenarioPackageRegister)
}

func TestRevisionUploadPush(t *testing.T) {
	runScenario(t, functional.ScenarioRevisionUploadPush)
}

func TestReleaseChannel(t *testing.T) {
	runScenario(t, functional.ScenarioReleaseChannel)
}

func TestV2Info(t *testing.T) {
	runScenario(t, functional.ScenarioV2Info)
}

func TestResourceList(t *testing.T) {
	runScenario(t, functional.ScenarioResourceList)
}

func TestResourceRevisionLifecycle(t *testing.T) {
	runScenario(t, functional.ScenarioResourceRevisionLifecycle)
}

func TestResourceDownload(t *testing.T) {
	runScenario(t, functional.ScenarioResourceDownload)
}

func TestSyncListRules(t *testing.T) {
	runScenario(t, functional.ScenarioSyncListRules)
}

func TestSyncAddDeleteRule(t *testing.T) {
	runScenario(t, functional.ScenarioSyncAddDeleteRule)
}

func TestOCIRegistryV2(t *testing.T) {
	runScenario(t, functional.ScenarioOCIRegistryV2)
}

// TestAll runs every scenario sequentially (useful as a quick smoke test).
func TestAll(t *testing.T) {
	c := functional.NewClientFromEnv()
	results := functional.AllScenarios()
	for _, fn := range results {
		r := fn(c)
		t.Run(r.Name, func(t *testing.T) {
			if !r.Passed {
				t.Fatalf("scenario %s failed: %s", r.Name, r.Message)
			}
		})
	}
}

// runScenario executes a single scenario function and fails the test if
// the scenario did not pass.
func runScenario(t *testing.T, fn func(*functional.Client) functional.ScenarioResult) {
	t.Helper()
	c := functional.NewClientFromEnv()
	r := fn(c)
	if !r.Passed {
		t.Fatalf("%s: %s", r.Name, r.Message)
	}
	t.Logf("%s: %s", r.Name, r.Message)
}
