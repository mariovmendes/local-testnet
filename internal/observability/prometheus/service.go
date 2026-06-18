package prometheus

import (
	"bufio"
	"context"
	"errors"
	"log/slog"
	"path/filepath"

	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/image"
	"github.com/docker/docker/api/types/mount"
	"github.com/docker/docker/api/types/network"
	"github.com/docker/docker/api/types/strslice"
	"github.com/docker/docker/client"
	"github.com/docker/go-connections/nat"
	"github.com/ethera-labs/local-testnet/internal/observability/shared"
)

const dockerImage = "prom/prometheus:v3.4.1"

func Start(ctx context.Context, client *client.Client) error {
	logger := slog.With("observability_service_name", "prometheus")

	reader, err := client.ImagePull(ctx, dockerImage, image.PullOptions{})
	if err != nil {
		return errors.Join(err, errors.New("failed to pull prometheus image"))
	}
	defer reader.Close()

	scanner := bufio.NewScanner(reader)
	for scanner.Scan() {
		logger.Debug(scanner.Text())
	}

	logger.Info("image was pulled. Creating container")

	configAbsPath, err := filepath.Abs("configs/prometheus")
	if err != nil {
		return errors.Join(err, errors.New("failed build absolute path for Prometheus configuration file"))
	}

	resp, err := client.ContainerCreate(ctx, &container.Config{
		Image: dockerImage,
		ExposedPorts: nat.PortSet{
			"9090/tcp": struct{}{},
		},
		Labels: shared.Labels,
		Cmd:    strslice.StrSlice{"--config.file=/etc/prometheus/config.yaml", "--web.enable-remote-write-receiver"},
	},
		&container.HostConfig{
			NetworkMode: shared.NetworkMode,
			ExtraHosts: []string{
        			"host.docker.internal:host-gateway",
 			},
			PortBindings: nat.PortMap{
				"9090/tcp": []nat.PortBinding{{HostIP: "0.0.0.0", HostPort: "1337"}},
			},
			Mounts: []mount.Mount{
				{
					Type:   mount.TypeBind,
					Source: configAbsPath,
					Target: "/etc/prometheus",
				},
			},
		},
		&network.NetworkingConfig{},
		nil,
		"prometheus")
	if err != nil {
		return errors.Join(err, errors.New("failed to create container"))
	}

	logger.With("ID", resp.ID).Info("container created. Starting...")

	if err := client.ContainerStart(ctx, resp.ID, container.StartOptions{}); err != nil {
		return errors.Join(err, errors.New("failed to start container"))
	}

	logger.Info("container started")

	if err := shared.AttachToLocalnetNetworks(ctx, client, resp.ID); err != nil {
		return errors.Join(err, errors.New("failed to attach container to localnet networks"))
	}

	logger.Info("container attached to localnet networks")

	return nil
}
