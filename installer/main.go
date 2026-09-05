// Package main downloads ollama and runs it.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"time"
)

const (
	checkURL = "http://localhost:11434/api/tags"
)

type Mode string

const (
	ModeInstall   Mode = "install"   // Install ollama to the default location.
	ModeUninstall Mode = "uninstall" // Uninstall ollama that we have installed.
	ModeCheck     Mode = "check"     // Check if Ollama is installed, printing "true" or "false".
	ModeStart     Mode = "start"     // Run ollama in a new process and return immediately.
	ModeShutdown  Mode = "shutdown"  // Terminate any running ollama instrances.
)

var (
	mode           = ModeInstall
	allModes       = []Mode{ModeInstall, ModeUninstall, ModeCheck, ModeStart, ModeShutdown}
	releaseVersion = flag.String("release", "latest", "release to download when installing")
	pullModel      = flag.String("model", "tinyllama", "model to pull on install; set to empty string to skip")
)

func main() {
	ctx := context.Background()
	log.SetFlags(log.LUTC | log.Ldate | log.Ltime)
	flag.Func("mode", fmt.Sprintf("operation mode; one of %+v (default %q)", allModes, mode), func(s string) error {
		if i := slices.Index(allModes, Mode(s)); i > -1 {
			mode = allModes[i]
		} else {
			return fmt.Errorf("unexpected mode %s: should be one of %+v", s, allModes)
		}
		return nil
	})
	flag.Parse()

	switch mode {
	case ModeInstall:
		log.Printf("Installing ollama...")
		if err := install(ctx); err != nil {
			log.Fatal(err)
		}
	case ModeUninstall:
		log.Printf("Uninstalling ollama...")
		if err := uninstallOllama(ctx); err != nil {
			log.Fatal(err)
		}
	case ModeCheck:
		if err := checkInstall(ctx); err != nil {
			log.Fatal(err)
		}
	case ModeStart:
		if err := startOllama(ctx); err != nil {
			log.Fatal(err)
		}
	case ModeShutdown:
		if err := shutdownOllama(ctx); err != nil {
			log.Fatal(err)
		}
	}
}

