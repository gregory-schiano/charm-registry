package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"text/tabwriter"
	"time"
)

type cliConfig struct {
	URL   string
	Token string
}

type charmhubSyncRule struct {
	Name               string     `json:"name"`
	Track              string     `json:"track"`
	Bases              []string   `json:"bases"`
	Architectures      []string   `json:"architectures"`
	Status             string     `json:"status"`
	LastSyncStartedAt  *time.Time `json:"last-sync-started-at"`
	LastSyncFinishedAt *time.Time `json:"last-sync-finished-at"`
	LastSyncError      *string    `json:"last-sync-error"`
	CreatedAt          time.Time  `json:"created-at"`
	UpdatedAt          time.Time  `json:"updated-at"`
}

type stringListFlag []string

func (f *stringListFlag) String() string {
	return strings.Join(*f, ",")
}

func (f *stringListFlag) Set(value string) error {
	*f = append(*f, value)
	return nil
}

type charmhubSyncRuleListResponse struct {
	Rules []charmhubSyncRule `json:"rules"`
}

type errorListResponse struct {
	ErrorList []struct {
		Code    string `json:"code"`
		Message string `json:"message"`
	} `json:"error-list"`
}

func main() {
	if err := run(context.Background(), os.Args[1:], os.Stdout, os.Stderr); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(ctx context.Context, args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("charm-registryctl", flag.ContinueOnError)
	fs.SetOutput(stderr)

	cfg := cliConfig{
		URL:   strings.TrimRight(os.Getenv("CHARM_REGISTRY_URL"), "/"),
		Token: os.Getenv("CHARM_REGISTRY_TOKEN"),
	}
	fs.StringVar(&cfg.URL, "url", cfg.URL, "Charm Registry base URL")
	fs.StringVar(&cfg.Token, "token", cfg.Token, "Bearer token")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if cfg.URL == "" {
		return errors.New("registry URL is required via --url or CHARM_REGISTRY_URL")
	}
	if cfg.Token == "" {
		return errors.New("registry token is required via --token or CHARM_REGISTRY_TOKEN")
	}

	remaining := fs.Args()
	if len(remaining) == 0 {
		return errors.New("usage: charm-registryctl [--url url] [--token token] <sync|unregister>")
	}
	switch remaining[0] {
	case "sync":
		return runSync(ctx, cfg, remaining[1:], stdout, stderr)
	case "unregister":
		return runUnregister(ctx, cfg, remaining[1:], stdout, stderr)
	default:
		return fmt.Errorf("unknown command %q", remaining[0])
	}
}

func runSync(ctx context.Context, cfg cliConfig, args []string, stdout, stderr io.Writer) error {
	if len(args) == 0 {
		return errors.New("usage: charm-registryctl sync <list|add|remove|run|wait>")
	}
	switch args[0] {
	case "list":
		return runSyncList(ctx, cfg, stdout)
	case "add":
		return runSyncAdd(ctx, cfg, args[1:], stdout, stderr)
	case "remove":
		return runSyncRemove(ctx, cfg, args[1:], stdout, stderr)
	case "run":
		return runSyncRun(ctx, cfg, args[1:], stdout, stderr)
	case "wait":
		return runSyncWait(ctx, cfg, args[1:], stdout, stderr)
	default:
		return fmt.Errorf("unknown sync subcommand %q", args[0])
	}
}

func runSyncList(ctx context.Context, cfg cliConfig, stdout io.Writer) error {
	var payload charmhubSyncRuleListResponse
	if err := doJSON(ctx, cfg, http.MethodGet, "/v1/admin/charmhub-sync", nil, &payload); err != nil {
		return err
	}
	writer := tabwriter.NewWriter(stdout, 0, 2, 2, ' ', 0)
	fmt.Fprintln(writer, "NAME\tTRACK\tBASES\tARCHES\tSTATUS\tLAST_ERROR")
	for _, rule := range payload.Rules {
		lastError := ""
		if rule.LastSyncError != nil {
			lastError = *rule.LastSyncError
		}
		fmt.Fprintf(
			writer,
			"%s\t%s\t%s\t%s\t%s\t%s\n",
			rule.Name,
			rule.Track,
			formatFilter(rule.Bases),
			formatFilter(rule.Architectures),
			rule.Status,
			lastError,
		)
	}
	return writer.Flush()
}

func runSyncAdd(ctx context.Context, cfg cliConfig, args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("sync add", flag.ContinueOnError)
	fs.SetOutput(stderr)
	track := "latest"
	var bases stringListFlag
	var architectures stringListFlag
	fs.StringVar(&track, "track", track, "Charmhub track to synchronize")
	fs.Var(&bases, "base", "Base to synchronize, for example ubuntu@22.04. May be repeated")
	fs.Var(&architectures, "arch", "Architecture to synchronize, for example amd64. May be repeated")
	name := ""
	if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		name = args[0]
		args = args[1:]
	}
	if err := fs.Parse(args); err != nil {
		return err
	}
	switch {
	case name == "" && fs.NArg() == 1:
		name = fs.Arg(0)
	case name != "" && fs.NArg() == 0:
	case name == "" && fs.NArg() == 0:
		return errors.New("usage: charm-registryctl sync add <name> --track <track>")
	default:
		return errors.New("usage: charm-registryctl sync add <name> --track <track>")
	}
	var payload charmhubSyncRule
	if err := doJSON(ctx, cfg, http.MethodPost, "/v1/admin/charmhub-sync", map[string]any{
		"name":          name,
		"track":         track,
		"bases":         []string(bases),
		"architectures": []string(architectures),
	}, &payload); err != nil {
		return err
	}
	fmt.Fprintf(stdout, "scheduled sync for %s track %s\n", payload.Name, payload.Track)
	return nil
}

