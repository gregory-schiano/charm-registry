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
	Status             string     `json:"status"`
	LastSyncStartedAt  *time.Time `json:"last-sync-started-at"`
	LastSyncFinishedAt *time.Time `json:"last-sync-finished-at"`
	LastSyncError      *string    `json:"last-sync-error"`
	CreatedAt          time.Time  `json:"created-at"`
	UpdatedAt          time.Time  `json:"updated-at"`
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
		return errors.New("usage: charm-registryctl [--url url] [--token token] sync <list|add|remove|run>")
	}
	if remaining[0] != "sync" {
		return fmt.Errorf("unknown command %q", remaining[0])
	}
	return runSync(ctx, cfg, remaining[1:], stdout, stderr)
}

func runSync(ctx context.Context, cfg cliConfig, args []string, stdout, stderr io.Writer) error {
	if len(args) == 0 {
		return errors.New("usage: charm-registryctl sync <list|add|remove|run>")
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
	fmt.Fprintln(writer, "NAME\tTRACK\tSTATUS\tLAST_ERROR")
	for _, rule := range payload.Rules {
		lastError := ""
		if rule.LastSyncError != nil {
			lastError = *rule.LastSyncError
		}
		fmt.Fprintf(writer, "%s\t%s\t%s\t%s\n", rule.Name, rule.Track, rule.Status, lastError)
	}
	return writer.Flush()
}

func runSyncAdd(ctx context.Context, cfg cliConfig, args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("sync add", flag.ContinueOnError)
	fs.SetOutput(stderr)
	var track string
	fs.StringVar(&track, "track", "", "Charmhub track to synchronize")
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
	if err := doJSON(ctx, cfg, http.MethodPost, "/v1/admin/charmhub-sync", map[string]string{
		"name":  name,
		"track": track,
	}, &payload); err != nil {
		return err
	}
	fmt.Fprintf(stdout, "scheduled sync for %s track %s\n", payload.Name, payload.Track)
	return nil
}

func runSyncRemove(ctx context.Context, cfg cliConfig, args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("sync remove", flag.ContinueOnError)
	fs.SetOutput(stderr)
	var track string
	fs.StringVar(&track, "track", "", "Charmhub track to remove")
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
	path := fmt.Sprintf("/v1/admin/charmhub-sync/%s/%s", name, track)
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
	path := fmt.Sprintf("/v1/admin/charmhub-sync/%s/run", name)
	if err := doJSON(ctx, cfg, http.MethodPost, path, nil, nil); err != nil {
		return err
	}
	fmt.Fprintf(stdout, "triggered sync for %s\n", name)
	return nil
}

func doJSON(ctx context.Context, cfg cliConfig, method, path string, body any, out any) error {
	var requestBody io.Reader
	if body != nil {
		payload, err := json.Marshal(body)
		if err != nil {
			return err
		}
		requestBody = bytes.NewReader(payload)
	}

	req, err := http.NewRequestWithContext(ctx, method, cfg.URL+path, requestBody)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Authorization", "Bearer "+cfg.Token)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	responseBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return err
	}
	if resp.StatusCode >= http.StatusBadRequest {
		var apiErr errorListResponse
		if json.Unmarshal(responseBody, &apiErr) == nil && len(apiErr.ErrorList) > 0 {
			return fmt.Errorf("%s: %s", apiErr.ErrorList[0].Code, apiErr.ErrorList[0].Message)
		}
		return fmt.Errorf("request failed with status %s", resp.Status)
	}
	if out == nil || len(responseBody) == 0 {
		return nil
	}
	return json.Unmarshal(responseBody, out)
}
