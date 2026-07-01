package functional

import (
	"encoding/base64"
	"fmt"
	"net/http"
	"strings"
)

// ---------- Scenario results ----------

// ScenarioResult carries the outcome of a single scenario execution.
type ScenarioResult struct {
	// Name is the human-readable scenario identifier (e.g. "health/readiness").
	Name string

	// Passed is true when every assertion in the scenario succeeded.
	Passed bool

	// Message is a human-readable summary of the outcome, or the first
	// assertion failure message.
	Message string
}

// ---------- Health / readiness ----------

// ScenarioHealthReadiness checks GET /healthz and GET /readyz return 200
// with the expected JSON payloads.
func ScenarioHealthReadiness(c *Client) ScenarioResult {
	name := "health/readiness"

	// /healthz
	resp, err := c.DoRequest("GET", "/healthz", "", "")
	if err != nil {
		return fail(name, "GET /healthz: %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		body, _ := ReadAllBytes(resp)
		return fail(name, "GET /healthz: status %d, body: %s", resp.StatusCode, string(body))
	}
	body, err := ReadJSON(resp)
	if err != nil {
		return fail(name, "GET /healthz decode: %v", err)
	}
	if body["status"] != "ok" {
		return fail(name, "GET /healthz: expected status=ok, got %v", body["status"])
	}

	// /readyz
	resp2, err := c.DoRequest("GET", "/readyz", "", "")
	if err != nil {
		return fail(name, "GET /readyz: %v", err)
	}
	if resp2.StatusCode != http.StatusOK {
		body2, _ := ReadAllBytes(resp2)
		return fail(name, "GET /readyz: status %d, body: %s", resp2.StatusCode, string(body2))
	}
	body2, err := ReadJSON(resp2)
	if err != nil {
		return fail(name, "GET /readyz decode: %v", err)
	}
	if body2["status"] != "ready" {
		return fail(name, "GET /readyz: expected status=ready, got %v", body2["status"])
	}

	return pass(name)
}

// ScenarioRootDocument checks the root document returns the expected service name.
func ScenarioRootDocument(c *Client) ScenarioResult {
	name := "health/root-document"
	resp, err := c.DoRequest("GET", "/", "", "")
	if err != nil {
		return fail(name, "GET /: %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		body, _ := ReadAllBytes(resp)
		return fail(name, "GET /: status %d, body: %s", resp.StatusCode, string(body))
	}
	body, err := ReadJSON(resp)
	if err != nil {
		return fail(name, "GET / decode: %v", err)
	}
	if body["service-name"] != "private-charm-registry" {
		return fail(name, "GET /: expected service-name=private-charm-registry, got %v", body["service-name"])
	}
	return pass(name)
}

// ScenarioOpenAPIServed checks that the OpenAPI spec is served at /openapi.yaml.
func ScenarioOpenAPIServed(c *Client) ScenarioResult {
	name := "health/openapi"
	resp, err := c.DoRequest("GET", "/openapi.yaml", "", "")
	if err != nil {
		return fail(name, "GET /openapi.yaml: %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		body, _ := ReadAllBytes(resp)
		return fail(name, "GET /openapi.yaml: status %d, body: %s", resp.StatusCode, string(body))
	}
	ct := resp.Header.Get("Content-Type")
	if ct != "application/yaml" {
		return fail(name, "GET /openapi.yaml: expected Content-Type=application/yaml, got %s", ct)
	}
	raw, err := ReadAllBytes(resp)
	if err != nil {
		return fail(name, "reading /openapi.yaml: %v", err)
	}
	if !strings.Contains(string(raw), "openapi:") {
		return fail(name, "/openapi.yaml body missing 'openapi:' header")
	}
	return pass(name)
}

// ScenarioDocsPage checks that the docs page is rendered at /docs.
func ScenarioDocsPage(c *Client) ScenarioResult {
	name := "health/docs-page"
	resp, err := c.DoRequest("GET", "/docs", "", "")
	if err != nil {
		return fail(name, "GET /docs: %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		_, _ = ReadAllBytes(resp)
		return fail(name, "GET /docs: status %d", resp.StatusCode)
	}
	_, _ = ReadAllBytes(resp) // drain
	return pass(name)
}

// ---------- Auth / tokens ----------

// ScenarioDevAuthWhoami checks that dev-auth credentials work and /v1/whoami
// returns an authenticated identity.
func ScenarioDevAuthWhoami(c *Client) ScenarioResult {
	name := "auth/dev-auth-whoami"
	auth := c.AdminAuthHeader()
	resp, err := c.DoRequest("GET", "/v1/whoami", "", auth)
	if err != nil {
		return fail(name, "GET /v1/whoami: %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		body, _ := ReadAllBytes(resp)
		return fail(name, "GET /v1/whoami: status %d, body: %s", resp.StatusCode, string(body))
	}
	body, err := ReadJSON(resp)
	if err != nil {
		return fail(name, "decode /v1/whoami: %v", err)
	}
	if body["id"] == nil || body["id"] == "" {
		return fail(name, "/v1/whoami: expected non-empty id field, got %v", body)
	}
	if body["username"] != c.cfg.AdminUsername {
		return fail(name, "/v1/whoami: expected username %q, got %v", c.cfg.AdminUsername, body["username"])
	}
	return pass(name)
}

// ScenarioTokenIssueExchangeRevoke issues a token, exchanges it, and revokes it.
// Returns the scenario result.
func ScenarioTokenIssueExchangeRevoke(c *Client) ScenarioResult {
	name := "auth/token-issue-exchange-revoke"
	auth := c.AdminAuthHeader()

	// Issue
	resp, err := c.DoRequest("POST", "/v1/tokens", `{"description":"functional-test"}`, auth)
	if err != nil {
		return fail(name, "POST /v1/tokens: %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		body, _ := ReadAllBytes(resp)
		return fail(name, "POST /v1/tokens: status %d, body: %s", resp.StatusCode, string(body))
	}
	issueBody, err := ReadJSON(resp)
	if err != nil {
		return fail(name, "decode issue response: %v", err)
	}
	macaroonStr, ok := issueBody["macaroon"].(string)
	if !ok || macaroonStr == "" {
		return fail(name, "issue response missing macaroon string")
	}

	// Exchange
	macaroonsHeader := base64.URLEncoding.EncodeToString([]byte("[" + macaroonStr + "]"))
	resp2, err := c.DoRequestRaw(
		"POST",
		"/v1/tokens/exchange",
		map[string]string{
			"Content-Type": "application/json",
			"Macaroons":    macaroonsHeader,
		},
		strings.NewReader("{}"),
	)
	if err != nil {
		return fail(name, "POST /v1/tokens/exchange: %v", err)
	}
	if resp2.StatusCode != http.StatusOK {
		body2, _ := ReadAllBytes(resp2)
		return fail(name, "exchange: status %d, body: %s", resp2.StatusCode, string(body2))
	}
	exchBody, err := ReadJSON(resp2)
	if err != nil {
		return fail(name, "decode exchange response: %v", err)
	}
	if _, ok := exchBody["macaroon"].(string); !ok {
		return fail(name, "exchange response missing macaroon")
	}

	// Get session ID via list
	resp3, err := c.DoRequest("GET", "/v1/tokens", "", auth)
	if err != nil {
		return fail(name, "GET /v1/tokens: %v", err)
	}
	if resp3.StatusCode != http.StatusOK {
		body3, _ := ReadAllBytes(resp3)
		return fail(name, "list tokens: status %d, body: %s", resp3.StatusCode, string(body3))
	}
	listBody, err := ReadJSON(resp3)
	if err != nil {
		return fail(name, "decode list tokens: %v", err)
	}
	macaroons, ok := listBody["macaroons"].([]any)
	if !ok || len(macaroons) == 0 {
		return fail(name, "list tokens: empty or missing macaroons array")
	}
	last := macaroons[len(macaroons)-1].(map[string]any)
	sessionID, ok := last["session-id"].(string)
	if !ok || sessionID == "" {
		return fail(name, "last token entry missing session-id")
	}

	// Revoke
	revokeBody := fmt.Sprintf(`{"session-id":"%s"}`, sessionID)
	resp4, err := c.DoRequest("POST", "/v1/tokens/revoke", revokeBody, auth)
	if err != nil {
		return fail(name, "POST /v1/tokens/revoke: %v", err)
	}
	if resp4.StatusCode != http.StatusOK {
		body4, _ := ReadAllBytes(resp4)
		return fail(name, "revoke: status %d, body: %s", resp4.StatusCode, string(body4))
	}
	_, _ = ReadAllBytes(resp4) // drain

	return pass(name)
}

// ---------- Package registration ----------

// ScenarioPackageRegister registers a charm package and reads it back.
func ScenarioPackageRegister(c *Client) ScenarioResult {
	name := "packages/register"
	auth := c.AdminAuthHeader()
	pkgName := UniqueName("register")

	// Register
	body := fmt.Sprintf(`{"name":"%s","type":"charm"}`, pkgName)
	resp, err := c.DoRequest("POST", "/v1/charm", body, auth)
	if err != nil {
		return fail(name, "POST /v1/charm: %v", err)
	}
	if resp.StatusCode != http.StatusCreated {
		respBody, _ := ReadAllBytes(resp)
		return fail(name, "register: status %d, body: %s", resp.StatusCode, string(respBody))
	}
	regBody, err := ReadJSON(resp)
	if err != nil {
		return fail(name, "decode register response: %v", err)
	}
	pkgID, ok := regBody["id"].(string)
	if !ok || pkgID == "" {
		return fail(name, "register response missing string id")
	}

	// Read back
	resp2, err := c.DoRequest("GET", "/v1/charm/"+pkgName, "", auth)
	if err != nil {
		return fail(name, "GET /v1/charm/%s: %v", pkgName, err)
	}
	if resp2.StatusCode != http.StatusOK {
		body2, _ := ReadAllBytes(resp2)
		return fail(name, "read-back: status %d, body: %s", resp2.StatusCode, string(body2))
	}
	readBody, err := ReadJSON(resp2)
	if err != nil {
		return fail(name, "decode read-back: %v", err)
	}
	meta, ok := readBody["metadata"].(map[string]any)
	if !ok {
		return fail(name, "read-back missing metadata map")
	}
	if meta["name"] != pkgName {
		return fail(name, "read-back name %v != %s", meta["name"], pkgName)
	}
	if meta["id"] != pkgID {
		return fail(name, "read-back id %v != %s", meta["id"], pkgID)
	}
	return pass(name)
}

// ---------- Revision upload/download ----------

// ScenarioRevisionUploadPush uploads a charm archive and pushes a revision.
func ScenarioRevisionUploadPush(c *Client) ScenarioResult {
	name := "revisions/upload-push"
	auth := c.AdminAuthHeader()
	pkgName := UniqueName("revision")

	// Register
	regBody := fmt.Sprintf(`{"name":"%s","type":"charm"}`, pkgName)
	resp, err := c.DoRequest("POST", "/v1/charm", regBody, auth)
	if err != nil {
		return fail(name, "register: %v", err)
	}
	if resp.StatusCode != http.StatusCreated {
		respBody, _ := ReadAllBytes(resp)
		return fail(name, "register: status %d, body: %s", resp.StatusCode, string(respBody))
	}
	_, _ = ReadAllBytes(resp)

	// Build archive and upload
	archive, err := BuildTestCharmArchive(pkgName)
	if err != nil {
		return fail(name, "build archive: %v", err)
	}
	uploadBody, uploadStatus, err := c.UploadMultipart("/unscanned-upload/", "binary", pkgName+".charm", archive, auth)
	if err != nil {
		return fail(name, "upload: %v", err)
	}
	if uploadStatus != http.StatusOK && uploadStatus != http.StatusCreated {
		return fail(name, "upload: status %d, body: %v", uploadStatus, uploadBody)
	}
	uploadID, ok := uploadBody["upload_id"].(string)
	if !ok || uploadID == "" {
		return fail(name, "upload response missing upload_id")
	}

	// Push revision
	revBody := fmt.Sprintf(`{"upload-id":"%s"}`, uploadID)
	resp2, err := c.DoRequest("POST", "/v1/charm/"+pkgName+"/revisions", revBody, auth)
	if err != nil {
		return fail(name, "push revision: %v", err)
	}
	if resp2.StatusCode != http.StatusCreated {
		body2, _ := ReadAllBytes(resp2)
		return fail(name, "push revision: status %d, body: %s", resp2.StatusCode, string(body2))
	}
	_, _ = ReadAllBytes(resp2)

	// List revisions
	resp3, err := c.DoRequest("GET", "/v1/charm/"+pkgName+"/revisions", "", auth)
	if err != nil {
		return fail(name, "list revisions: %v", err)
	}
	if resp3.StatusCode != http.StatusOK {
		body3, _ := ReadAllBytes(resp3)
		return fail(name, "list revisions: status %d, body: %s", resp3.StatusCode, string(body3))
	}
	listBody, err := ReadJSON(resp3)
	if err != nil {
		return fail(name, "decode revisions: %v", err)
	}
	revisions, ok := listBody["revisions"].([]any)
	if !ok || len(revisions) == 0 {
		return fail(name, "expected at least one revision")
	}
	rev0, _ := revisions[0].(map[string]any)
	if rev0["revision"] != float64(1) {
		return fail(name, "first revision should be 1, got %v", rev0["revision"])
	}
	return pass(name)
}

// ---------- Release / channel ----------

// ScenarioReleaseChannel uploads a revision and releases it to a channel.
func ScenarioReleaseChannel(c *Client) ScenarioResult {
	name := "releases/channel"
	auth := c.AdminAuthHeader()
	pkgName := UniqueName("release")

	// Register
	regBody := fmt.Sprintf(`{"name":"%s","type":"charm"}`, pkgName)
	resp, err := c.DoRequest("POST", "/v1/charm", regBody, auth)
	if err != nil {
		return fail(name, "register: %v", err)
	}
	if resp.StatusCode != http.StatusCreated {
		respBody, _ := ReadAllBytes(resp)
		return fail(name, "register: status %d, body: %s", resp.StatusCode, string(respBody))
	}
	_, _ = ReadAllBytes(resp)

	// Upload + push
	archive, err := BuildTestCharmArchive(pkgName)
	if err != nil {
		return fail(name, "build archive: %v", err)
	}
	uploadBody, _, err := c.UploadMultipart("/unscanned-upload/", "binary", pkgName+".charm", archive, auth)
	if err != nil {
		return fail(name, "upload: %v", err)
	}
	uploadID, ok := uploadBody["upload_id"].(string)
	if !ok || uploadID == "" {
		return fail(name, "upload response missing upload_id")
	}

	revBody := fmt.Sprintf(`{"upload-id":"%s"}`, uploadID)
	resp2, err := c.DoRequest("POST", "/v1/charm/"+pkgName+"/revisions", revBody, auth)
	if err != nil {
		return fail(name, "push revision: %v", err)
	}
	if resp2.StatusCode != http.StatusCreated {
		body2, _ := ReadAllBytes(resp2)
		return fail(name, "push: status %d, body: %s", resp2.StatusCode, string(body2))
	}
	_, _ = ReadAllBytes(resp2)

	// Release to channel
	relBody := fmt.Sprintf(`[{"revision":1,"channel":"latest/stable"}]`)
	resp3, err := c.DoRequest("POST", "/v1/charm/"+pkgName+"/releases", relBody, auth)
	if err != nil {
		return fail(name, "release: %v", err)
	}
	if resp3.StatusCode != http.StatusCreated {
		body3, _ := ReadAllBytes(resp3)
		return fail(name, "release: status %d, body: %s", resp3.StatusCode, string(body3))
	}
	_, _ = ReadAllBytes(resp3)

	// List releases and verify
	resp4, err := c.DoRequest("GET", "/v1/charm/"+pkgName+"/releases", "", auth)
	if err != nil {
		return fail(name, "list releases: %v", err)
	}
	if resp4.StatusCode != http.StatusOK {
		body4, _ := ReadAllBytes(resp4)
		return fail(name, "list releases: status %d, body: %s", resp4.StatusCode, string(body4))
	}
	listBody, err := ReadJSON(resp4)
	if err != nil {
		return fail(name, "decode releases: %v", err)
	}
	channelMap, ok := listBody["channel-map"].([]any)
	if !ok || len(channelMap) == 0 {
		return fail(name, "expected at least one channel-map entry")
	}
	return pass(name)
}

// ---------- V2 info ----------

// ScenarioV2Info checks the v2 info endpoint reflects registered and released data.
func ScenarioV2Info(c *Client) ScenarioResult {
	name := "v2/info-after-release"
	auth := c.AdminAuthHeader()
	pkgName := UniqueName("v2info")

	// Register
	regBody := fmt.Sprintf(`{"name":"%s","type":"charm"}`, pkgName)
	resp, err := c.DoRequest("POST", "/v1/charm", regBody, auth)
	if err != nil {
		return fail(name, "register: %v", err)
	}
	if resp.StatusCode != http.StatusCreated {
		respBody, _ := ReadAllBytes(resp)
		return fail(name, "register: status %d, body: %s", resp.StatusCode, string(respBody))
	}
	_, _ = ReadAllBytes(resp)

	// Upload + push + release
	archive, err := BuildTestCharmArchive(pkgName)
	if err != nil {
		return fail(name, "build archive: %v", err)
	}
	uploadBody, _, err := c.UploadMultipart("/unscanned-upload/", "binary", pkgName+".charm", archive, auth)
	if err != nil {
		return fail(name, "upload: %v", err)
	}
	uploadID, _ := uploadBody["upload_id"].(string)
	revBody := fmt.Sprintf(`{"upload-id":"%s"}`, uploadID)
	resp2, err := c.DoRequest("POST", "/v1/charm/"+pkgName+"/revisions", revBody, auth)
	if err != nil {
		return fail(name, "push: %v", err)
	}
	_, _ = ReadAllBytes(resp2)

	relBody := `[{"revision":1,"channel":"latest/stable"}]`
	resp3, err := c.DoRequest("POST", "/v1/charm/"+pkgName+"/releases", relBody, auth)
	if err != nil {
		return fail(name, "release: %v", err)
	}
	_, _ = ReadAllBytes(resp3)

	// V2 info
	resp4, err := c.DoRequest("GET", "/v2/charms/info/"+pkgName, "", auth)
	if err != nil {
		return fail(name, "GET /v2/charms/info/%s: %v", pkgName, err)
	}
	if resp4.StatusCode != http.StatusOK {
		body4, _ := ReadAllBytes(resp4)
		return fail(name, "v2 info: status %d, body: %s", resp4.StatusCode, string(body4))
	}
	infoBody, err := ReadJSON(resp4)
	if err != nil {
		return fail(name, "decode v2 info: %v", err)
	}
	if infoBody["name"] != pkgName {
		return fail(name, "v2 info name %v != %s", infoBody["name"], pkgName)
	}
	return pass(name)
}

// ---------- Resources ----------

// ScenarioResourceList registers a charm declaring resources in metadata.yaml,
// pushes a revision, and verifies GET /v1/charm/{name}/resources returns them.
func ScenarioResourceList(c *Client) ScenarioResult {
	name := "resources/list"
	auth := c.AdminAuthHeader()
	pkgName := UniqueName("res-list")

	// Register charm.
	regBody := fmt.Sprintf(`{"name":"%s","type":"charm"}`, pkgName)
	resp, err := c.DoRequest("POST", "/v1/charm", regBody, auth)
	if err != nil {
		return fail(name, "register: %v", err)
	}
	if resp.StatusCode != http.StatusCreated {
		body, _ := ReadAllBytes(resp)
		return fail(name, "register: status %d, body: %s", resp.StatusCode, string(body))
	}
	_, _ = ReadAllBytes(resp)

	// Upload charm archive with resource declarations.
	archive, err := BuildTestCharmArchiveWithResources(pkgName)
	if err != nil {
		return fail(name, "build archive: %v", err)
	}
	uploadBody, uploadStatus, err := c.UploadMultipart("/unscanned-upload/", "binary", pkgName+".charm", archive, auth)
	if err != nil {
		return fail(name, "upload charm: %v", err)
	}
	if uploadStatus != http.StatusOK && uploadStatus != http.StatusCreated {
		return fail(name, "upload charm: status %d, body: %v", uploadStatus, uploadBody)
	}
	uploadID, ok := uploadBody["upload_id"].(string)
	if !ok || uploadID == "" {
		return fail(name, "upload response missing upload_id")
	}

	// Push charm revision.
	revBody := fmt.Sprintf(`{"upload-id":"%s"}`, uploadID)
	resp2, err := c.DoRequest("POST", "/v1/charm/"+pkgName+"/revisions", revBody, auth)
	if err != nil {
		return fail(name, "push revision: %v", err)
	}
	if resp2.StatusCode != http.StatusCreated {
		body2, _ := ReadAllBytes(resp2)
		return fail(name, "push revision: status %d, body: %s", resp2.StatusCode, string(body2))
	}
	_, _ = ReadAllBytes(resp2)

	// List resources.
	resp3, err := c.DoRequest("GET", "/v1/charm/"+pkgName+"/resources", "", auth)
	if err != nil {
		return fail(name, "list resources: %v", err)
	}
	if resp3.StatusCode != http.StatusOK {
		body3, _ := ReadAllBytes(resp3)
		return fail(name, "list resources: status %d, body: %s", resp3.StatusCode, string(body3))
	}
	listBody, err := ReadJSON(resp3)
	if err != nil {
		return fail(name, "decode resources: %v", err)
	}
	resources, ok := listBody["resources"].([]any)
	if !ok || len(resources) == 0 {
		return fail(name, "expected at least one declared resource, got %v", listBody["resources"])
	}
	first, ok := resources[0].(map[string]any)
	if !ok {
		return fail(name, "resource entry is not a map")
	}
	if first["name"] != "config" {
		return fail(name, "expected resource name 'config', got %v", first["name"])
	}
	if first["type"] != "file" {
		return fail(name, "expected resource type 'file', got %v", first["type"])
	}
	return pass(name)
}

// ScenarioResourceRevisionLifecycle pushes a resource file revision and lists
// the revisions to verify the round-trip.
func ScenarioResourceRevisionLifecycle(c *Client) ScenarioResult {
	name := "resources/revision-lifecycle"
	auth := c.AdminAuthHeader()
	pkgName := UniqueName("res-rev")

	// Register + upload charm with resources + push revision.
	regBody := fmt.Sprintf(`{"name":"%s","type":"charm"}`, pkgName)
	resp, err := c.DoRequest("POST", "/v1/charm", regBody, auth)
	if err != nil {
		return fail(name, "register: %v", err)
	}
	if resp.StatusCode != http.StatusCreated {
		body, _ := ReadAllBytes(resp)
		return fail(name, "register: status %d, body: %s", resp.StatusCode, string(body))
	}
	_, _ = ReadAllBytes(resp)

	archive, err := BuildTestCharmArchiveWithResources(pkgName)
	if err != nil {
		return fail(name, "build archive: %v", err)
	}
	uploadBody, _, err := c.UploadMultipart("/unscanned-upload/", "binary", pkgName+".charm", archive, auth)
	if err != nil {
		return fail(name, "upload charm: %v", err)
	}
	uploadID, _ := uploadBody["upload_id"].(string)
	revBody := fmt.Sprintf(`{"upload-id":"%s"}`, uploadID)
	resp2, err := c.DoRequest("POST", "/v1/charm/"+pkgName+"/revisions", revBody, auth)
	if err != nil {
		return fail(name, "push charm revision: %v", err)
	}
	if resp2.StatusCode != http.StatusCreated {
		body2, _ := ReadAllBytes(resp2)
		return fail(name, "push charm revision: status %d, body: %s", resp2.StatusCode, string(body2))
	}
	_, _ = ReadAllBytes(resp2)

	// Upload a resource file.
	resourceContent := []byte("key: value\n")
	resUploadBody, resStatus, err := c.UploadMultipart("/unscanned-upload/", "binary", "config.yaml", resourceContent, auth)
	if err != nil {
		return fail(name, "upload resource: %v", err)
	}
	if resStatus != http.StatusOK && resStatus != http.StatusCreated {
		return fail(name, "upload resource: status %d", resStatus)
	}
	resUploadID, ok := resUploadBody["upload_id"].(string)
	if !ok || resUploadID == "" {
		return fail(name, "resource upload missing upload_id")
	}

	// Push resource revision.
	pushBody := fmt.Sprintf(`{"upload-id":"%s"}`, resUploadID)
	resp3, err := c.DoRequest("POST", "/v1/charm/"+pkgName+"/resources/config/revisions", pushBody, auth)
	if err != nil {
		return fail(name, "push resource revision: %v", err)
	}
	if resp3.StatusCode != http.StatusCreated {
		body3, _ := ReadAllBytes(resp3)
		return fail(name, "push resource revision: status %d, body: %s", resp3.StatusCode, string(body3))
	}
	_, _ = ReadAllBytes(resp3)

	// List resource revisions.
	resp4, err := c.DoRequest("GET", "/v1/charm/"+pkgName+"/resources/config/revisions", "", auth)
	if err != nil {
		return fail(name, "list resource revisions: %v", err)
	}
	if resp4.StatusCode != http.StatusOK {
		body4, _ := ReadAllBytes(resp4)
		return fail(name, "list resource revisions: status %d, body: %s", resp4.StatusCode, string(body4))
	}
	listBody, err := ReadJSON(resp4)
	if err != nil {
		return fail(name, "decode resource revisions: %v", err)
	}
	revisions, ok := listBody["revisions"].([]any)
	if !ok || len(revisions) == 0 {
		return fail(name, "expected at least one resource revision")
	}
	rev0, ok := revisions[0].(map[string]any)
	if !ok {
		return fail(name, "revision entry is not a map")
	}
	for _, field := range []string{"revision", "filename", "created-at"} {
		if _, exists := rev0[field]; !exists {
			return fail(name, "resource revision missing field %q", field)
		}
	}
	return pass(name)
}

// ScenarioResourceDownload pushes a resource and downloads it via the
// /api/v1/resources/download/{filename} endpoint, verifying content integrity.
func ScenarioResourceDownload(c *Client) ScenarioResult {
	name := "resources/download"
	auth := c.AdminAuthHeader()
	pkgName := UniqueName("res-dl")

	// Register + get package ID.
	regBody := fmt.Sprintf(`{"name":"%s","type":"charm"}`, pkgName)
	resp, err := c.DoRequest("POST", "/v1/charm", regBody, auth)
	if err != nil {
		return fail(name, "register: %v", err)
	}
	if resp.StatusCode != http.StatusCreated {
		body, _ := ReadAllBytes(resp)
		return fail(name, "register: status %d, body: %s", resp.StatusCode, string(body))
	}
	regResult, err := ReadJSON(resp)
	if err != nil {
		return fail(name, "decode register: %v", err)
	}
	pkgID, _ := regResult["id"].(string)

	// Upload charm with resources + push revision.
	archive, err := BuildTestCharmArchiveWithResources(pkgName)
	if err != nil {
		return fail(name, "build archive: %v", err)
	}
	uploadBody, _, err := c.UploadMultipart("/unscanned-upload/", "binary", pkgName+".charm", archive, auth)
	if err != nil {
		return fail(name, "upload charm: %v", err)
	}
	uploadID, _ := uploadBody["upload_id"].(string)
	revBody := fmt.Sprintf(`{"upload-id":"%s"}`, uploadID)
	resp2, err := c.DoRequest("POST", "/v1/charm/"+pkgName+"/revisions", revBody, auth)
	if err != nil {
		return fail(name, "push revision: %v", err)
	}
	_, _ = ReadAllBytes(resp2)

	// Upload resource file + push resource revision.
	resourceContent := []byte("key: value\n")
	resUploadBody, _, err := c.UploadMultipart("/unscanned-upload/", "binary", "config.yaml", resourceContent, auth)
	if err != nil {
		return fail(name, "upload resource: %v", err)
	}
	resUploadID, _ := resUploadBody["upload_id"].(string)
	pushBody := fmt.Sprintf(`{"upload-id":"%s"}`, resUploadID)
	resp3, err := c.DoRequest("POST", "/v1/charm/"+pkgName+"/resources/config/revisions", pushBody, auth)
	if err != nil {
		return fail(name, "push resource revision: %v", err)
	}
	_, _ = ReadAllBytes(resp3)

	// Determine resource revision number.
	resp4, err := c.DoRequest("GET", "/v1/charm/"+pkgName+"/resources/config/revisions", "", auth)
	if err != nil {
		return fail(name, "list resource revisions: %v", err)
	}
	listBody, err := ReadJSON(resp4)
	if err != nil {
		return fail(name, "decode revisions: %v", err)
	}
	revisions, _ := listBody["revisions"].([]any)
	if len(revisions) == 0 {
		return fail(name, "no resource revisions found for download")
	}
	revNum := int(revisions[0].(map[string]any)["revision"].(float64))

	// Download resource.
	downloadFilename := fmt.Sprintf("charm_%s.config_%d", pkgID, revNum)
	resp5, err := c.DoRequest("GET", "/api/v1/resources/download/"+downloadFilename, "", auth)
	if err != nil {
		return fail(name, "download resource: %v", err)
	}
	if resp5.StatusCode != http.StatusOK {
		body5, _ := ReadAllBytes(resp5)
		return fail(name, "download: status %d, body: %s", resp5.StatusCode, string(body5))
	}
	downloaded, err := ReadAllBytes(resp5)
	if err != nil {
		return fail(name, "read download: %v", err)
	}
	if string(downloaded) != string(resourceContent) {
		return fail(name, "downloaded content %q != uploaded %q", string(downloaded), string(resourceContent))
	}
	return pass(name)
}

// ---------- Charmhub sync rules ----------

// ScenarioSyncListRules lists Charmhub sync rules and verifies the endpoint
// returns a valid response with a rules array.
func ScenarioSyncListRules(c *Client) ScenarioResult {
	name := "sync/list-rules"
	auth := c.AdminAuthHeader()

	resp, err := c.DoRequest("GET", "/v1/admin/charmhub-sync", "", auth)
	if err != nil {
		return fail(name, "GET /v1/admin/charmhub-sync: %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		body, _ := ReadAllBytes(resp)
		return fail(name, "GET sync rules: status %d, body: %s", resp.StatusCode, string(body))
	}
	body, err := ReadJSON(resp)
	if err != nil {
		return fail(name, "decode sync rules: %v", err)
	}
	rules, ok := body["rules"].([]any)
	if !ok && body["rules"] != nil {
		return fail(name, "response missing 'rules' array, got: %v", body)
	}
	// Rules may be empty — that's fine for a clean instance.
	_ = rules
	return pass(name)
}

// ScenarioSyncAddDeleteRule adds a Charmhub sync rule, verifies it appears
// in the list, deletes it, and verifies it's gone.
func ScenarioSyncAddDeleteRule(c *Client) ScenarioResult {
	name := "sync/add-delete-rule"
	auth := c.AdminAuthHeader()
	ruleName := UniqueName("sync-rule")
	track := "latest"

	// Add sync rule.
	addBody := fmt.Sprintf(`{"name":"%s","track":"%s","bases":["ubuntu@22.04"],"architectures":["amd64"]}`, ruleName, track)
	resp, err := c.DoRequest("POST", "/v1/admin/charmhub-sync", addBody, auth)
	if err != nil {
		return fail(name, "POST add sync rule: %v", err)
	}
	if resp.StatusCode != http.StatusAccepted {
		body, _ := ReadAllBytes(resp)
		return fail(name, "add sync rule: status %d, body: %s", resp.StatusCode, string(body))
	}
	addResult, err := ReadJSON(resp)
	if err != nil {
		return fail(name, "decode add response: %v", err)
	}
	if addResult["name"] != ruleName {
		return fail(name, "add rule: name %v != %s", addResult["name"], ruleName)
	}

	// List and verify the rule appears.
	resp2, err := c.DoRequest("GET", "/v1/admin/charmhub-sync", "", auth)
	if err != nil {
		return fail(name, "list after add: %v", err)
	}
	if resp2.StatusCode != http.StatusOK {
		body2, _ := ReadAllBytes(resp2)
		return fail(name, "list after add: status %d, body: %s", resp2.StatusCode, string(body2))
	}
	listBody, err := ReadJSON(resp2)
	if err != nil {
		return fail(name, "decode list: %v", err)
	}
	rules, _ := listBody["rules"].([]any)
	found := false
	for _, r := range rules {
		rMap, ok := r.(map[string]any)
		if ok && rMap["name"] == ruleName {
			found = true
			break
		}
	}
	if !found {
		return fail(name, "added rule %q not found in list of %d rules", ruleName, len(rules))
	}

	// Delete the rule.
	delPath := fmt.Sprintf("/v1/admin/charmhub-sync/%s/%s", ruleName, track)
	resp3, err := c.DoRequest("DELETE", delPath, "", auth)
	if err != nil {
		return fail(name, "DELETE sync rule: %v", err)
	}
	if resp3.StatusCode != http.StatusAccepted {
		body3, _ := ReadAllBytes(resp3)
		return fail(name, "delete sync rule: status %d, body: %s", resp3.StatusCode, string(body3))
	}
	_, _ = ReadAllBytes(resp3)

	// List again and verify removal.
	resp4, err := c.DoRequest("GET", "/v1/admin/charmhub-sync", "", auth)
	if err != nil {
		return fail(name, "list after delete: %v", err)
	}
	if resp4.StatusCode != http.StatusOK {
		body4, _ := ReadAllBytes(resp4)
		return fail(name, "list after delete: status %d, body: %s", resp4.StatusCode, string(body4))
	}
	listBody2, err := ReadJSON(resp4)
	if err != nil {
		return fail(name, "decode list after delete: %v", err)
	}
	rules2, _ := listBody2["rules"].([]any)
	for _, r := range rules2 {
		rMap, ok := r.(map[string]any)
		if ok && rMap["name"] == ruleName && rMap["track"] == track {
			if rMap["status"] != "deleting" {
				return fail(name, "deleted rule %q should be marked deleting, got status %v", ruleName, rMap["status"])
			}
			return pass(name)
		}
	}

	return pass(name)
}

// ---------- OCI registry ----------

// ScenarioOCIRegistryV2 checks the OCI registry /v2/ and /v2/_catalog
// endpoints are reachable and return expected responses.
func ScenarioOCIRegistryV2(c *Client) ScenarioResult {
	name := "oci/registry-v2"

	tlsClient, err := c.OCITLSClient()
	if err != nil {
		return fail(name, "OCI TLS client: %v", err)
	}
	ociURL := c.OCIURL()

	// GET /v2/
	req, err := http.NewRequest(http.MethodGet, ociURL+"/v2/", nil)
	if err != nil {
		return fail(name, "build /v2/ request: %v", err)
	}
	resp, err := tlsClient.Do(req)
	if err != nil {
		return fail(name, "OCI /v2/: %v", err)
	}
	_, _ = ReadAllBytes(resp)
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusUnauthorized {
		return fail(name, "OCI /v2/: expected 200 or 401, got %d", resp.StatusCode)
	}

	// GET /v2/_catalog
	req2, err := http.NewRequest(http.MethodGet, ociURL+"/v2/_catalog", nil)
	if err != nil {
		return fail(name, "build /v2/_catalog request: %v", err)
	}
	resp2, err := tlsClient.Do(req2)
	if err != nil {
		return fail(name, "OCI /v2/_catalog: %v", err)
	}
	if resp2.StatusCode == http.StatusOK {
		body2, err := ReadJSON(resp2)
		if err != nil {
			return fail(name, "decode catalog: %v", err)
		}
		if _, ok := body2["repositories"]; !ok {
			return fail(name, "catalog response missing 'repositories' key")
		}
	} else if resp2.StatusCode != http.StatusUnauthorized {
		_, _ = ReadAllBytes(resp2)
		return fail(name, "OCI /v2/_catalog: expected 200 or 401, got %d", resp2.StatusCode)
	} else {
		_, _ = ReadAllBytes(resp2)
	}

	return pass(name)
}

// ---------- Scenario registry ----------

// AllScenarios returns the full set of functional scenarios in recommended
// execution order. Persistence/restart scenarios are deliberately excluded
// here — they are orchestrator-specific and belong in the charm or snap
// suite that controls service lifecycle.
func AllScenarios() []func(*Client) ScenarioResult {
	return []func(*Client) ScenarioResult{
		ScenarioHealthReadiness,
		ScenarioRootDocument,
		ScenarioOpenAPIServed,
		ScenarioDocsPage,
		ScenarioDevAuthWhoami,
		ScenarioTokenIssueExchangeRevoke,
		ScenarioPackageRegister,
		ScenarioRevisionUploadPush,
		ScenarioReleaseChannel,
		ScenarioV2Info,
		ScenarioResourceList,
		ScenarioResourceRevisionLifecycle,
		ScenarioResourceDownload,
		ScenarioSyncListRules,
		ScenarioSyncAddDeleteRule,
		ScenarioOCIRegistryV2,
	}
}

// runAll executes every scenario and returns the results.
func runAll(c *Client) []ScenarioResult {
	results := make([]ScenarioResult, 0, len(AllScenarios()))
	for _, fn := range AllScenarios() {
		results = append(results, fn(c))
	}
	return results
}

// ---------- helpers ----------

func pass(name string) ScenarioResult {
	return ScenarioResult{Name: name, Passed: true, Message: "ok"}
}

func fail(name, format string, args ...any) ScenarioResult {
	return ScenarioResult{Name: name, Passed: false, Message: fmt.Sprintf(format, args...)}
}
