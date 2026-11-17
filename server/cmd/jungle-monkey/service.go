package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
)

const serviceLabel = "dev.junglemonkey.server"

// runService starts jungle-monkey at login: a launchd user agent on macOS, a
// systemd --user unit on Linux, a Task Scheduler logon task on Windows. The
// service runs `start --no-open`; `jungle-monkey open` opens the UI.
func runService(args []string) error {
	if len(args) == 0 || (args[0] != "install" && args[0] != "uninstall") {
		return fmt.Errorf("usage: jungle-monkey service install|uninstall")
	}
	f, _, err := parseFlags("service", args[1:])
	if err != nil {
		return err
	}
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	if exe, err = filepath.EvalSymlinks(exe); err != nil {
		return err
	}
	if strings.Contains(exe, string(filepath.Separator)+"_npx"+string(filepath.Separator)) {
		fmt.Fprintln(os.Stderr, "note: this binary lives in the npx cache, which npm may clean. For a stable service, run `npm i -g jungle-monkey` first.")
	}
	startArgs := []string{exe, "start", "--no-open"}
	if f.dataDir != "" {
		startArgs = append(startArgs, "--data-dir", f.dataDir)
	}
	home, _ := os.UserHomeDir()
	install := args[0] == "install"
	switch runtime.GOOS {
	case "darwin":
		plist := filepath.Join(home, "Library", "LaunchAgents", serviceLabel+".plist")
		if !install {
			_ = exec.Command("launchctl", "unload", plist).Run()
			return removeIfExists(plist)
		}
		var argXML strings.Builder
		for _, a := range startArgs {
			fmt.Fprintf(&argXML, "\n    <string>%s</string>", xmlEscape(a))
		}
		body := fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
  <key>Label</key><string>%s</string>
  <key>ProgramArguments</key>
  <array>%s
  </array>
  <key>RunAtLoad</key><true/>
  <key>KeepAlive</key><dict><key>SuccessfulExit</key><false/></dict>
</dict>
</plist>
`, serviceLabel, argXML.String())
		if err := writeFile(plist, body); err != nil {
			return err
		}
		_ = exec.Command("launchctl", "unload", plist).Run()
		return report(exec.Command("launchctl", "load", "-w", plist).Run(), plist)
	case "windows":
		if !install {
			return exec.Command("schtasks", "/Delete", "/F", "/TN", "JungleMonkey").Run()
		}
		tr := `"` + strings.Join(startArgs, `" "`) + `"`
		return report(exec.Command("schtasks", "/Create", "/F", "/SC", "ONLOGON", "/TN", "JungleMonkey", "/TR", tr).Run(), "Task Scheduler: JungleMonkey")
	default:
		unit := filepath.Join(home, ".config", "systemd", "user", "jungle-monkey.service")
		if !install {
			_ = exec.Command("systemctl", "--user", "disable", "--now", "jungle-monkey.service").Run()
			return removeIfExists(unit)
		}
		quoted := make([]string, len(startArgs))
		for i, a := range startArgs {
			quoted[i] = `"` + strings.ReplaceAll(a, `"`, `\"`) + `"`
		}
		body := fmt.Sprintf(`[Unit]
Description=Jungle Monkey (local mode)
After=network-online.target docker.service

[Service]
ExecStart=%s
Restart=on-failure
RestartSec=5

[Install]
WantedBy=default.target
`, strings.Join(quoted, " "))
		if err := writeFile(unit, body); err != nil {
			return err
		}
		_ = exec.Command("systemctl", "--user", "daemon-reload").Run()
		return report(exec.Command("systemctl", "--user", "enable", "--now", "jungle-monkey.service").Run(), unit)
	}
}

func writeFile(path, body string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, []byte(body), 0o644)
}

func removeIfExists(path string) error {
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return err
	}
	fmt.Println("Removed", path)
	return nil
}

func report(err error, where string) error {
	if err != nil {
		return fmt.Errorf("installed %s but could not start it: %w", where, err)
	}
	fmt.Printf("%s Jungle Monkey now starts at login (%s). Open it with `jungle-monkey open`.\n", green("✓"), where)
	return nil
}

func xmlEscape(s string) string {
	return strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;").Replace(s)
}
