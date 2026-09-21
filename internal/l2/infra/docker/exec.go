package docker

import (
	"archive/tar"
	"context"
	"fmt"
	"io"

	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/filters"
)

// FindContainer returns the ID of the first running container whose name
// contains nameSubstring (Kurtosis suffixes service names with a random
// hash, so exact matches aren't possible). Returns "" if none is running.
func (c *Client) FindContainer(ctx context.Context, nameSubstring string) (string, error) {
	f := filters.NewArgs()
	f.Add("name", nameSubstring)
	f.Add("status", "running")

	containers, err := c.cli.ContainerList(ctx, container.ListOptions{Filters: f})
	if err != nil {
		return "", fmt.Errorf("failed to list containers: %w", err)
	}
	if len(containers) == 0 {
		return "", nil
	}

	return containers[0].ID, nil
}

// ReadFile reads a single file's contents from inside a container's filesystem.
func (c *Client) ReadFile(ctx context.Context, containerID, path string) ([]byte, error) {
	reader, _, err := c.cli.CopyFromContainer(ctx, containerID, path)
	if err != nil {
		return nil, fmt.Errorf("failed to copy %q from container: %w", path, err)
	}
	defer reader.Close()

	tr := tar.NewReader(reader)
	if _, err := tr.Next(); err != nil {
		return nil, fmt.Errorf("failed to read tar header for %q: %w", path, err)
	}

	data, err := io.ReadAll(tr)
	if err != nil {
		return nil, fmt.Errorf("failed to read file contents for %q: %w", path, err)
	}

	return data, nil
}
