package docker

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"

	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/pkg/stdcopy"
)

type RunOptions struct {
	Image      string
	Entrypoint []string
	Cmd        []string
	Env        []string
	Volumes    map[string]string // host:container
	WorkDir    string
	User       string
	// NetworkMode joins the container to an existing Docker network by name
	// (e.g. "kt-localnet" to reach Kurtosis services from within the container).
	// Leave empty to use the default bridge network.
	NetworkMode string
	AutoRemove  bool
	StreamLogs  bool
	CaptureOut  bool
	// CaptureErr appends stderr to the returned string. Combine with CaptureOut
	// to capture output regardless of which stream the process used.
	CaptureErr bool
}

// Run runs a Docker container and waits for it to complete. When capturing
// output, logs are read after the container exits to avoid racing AutoRemove
// against a live attach stream.
func (c *Client) Run(ctx context.Context, opts RunOptions) (string, error) {
	capturing := opts.CaptureOut || opts.CaptureErr || opts.StreamLogs

	if err := c.ensureImage(ctx, opts.Image); err != nil {
		return "", err
	}

	hostConfig := &container.HostConfig{
		// Suppress AutoRemove while capturing so ContainerLogs can drain first.
		AutoRemove:  opts.AutoRemove && !capturing,
		NetworkMode: container.NetworkMode(opts.NetworkMode),
	}

	if len(opts.Volumes) > 0 {
		binds := make([]string, 0, len(opts.Volumes))
		for host, containerPath := range opts.Volumes {
			binds = append(binds, fmt.Sprintf("%s:%s", host, containerPath))
		}
		hostConfig.Binds = binds
	}

	resp, err := c.cli.ContainerCreate(ctx, &container.Config{
		Image:      opts.Image,
		Entrypoint: opts.Entrypoint,
		Cmd:        opts.Cmd,
		Env:        opts.Env,
		WorkingDir: opts.WorkDir,
		User:       opts.User,
	}, hostConfig, nil, nil, "")
	if err != nil {
		return "", fmt.Errorf("failed to create container: %w", err)
	}
	containerID := resp.ID

	// Background ctx so cleanup still runs if the caller's ctx was cancelled.
	if !hostConfig.AutoRemove {
		defer func() {
			_ = c.cli.ContainerRemove(context.Background(), containerID, container.RemoveOptions{Force: true})
		}()
	}

	if err := c.cli.ContainerStart(ctx, containerID, container.StartOptions{}); err != nil {
		return "", fmt.Errorf("failed to start container: %w", err)
	}

	statusCh, errCh := c.cli.ContainerWait(ctx, containerID, container.WaitConditionNotRunning)
	var statusCode int64
	select {
	case err := <-errCh:
		if err != nil {
			return "", fmt.Errorf("error waiting for container: %w", err)
		}
	case status := <-statusCh:
		statusCode = status.StatusCode
	}

	var stdout, stderr bytes.Buffer
	if capturing {
		logs, err := c.cli.ContainerLogs(ctx, containerID, container.LogsOptions{
			ShowStdout: true,
			ShowStderr: true,
		})
		if err != nil {
			return "", fmt.Errorf("failed to read container logs: %w", err)
		}
		defer logs.Close()

		var outW, errW io.Writer = &stdout, &stderr
		if opts.StreamLogs {
			outW = io.MultiWriter(&stdout, os.Stdout)
			errW = io.MultiWriter(&stderr, os.Stderr)
		}
		if _, err := stdcopy.StdCopy(outW, errW, logs); err != nil {
			return "", fmt.Errorf("failed to copy container logs: %w", err)
		}
	}

	if statusCode != 0 {
		errorOutput := stdout.String() + stderr.String()
		if errorOutput != "" {
			return "", fmt.Errorf("container exited with code %d: %s", statusCode, errorOutput)
		}
		return "", fmt.Errorf("container exited with code %d", statusCode)
	}

	switch {
	case opts.CaptureOut && opts.CaptureErr:
		return stdout.String() + stderr.String(), nil
	case opts.CaptureOut:
		return stdout.String(), nil
	case opts.CaptureErr:
		return stderr.String(), nil
	}

	return "", nil
}

// ensureImage pulls the image on demand. ContainerCreate does not auto-pull
// (unlike `docker run`), so missing images would otherwise fail at create.
func (c *Client) ensureImage(ctx context.Context, image string) error {
	if image == "" {
		return fmt.Errorf("image is required")
	}
	exists, err := c.ImageExists(ctx, image)
	if err != nil {
		return fmt.Errorf("failed to check image %q: %w", image, err)
	}
	if exists {
		return nil
	}
	if err := c.PullImage(ctx, image); err != nil {
		return fmt.Errorf("failed to pull image %q: %w", image, err)
	}
	return nil
}
