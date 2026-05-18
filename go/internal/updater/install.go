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

const InstallScriptURL = "https://raw.githubusercontent.com/itdoginfo/podkop/refs/heads/main/install.sh"

// RunInstallScript downloads the upstream podkop install.sh into a temp
// file and runs it via /bin/sh with "y\ny\n" piped to stdin (matching the
// bash behavior). Script stdout/stderr go to w (typically the daemon log).
func RunInstallScript(ctx context.Context, hc *http.Client, w io.Writer) error {
	if hc == nil {
		hc = http.DefaultClient
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, InstallScriptURL, nil)
	if err != nil {
		return err
	}
	resp, err := hc.Do(req)
	if err != nil {
		return fmt.Errorf("fetch install.sh: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("fetch install.sh: status %d", resp.StatusCode)
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("read install.sh: %w", err)
	}
	if len(body) == 0 {
		return fmt.Errorf("install.sh: empty body")
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
