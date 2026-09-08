package docker

import (
	"embed"
	"fmt"
	"os"
	"path/filepath"
)

//go:embed docker-compose.yml docker-compose.flashblocks.yml docker-compose.sidecar.yml docker-compose.bundler.yml docker-compose.frontend.yml docker-compose.frontend.dev.yml docker-compose.altda.yml docker-compose.opsuccinct.yml docker-compose.opbesu.yml
var embeddedComposeFS embed.FS

const (
	dockerFileName             = "docker-compose.yml"
	composeFlashblocksFileName = "docker-compose.flashblocks.yml"
	composeSidecarFileName     = "docker-compose.sidecar.yml"
	composeBundlerFileName     = "docker-compose.bundler.yml"
	composeFrontendFileName    = "docker-compose.frontend.yml"
	composeFrontendDevFileName = "docker-compose.frontend.dev.yml"
	composeAltDAFileName       = "docker-compose.altda.yml"
	composeOPSuccinctFileName  = "docker-compose.opsuccinct.yml"
	composeOpBesuFileName      = "docker-compose.opbesu.yml"
)

// ensureEmbeddedFile writes the named embedded compose file to localnetDir and returns its path.
// The file is always overwritten to stay in sync with the embedded version.
func ensureEmbeddedFile(localnetDir, filename string) (string, error) {
	dest := filepath.Join(localnetDir, filename)

	content, err := embeddedComposeFS.ReadFile(filename)
	if err != nil {
		return "", fmt.Errorf("failed to read embedded %s: %w", filename, err)
	}
	if err := os.MkdirAll(filepath.Dir(dest), 0755); err != nil {
		return "", fmt.Errorf("failed to create directory %s: %w", localnetDir, err)
	}
	if err := os.WriteFile(dest, content, 0644); err != nil {
		return "", fmt.Errorf("failed to write %s: %w", filename, err)
	}

	return dest, nil
}

// EnsureComposeFile writes the embedded docker-compose.yml to localnetDir and returns its path.
func EnsureComposeFile(localnetDir string) (string, error) {
	return ensureEmbeddedFile(localnetDir, dockerFileName)
}

// EnsureFlashblocksComposeFile writes the embedded docker-compose.flashblocks.yml to localnetDir and returns its path.
func EnsureFlashblocksComposeFile(localnetDir string) (string, error) {
	return ensureEmbeddedFile(localnetDir, composeFlashblocksFileName)
}

// EnsureSidecarComposeFile writes the embedded docker-compose.sidecar.yml to localnetDir and returns its path.
func EnsureSidecarComposeFile(localnetDir string) (string, error) {
	return ensureEmbeddedFile(localnetDir, composeSidecarFileName)
}

// EnsureBundlerComposeFile writes the embedded docker-compose.bundler.yml to localnetDir and returns its path.
func EnsureBundlerComposeFile(localnetDir string) (string, error) {
	return ensureEmbeddedFile(localnetDir, composeBundlerFileName)
}

// EnsureFrontendComposeFile writes the embedded docker-compose.frontend.yml to localnetDir and returns its path.
func EnsureFrontendComposeFile(localnetDir string) (string, error) {
	return ensureEmbeddedFile(localnetDir, composeFrontendFileName)
}

// EnsureFrontendDevComposeFile writes the embedded docker-compose.frontend.dev.yml to localnetDir and returns its path.
func EnsureFrontendDevComposeFile(localnetDir string) (string, error) {
	return ensureEmbeddedFile(localnetDir, composeFrontendDevFileName)
}

// EnsureAltDAComposeFile writes the embedded docker-compose.altda.yml to localnetDir and returns its path.
func EnsureAltDAComposeFile(localnetDir string) (string, error) {
	return ensureEmbeddedFile(localnetDir, composeAltDAFileName)
}

// EnsureOPSuccinctComposeFile writes the embedded docker-compose.opsuccinct.yml to localnetDir and returns its path.
func EnsureOPSuccinctComposeFile(localnetDir string) (string, error) {
	return ensureEmbeddedFile(localnetDir, composeOPSuccinctFileName)
}

// EnsureOpBesuComposeFile writes the embedded docker-compose.opbesu.yml to localnetDir and returns its path.
func EnsureOpBesuComposeFile(localnetDir string) (string, error) {
	return ensureEmbeddedFile(localnetDir, composeOpBesuFileName)
}
