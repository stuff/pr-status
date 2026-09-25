package main

import (
	"io"
	"os"
	"os/exec"
	"strings"

	"github.com/cli/go-gh/v2/pkg/browser"
	"github.com/cli/go-gh/v2/pkg/config"
)

// openURL opens url in the user's browser.
//
// An explicitly configured browser always wins (GH_BROWSER, `gh config set
// browser`, then BROWSER), exactly like `gh --web`. Otherwise, under WSL the
// URL is handed to Windows so it opens in the Windows default browser instead
// of a Linux one running through WSLg.
func openURL(url string) error {
	if !browserConfigured() && isWSL() {
		if path, err := exec.LookPath("wslview"); err == nil {
			return exec.Command(path, url).Run()
		}
		if path, err := exec.LookPath("rundll32.exe"); err == nil {
			return exec.Command(path, "url.dll,FileProtocolHandler", url).Run()
		}
	}
	return browser.New("", io.Discard, io.Discard).Browse(url)
}

func browserConfigured() bool {
	if os.Getenv("GH_BROWSER") != "" || os.Getenv("BROWSER") != "" {
		return true
	}
	if cfg, err := config.Read(nil); err == nil {
		if b, _ := cfg.Get([]string{"browser"}); b != "" {
			return true
		}
	}
	return false
}

func isWSL() bool {
	if os.Getenv("WSL_DISTRO_NAME") != "" {
		return true
	}
	release, err := os.ReadFile("/proc/sys/kernel/osrelease")
	return err == nil && strings.Contains(strings.ToLower(string(release)), "microsoft")
}