// Check if Ollama is already running.
func checkExistingInstance(ctx context.Context) (bool, error) {
	log.Printf("Checking if %s returns a valid response...", checkURL)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, checkURL, nil)
	if err != nil {
		return false, fmt.Errorf("failed to check Ollama: %v", err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err == nil && resp.StatusCode < 400 {
		defer resp.Body.Close()
		log.Printf("Ollama seems to be running correctly.")
		return true, nil
	}
	return false, nil
}

func install(ctx context.Context) error {
	isRunning, err := checkExistingInstance(ctx)
	if err != nil {
		return err
	}
	if isRunning {
		return nil
	}
	executablePath := findExecutable(ctx, false)
	if executablePath == "" {
		// If a previous executable is not found, install it to the default
		// location.
		installLocation, err := getDefaultInstallLocation(ctx)
		if err != nil {
			return fmt.Errorf("failed to get install location: %w", err)
		}
		executablePath, err = installOllama(ctx, *releaseVersion, installLocation)
		if err != nil {
			return fmt.Errorf("failed to install ollama: %w", err)
		}
	}

	// To ensure the file has been completely written (and virus scanners are done
	// scanning), try to run it a few times.
	for i := 0; i < 10; i++ {
		if err = exec.Command(executablePath, "--version").Run(); err == nil {
			break
		}
		time.Sleep(time.Second)
	}
	if err != nil {
		return fmt.Errorf("failed to run %s: %w", executablePath, err)
	}

	return nil
}

type releaseInfo struct {
	AssetsURL string `json:"assets_url"`
}

type assetInfo struct {
	Name string `json:"name"`
	URL  string `json:"browser_download_url"`
}

// getReleaseAssetURL returns the download URL for a specific asset in a release.
func getReleaseAssetURL(ctx context.Context, release, assetName string) (string, error) {
	releaseURL := fmt.Sprintf("https://api.github.com/repos/ollama/ollama/releases/tags/%s", release)
	if release == "latest" {
		releaseURL = "https://api.github.com/repos/ollama/ollama/releases/latest"
	}
	releaseReq, err := http.NewRequestWithContext(ctx, http.MethodGet, releaseURL, nil)
	if err != nil {
		return "", fmt.Errorf("failed to find release: %w", err)
	}
	releaseResp, err := http.DefaultClient.Do(releaseReq)
	if err != nil {
		return "", fmt.Errorf("failed to find release: %w", err)
	}
	defer releaseResp.Body.Close()
	if releaseResp.StatusCode >= 300 {
		return "", fmt.Errorf("failed to find release: unexpected status %s", releaseResp.Status)
	}
	releaseBody, err := io.ReadAll(releaseResp.Body)
	if err != nil {
		return "", fmt.Errorf("failed to find release: reading response: %w", err)
	}
	var releaseInfo releaseInfo
	if err = json.Unmarshal(releaseBody, &releaseInfo); err != nil {
		return "", fmt.Errorf("failed to find release: error unmarshaling response: %w", err)
	}

	assetsReq, err := http.NewRequestWithContext(ctx, http.MethodGet, releaseInfo.AssetsURL, nil)
	if err != nil {
		return "", fmt.Errorf("failed to find assets: %w", err)
	}
	assetsResp, err := http.DefaultClient.Do(assetsReq)
	if err != nil {
		return "", fmt.Errorf("failed to find assets: %w", err)
	}
	defer assetsResp.Body.Close()
	if assetsResp.StatusCode >= 300 {
		return "", fmt.Errorf("failed to find assets: unexpected status %s", assetsResp.Status)
	}
	assetsBody, err := io.ReadAll(assetsResp.Body)
	if err != nil {
		return "", fmt.Errorf("failed to find assets: reading response: %w", err)
	}
	var assets []assetInfo
	if err = json.Unmarshal(assetsBody, &assets); err != nil {
		return "", fmt.Errorf("failed to find assets: error unmarshaling response: %w", err)
	}

	for _, asset := range assets {
		if asset.Name == assetName {
			return asset.URL, nil
		}
	}

	return "", fmt.Errorf("failed to find asset %q in release %q", assetName, release)
}

// Get the default install location.  Note that this does not return the
// location of any externally installed copies of ollama.
func getDefaultInstallLocation(ctx context.Context) (string, error) {
	executable, err := os.Executable()
	if err != nil {
		return "", fmt.Errorf("failed to find executable path: %w", err)
	}
	extensionDir := filepath.Dir(filepath.Dir(executable))
	return filepath.Join(extensionDir, "ollama"), nil
}

// Print "true" if Ollama is installed, or "false" otherwise.
func checkInstall(ctx context.Context) error {
	isRunning, err := checkExistingInstance(ctx)
	if err == nil && isRunning {
		if _, err = fmt.Println("true"); err != nil {
			return fmt.Errorf("failed to output state")
		}
		return nil
	}
	location := findExecutable(ctx, false)
	if location != "" {
		if _, err := os.Stat(location); err == nil {
			if _, err = fmt.Println("true"); err != nil {
				return fmt.Errorf("failed to output location: %w", err)
			}
			return nil
		}
	}
	fmt.Println("false")
	return nil
}

func startOllama(ctx context.Context) error {
	isRunning, err := checkExistingInstance(ctx)
	if err != nil {
		return err
	}
	if isRunning {
		return nil
	}

	executablePath := findExecutable(ctx, false)
	if executablePath == "" {
		return fmt.Errorf("failed to find ollama executable; was it installed?")
	}

	// Do not wait for serveProc to complete.
	serveProc := exec.Command(executablePath, "serve")
	serveProc.Stdout = os.Stdout
	serveProc.Stderr = os.Stderr
	if err = serveProc.Start(); err != nil {
		return fmt.Errorf("failed to start ollama server: %v", err)
	}

	log.Printf("Waiting for %s to succeed...", checkURL)
	for {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, checkURL, nil)
		if err != nil {
			return fmt.Errorf("failed to check Ollama: %v", err)
		}
		resp, err := http.DefaultClient.Do(req)
		if err == nil && resp.StatusCode < 400 {
			resp.Body.Close()
			break
		}
		time.Sleep(time.Second)
	}

	if *pullModel != "" {
		pullProc := exec.CommandContext(ctx, executablePath, "pull", *pullModel)
		pullProc.Stdout = os.Stdout
		pullProc.Stderr = os.Stderr
		if err = pullProc.Run(); err != nil {
			return fmt.Errorf("failed to pull %s: %v", *pullModel, err)
		}
	}

	return nil
}

func shutdownOllama(ctx context.Context) error {
	executablePath := findExecutable(ctx, true)
	if executablePath == "" {
		// When shutting down, it is not an error if it was not found.
		return nil
	}
	err := terminateProcess(ctx, executablePath)
	if err != nil {
		return err
	}
	return nil
}
