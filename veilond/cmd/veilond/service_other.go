//go:build !windows

package main

// platformMain has no extra modes outside Windows (systemd / launchd run
// veilond as a plain foreground process).
func platformMain(string, []string) (bool, error) { return false, nil }