func formatFilter(values []string) string {
	if len(values) == 0 {
		return "all"
	}
	return strings.Join(values, ",")
}

func runSyncRemove(ctx context.Context, cfg cliConfig, args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("sync remove", flag.ContinueOnError)
	fs.SetOutput(stderr)
	track := "latest"
	fs.StringVar(&track, "track", track, "Charmhub track to remove")
	name := ""
	if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		name = args[0]
		args = args[1:]
	}
	if err := fs.Parse(args); err != nil {
		return err
	}
	switch {
	case name == "" && fs.NArg() == 1:
		name = fs.Arg(0)
	case name != "" && fs.NArg() == 0:
	case name == "" && fs.NArg() == 0:
		return errors.New("usage: charm-registryctl sync remove <name> --track <track>")
	default:
		return errors.New("usage: charm-registryctl sync remove <name> --track <track>")
	}
	path := fmt.Sprintf("/v1/admin/charmhub-sync/%s/%s", url.PathEscape(name), url.PathEscape(track))
	if err := doJSON(ctx, cfg, http.MethodDelete, path, nil, nil); err != nil {
		return err
	}
	fmt.Fprintf(stdout, "scheduled removal for %s track %s\n", name, track)
	return nil
}

func runSyncRun(ctx context.Context, cfg cliConfig, args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("sync run", flag.ContinueOnError)
	fs.SetOutput(stderr)
	name := ""
	if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		name = args[0]
		args = args[1:]
	}
	if err := fs.Parse(args); err != nil {
		return err
	}
	switch {
	case name == "" && fs.NArg() == 1:
		name = fs.Arg(0)
	case name != "" && fs.NArg() == 0:
	case name == "" && fs.NArg() == 0:
		return errors.New("usage: charm-registryctl sync run <name>")
	default:
		return errors.New("usage: charm-registryctl sync run <name>")
	}
	path := fmt.Sprintf("/v1/admin/charmhub-sync/%s/run", url.PathEscape(name))
	if err := doJSON(ctx, cfg, http.MethodPost, path, nil, nil); err != nil {
		return err
	}
	fmt.Fprintf(stdout, "triggered sync for %s\n", name)
	return nil
}

// transientSyncError reports whether a sync rule failed for a reason that a
// re-triggered run is likely to resolve (network timeouts and the like).
func transientSyncError(message string) bool {
	lowered := strings.ToLower(message)
	for _, marker := range []string{
		"client.timeout",
		"context deadline exceeded",
		"timeout",
		"connection reset",
		"temporarily unavailable",
	} {
		if strings.Contains(lowered, marker) {
			return true
		}
	}
	return false
}

func runSyncWait(ctx context.Context, cfg cliConfig, args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("sync wait", flag.ContinueOnError)
	fs.SetOutput(stderr)
	timeout := 30 * time.Minute
	interval := 15 * time.Second
	fs.DurationVar(&timeout, "timeout", timeout, "Maximum time to wait for all sync rules to complete")
	fs.DurationVar(&interval, "interval", interval, "Polling interval")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 0 {
		return errors.New("usage: charm-registryctl sync wait [--timeout duration] [--interval duration]")
	}

	deadline := time.Now().Add(timeout)
	for {
		pending, err := pendingSyncRules(ctx, cfg)
		if err != nil {
			return err
		}
		if len(pending) == 0 {
			fmt.Fprintln(stdout, "all sync rules completed")
			return nil
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("timed out waiting for sync rules: %s", strings.Join(pending, ", "))
		}
		fmt.Fprintf(stdout, "waiting for sync rules: %s\n", strings.Join(pending, ", "))
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(interval):
		}
	}
}

