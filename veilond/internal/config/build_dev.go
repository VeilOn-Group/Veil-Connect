//go:build devinsecure

package config

// DevBuild is true only with `-tags devinsecure` (local development):
// allows an unpinned core, plain-HTTP API and Hysteria2 insecure TLS.
const DevBuild = true
