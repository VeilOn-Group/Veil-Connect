//go:build !devinsecure

package config

// DevBuild is false in release builds: pinning cannot be disabled by config.
const DevBuild = false
