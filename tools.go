//go:build tools

package tools

import (
	_ "github.com/golangci/golangci-lint/v2/cmd/golangci-lint"
	_ "github.com/securego/gosec/v2/cmd/gosec"
	_ "github.com/sqlc-dev/sqlc/cmd/sqlc"
	_ "golang.org/x/vuln/cmd/govulncheck"
)
