// Package auth authenticates requests and manages store-token exchange flows.
//
// Charmcraft macaroon wrappers are compatibility envelopes: the registry
// authenticates the opaque token carried in the macaroon identifier, not the
// macaroon signature.
package auth