// pendingSyncRules lists the sync rules that have not completed yet,
// re-triggering rules that failed with a transient error. Rules that failed
// permanently produce an error.
func pendingSyncRules(ctx context.Context, cfg cliConfig) ([]string, error) {
	var payload charmhubSyncRuleListResponse
	if err := doJSON(ctx, cfg, http.MethodGet, "/v1/admin/charmhub-sync", nil, &payload); err != nil {
		return nil, err
	}
	var pending []string
	for _, rule := range payload.Rules {
		switch rule.Status {
		case "ok":
			continue
		case "error", "delete-error":
			lastError := ""
			if rule.LastSyncError != nil {
				lastError = *rule.LastSyncError
			}
			if !transientSyncError(lastError) {
				return nil, fmt.Errorf("sync failed for %s track %s: %s", rule.Name, rule.Track, lastError)
			}
			path := fmt.Sprintf("/v1/admin/charmhub-sync/%s/run", url.PathEscape(rule.Name))
			if err := doJSON(ctx, cfg, http.MethodPost, path, nil, nil); err != nil {
				return nil, fmt.Errorf("retrying sync for %s: %w", rule.Name, err)
			}
			pending = append(pending, fmt.Sprintf("%s:%s:%s:retrying", rule.Name, rule.Track, rule.Status))
		default:
			pending = append(pending, fmt.Sprintf("%s:%s:%s", rule.Name, rule.Track, rule.Status))
		}
	}
	return pending, nil
}

func runUnregister(ctx context.Context, cfg cliConfig, args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("unregister", flag.ContinueOnError)
	fs.SetOutput(stderr)
	yes := false
	fs.BoolVar(&yes, "yes", false, "Confirm destructive charm removal")
	name := ""
	if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		name = args[0]
		args = args[1:]
	}
	if err := fs.Parse(args); err != nil {
		return err
	}
	switch {
	case name == "" && fs.NArg() == 1:
		name = fs.Arg(0)
	case name != "" && fs.NArg() == 0:
	case name == "" && fs.NArg() == 0:
		return errors.New("usage: charm-registryctl unregister <name> --yes")
	default:
		return errors.New("usage: charm-registryctl unregister <name> --yes")
	}
	if !yes {
		return errors.New("refusing to unregister without --yes")
	}
	path := fmt.Sprintf("/v1/charm/%s?force=true", url.PathEscape(name))
	if err := doJSON(ctx, cfg, http.MethodDelete, path, nil, nil); err != nil {
		return err
	}
	fmt.Fprintf(stdout, "unregistered %s and removed registry-managed artifacts\n", name)
	return nil
}

func doJSON(ctx context.Context, cfg cliConfig, method, path string, body any, out any) error {
	_, err := doJSONStatus(ctx, cfg, method, path, body, out)
	return err
}

// doJSONStatus behaves like doJSON but also returns the HTTP status code so
// callers can tolerate specific error statuses (for example 409 Conflict).
func doJSONStatus(ctx context.Context, cfg cliConfig, method, path string, body any, out any) (int, error) {
	var requestBody io.Reader
	if body != nil {
		payload, err := json.Marshal(body)
		if err != nil {
			return 0, err
		}
		requestBody = bytes.NewReader(payload)
	}

	req, err := http.NewRequestWithContext(ctx, method, cfg.URL+path, requestBody)
	if err != nil {
		return 0, err
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Authorization", "Bearer "+cfg.Token)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()

	responseBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return resp.StatusCode, err
	}
	if resp.StatusCode >= http.StatusBadRequest {
		var apiErr errorListResponse
		if json.Unmarshal(responseBody, &apiErr) == nil && len(apiErr.ErrorList) > 0 {
			return resp.StatusCode, fmt.Errorf("%s: %s", apiErr.ErrorList[0].Code, apiErr.ErrorList[0].Message)
		}
		return resp.StatusCode, fmt.Errorf("request failed with status %s", resp.Status)
	}
	if out == nil || len(responseBody) == 0 {
		return resp.StatusCode, nil
	}
	return resp.StatusCode, json.Unmarshal(responseBody, out)
}
