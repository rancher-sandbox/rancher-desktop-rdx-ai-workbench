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
	"runtime"
	"strconv"

	"golang.org/x/sys/unix"
)

func findExecutable(ctx context.Context, defaultOnly bool) string {
	var potentialLocations []string

	if installLocation, err := getDefaultInstallLocation(ctx); err == nil {
		executablePath := filepath.Join(installLocation, "bin", "ollama")
		potentialLocations = append(potentialLocations, executablePath)
	}

	if !defaultOnly {
		potentialLocations = append(potentialLocations, "/usr/local/bin/ollama")
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
	executablePath := filepath.Join(installPath, "bin", "ollama")

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

	filename := "ollama-linux-amd64.tgz"
	if runtime.GOARCH == "arm64" {
		filename = "ollama-linux-arm64.tgz"
	}
	assetURL, err := getReleaseAssetURL(ctx, release, filename)
	if err != nil {
		return "", err
	}

	log.Printf("Downloading ollama from %s...", assetURL)

	// For Linux, Ollama is an archive that we need to extract.
	//TODO: Support ROCm
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
	installDir, err := getDefaultInstallLocation(ctx)
	if err != nil {
		return fmt.Errorf("failed to find ollama install: %w", err)
	}

	executablePath := filepath.Join(installDir, "bin", "ollama")
	if err = terminateProcess(ctx, executablePath); err != nil {
		return fmt.Errorf("error terminating existing ollama process: %w", err)
	}

	err = os.RemoveAll(installDir)
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

	// Check /proc/<pid>/exe to see if they're the correct file.
	pidfds, err := os.ReadDir("/proc")
	if err != nil {
		return fmt.Errorf("error listing processes: %w", err)
	}
	for _, pidfd := range pidfds {
		if !pidfd.IsDir() {
			continue
		}
		pid, err := strconv.Atoi(pidfd.Name())
		if err != nil {
			continue
		}
		exeInfo, err := os.Stat(filepath.Join("/proc", pidfd.Name(), "exe"))
		if err != nil {
			if !errors.Is(err, os.ErrNotExist) && !errors.Is(err, os.ErrPermission) {
				log.Printf("Failed to get executable of process %s: %s", pidfd.Name(), err)
			}
			continue
		}
		if !os.SameFile(executableInfo, exeInfo) {
			continue
		}
		proc, err := os.FindProcess(pid)
		if err != nil {
			continue
		}
		err = proc.Signal(unix.SIGTERM)
		if err == nil {
			log.Printf("Terminated process %d", pid)
		} else if !errors.Is(err, unix.EINVAL) {
			log.Printf("Ignoring failure to terminate pid %d: %s", pid, err)
		}
	}

	return nil
}
