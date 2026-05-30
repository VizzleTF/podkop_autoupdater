package updater

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"strings"
)

const (
	// InstallScriptURL is the moving HEAD of podkop's default branch. Used
	// only as a fallback when a release tag has no install.sh at its path.
	InstallScriptURL = "https://raw.githubusercontent.com/itdoginfo/podkop/refs/heads/main/install.sh"
	installScriptTpl = "https://raw.githubusercontent.com/itdoginfo/podkop/refs/tags/%s/install.sh"
)

// installScriptURLForTag pins the install.sh download to a specific release
// tag instead of the moving default branch. Empty tag → branch HEAD.
func installScriptURLForTag(tag string) string {
	if tag == "" {
		return InstallScriptURL
	}
	return fmt.Sprintf(installScriptTpl, tag)
}

// RunInstallScript downloads the upstream podkop install.sh into a temp
// file and runs it via /bin/sh with "y\ny\n" piped to stdin (matching the
// bash behavior). Script stdout/stderr go to w (typically the daemon log).
//
// The script is fetched from the given release tag when non-empty (so a
// compromised or accidental commit to the default branch is not executed as
// root), falling back to the branch HEAD only if the tag has no install.sh.
func RunInstallScript(ctx context.Context, hc *http.Client, w io.Writer, tag string) error {
	if hc == nil {
		hc = http.DefaultClient
	}
	body, err := fetchInstallScript(ctx, hc, installScriptURLForTag(tag))
	if err != nil && tag != "" {
		// Tag path may not carry install.sh on older releases; fall back.
		body, err = fetchInstallScript(ctx, hc, InstallScriptURL)
	}
	if err != nil {
		return err
	}

	tmp, err := os.CreateTemp("", "podkop_install_*.sh")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	defer os.Remove(tmpPath)
	if _, err := tmp.Write(body); err != nil {
		tmp.Close()
		return err
	}
	tmp.Close()
	if err := os.Chmod(tmpPath, 0o700); err != nil {
		return err
	}

	cmd := exec.CommandContext(ctx, "/bin/sh", tmpPath)
	cmd.Stdin = strings.NewReader("y\ny\n")
	if w != nil {
		cmd.Stdout = w
		cmd.Stderr = w
	}
	return cmd.Run()
}

// fetchInstallScript downloads url and returns its body, erroring on non-200
// or empty responses.
func fetchInstallScript(ctx context.Context, hc *http.Client, url string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	resp, err := hc.Do(req)
	if err != nil {
		return nil, fmt.Errorf("fetch install.sh: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("fetch install.sh: status %d", resp.StatusCode)
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read install.sh: %w", err)
	}
	if len(body) == 0 {
		return nil, fmt.Errorf("install.sh: empty body")
	}
	return body, nil
}
