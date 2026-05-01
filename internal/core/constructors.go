package core

import (
	"fmt"
	"strings"
)

// NewPackage validates and returns a package value.
func NewPackage(pkg Package) (Package, error) {
	pkg.Name = strings.TrimSpace(pkg.Name)
	pkg.Type = FirstNonEmpty(strings.TrimSpace(pkg.Type), "charm")
	if pkg.ID == "" {
		return Package{}, fmt.Errorf("package id is required")
	}
	if pkg.Name == "" {
		return Package{}, fmt.Errorf("package name is required")
	}
	if pkg.Status == "" {
		return Package{}, fmt.Errorf("package status is required")
	}
	if pkg.OwnerAccountID == "" {
		return Package{}, fmt.Errorf("package owner account id is required")
	}
	return pkg, nil
}

// NewRevision validates and returns a revision value.
func NewRevision(revision Revision) (Revision, error) {
	if revision.ID == "" {
		return Revision{}, fmt.Errorf("revision id is required")
	}
	if revision.PackageID == "" {
		return Revision{}, fmt.Errorf("revision package id is required")
	}
	if revision.Revision <= 0 {
		return Revision{}, fmt.Errorf("revision number must be greater than zero")
	}
	if revision.Version == "" {
		return Revision{}, fmt.Errorf("revision version is required")
	}
	if revision.Status == "" {
		return Revision{}, fmt.Errorf("revision status is required")
	}
	if revision.CreatedBy == "" {
		return Revision{}, fmt.Errorf("revision creator is required")
	}
	if revision.ObjectKey == "" {
		return Revision{}, fmt.Errorf("revision object key is required")
	}
	if revision.CreatedAt.IsZero() {
		return Revision{}, fmt.Errorf("revision creation time is required")
	}
	return revision, nil
}

// NewRelease validates and returns a release value.
func NewRelease(release Release) (Release, error) {
	release.Channel = strings.TrimSpace(release.Channel)
	if release.ID == "" {
		return Release{}, fmt.Errorf("release id is required")
	}
	if release.Channel == "" {
		return Release{}, fmt.Errorf("release channel is required")
	}
	if release.Revision <= 0 {
		return Release{}, fmt.Errorf("release revision must be greater than zero")
	}
	if release.When.IsZero() {
		return Release{}, fmt.Errorf("release timestamp is required")
	}
	return release, nil
}

// NewStoreToken validates and returns a store token value.
func NewStoreToken(token StoreToken) (StoreToken, error) {
	if token.SessionID == "" {
		return StoreToken{}, fmt.Errorf("store token session id is required")
	}
	if token.TokenHash == "" {
		return StoreToken{}, fmt.Errorf("store token hash is required")
	}
	if token.AccountID == "" {
		return StoreToken{}, fmt.Errorf("store token account id is required")
	}
	if token.ValidSince.IsZero() {
		return StoreToken{}, fmt.Errorf("store token valid-since is required")
	}
	if token.ValidUntil.IsZero() {
		return StoreToken{}, fmt.Errorf("store token valid-until is required")
	}
	if !token.ValidUntil.After(token.ValidSince) {
		return StoreToken{}, fmt.Errorf("store token valid-until must be after valid-since")
	}
	return token, nil
}
