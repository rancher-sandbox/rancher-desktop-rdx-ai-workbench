package main

import (
	"compress/gzip"
	"context"
	"errors"
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"slices"

	"golang.org/x/sys/unix"
)

const (
	CTL_KERN      = "kern"
	KERN_PROCARGS = 38
)

// Find an existing install of ollama; if defaultOnly is false, this may include
// externally installed copies of ollama.  If not found, returns empty string.
func findExecutable(ctx context.Context, defaultOnly bool) string {
	var potentialLocations []string

	if installLocation, err := getDefaultInstallLocation(ctx); err == nil {
		potentialLocations = append(potentialLocations, filepath.Join(installLocation, "ollama"))
	}

	if !defaultOnly {
		potentialLocations = append(potentialLocations,
			"/usr/local/bin/ollama",
			"/Applications/Ollama.app/Contents/Resources/ollama",
		)

		if homeDir, err := os.UserHomeDir(); err == nil {
			potentialLocations = append(potentialLocations,
				filepath.Join(homeDir, "Applications/Ollama.app/Contents/Resources/ollama"))
		}
	}

	for _, location := range potentialLocations {
		if _, err := os.Stat(location); err == nil {
			// Found an existing ollama
			return location
		}
	}
	return ""
}

func installOllama(ctx context.Context, release, installPath string) (string, error) {
	succeeded := false
	executablePath := filepath.Join(installPath, "ollama")

	if _, err := os.Stat(executablePath); err == nil {
		return executablePath, nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return "", fmt.Errorf("failed to check ollama executable: %w", err)
	}

	defer func() {
		if !succeeded {
			// On failure, remove partially extracted files.
			_ = os.RemoveAll(installPath)
		}
	}()

	if err := os.MkdirAll(installPath, 0o755); err != nil {
		return "", fmt.Errorf("failed to create ollama directory: %w", err)
	}

	assetURL, err := getReleaseAssetURL(ctx, release, "ollama-darwin.tgz")
	if err != nil {
		return "", err
	}

	log.Printf("Downloading ollama from %s...", assetURL)

	// For macOS, Ollama is an archive that we need to extract.
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, assetURL, nil)
	if err != nil {
		return "", fmt.Errorf("failed to create request: %w", err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("failed to download ollama: %w", err)
	}
	if resp.StatusCode >= 300 {
		return "", fmt.Errorf("error downloading ollama: status %s", resp.Status)
	}
	defer resp.Body.Close()

	gzipReader, err := gzip.NewReader(resp.Body)
	if err != nil {
		return "", fmt.Errorf("failed to read gzip archive: %w", err)
	}
	if err = extractTar(gzipReader, installPath); err != nil {
		return "", err
	}

	succeeded = true

	return executablePath, nil
}

func uninstallOllama(ctx context.Context) error {
	installPath, err := getDefaultInstallLocation(ctx)
	if err != nil {
		return fmt.Errorf("failed to find ollama install: %w", err)
	}
	if err = terminateProcess(ctx, installPath); err != nil {
		return fmt.Errorf("error terminating existing ollama process: %w", err)
	}
	err = os.RemoveAll(installPath)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}

	return nil
}

func terminateProcess(ctx context.Context, executablePath string) error {
	executableInfo, err := os.Stat(executablePath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return fmt.Errorf("failed to get executable info: %w", err)
	}

	procs, err := unix.SysctlKinfoProcSlice("kern.proc.all")
	if err != nil {
		return fmt.Errorf("failed to list processes: %w", err)
	}
	for _, proc := range procs {
		pid := int(proc.Proc.P_pid)
		buf, err := unix.SysctlRaw(CTL_KERN, KERN_PROCARGS, pid)
		if err != nil {
			if !errors.Is(err, unix.EINVAL) {
				log.Printf("Failed to get command line of pid %d: %s", pid, err)
			}
			continue
		}
		// The buffer starts with a null-terminated executable path, plus
		// command line arguments and things.
		index := slices.Index(buf, 0)
		if index < 0 {
			// If we have unexpected data, don't fall over.
			continue
		}
		procPath := string(buf[:index])
		procInfo, err := os.Stat(procPath)
		if err != nil {
			continue
		}
		if os.SameFile(executableInfo, procInfo) {
			process, err := os.FindProcess(pid)
			if err != nil {
				continue
			}
			err = process.Signal(unix.SIGTERM)
			if err == nil {
				log.Printf("Terminated process %d", pid)
			} else if !errors.Is(err, unix.EINVAL) {
				log.Printf("Ignoring failure to terminate pid %d: %s", pid, err)
			}
		}
	}
	return nil
}
