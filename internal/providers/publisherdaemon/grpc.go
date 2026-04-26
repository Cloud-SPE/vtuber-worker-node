package publisherdaemon

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"os"
	"path/filepath"
	"strings"
	"time"

	registryv1 "github.com/Cloud-SPE/livepeer-modules-project/service-registry-daemon/proto/gen/go/livepeer/registry/v1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

// Client is the worker's gRPC client for the publisher daemon. Held
// open for the worker's lifetime so the BuildSignWrite startup flow
// and any future hot-reload path share the same connection.
type Client struct {
	conn   *grpc.ClientConn
	pub    registryv1.PublisherClient
	logger *slog.Logger
}

// Dial connects to a unix-socket gRPC endpoint. Pass either
// "unix:///run/livepeer/registry/publisher.sock" or
// "/run/livepeer/registry/publisher.sock" — the function normalizes.
func Dial(socket string, logger *slog.Logger) (*Client, error) {
	if logger == nil {
		logger = slog.Default()
	}
	target := normalizeUnixTarget(socket)
	conn, err := grpc.NewClient(target,
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithContextDialer(func(ctx context.Context, addr string) (net.Conn, error) {
			path := strings.TrimPrefix(addr, "unix:")
			path = strings.TrimPrefix(path, "//")
			d := net.Dialer{}
			return d.DialContext(ctx, "unix", path)
		}),
	)
	if err != nil {
		return nil, fmt.Errorf("publisherdaemon: dial: %w", err)
	}
	return &Client{
		conn:   conn,
		pub:    registryv1.NewPublisherClient(conn),
		logger: logger,
	}, nil
}

// Close releases the gRPC connection. Safe to call multiple times.
func (c *Client) Close() error {
	if c == nil || c.conn == nil {
		return nil
	}
	return c.conn.Close()
}

// NodeIdentity bundles the operator-provided fields the publisher
// needs to assemble the manifest header.
type NodeIdentity struct {
	OperatorEthAddress string
	NodeID             string
	NodeURL            string
}

// CapabilityInput is the worker-side projection of one capability +
// model list to publish. Mirrors the registry proto shape but with
// vtuber-worker-node's own types so the caller can build the slice
// directly from worker.yaml without touching proto types.
type CapabilityInput struct {
	Name     string
	WorkUnit string
	Models   []ModelInput
}

// ModelInput is one model under a capability.
type ModelInput struct {
	ID                  string
	PricePerWorkUnitWei string
	Warm                bool
	ConstraintsJSON     []byte // nil for the common no-constraints case
}

// BuildSignWrite is the M6 startup-flow entry point: build a manifest
// from the worker's identity + capabilities, sign it, and atomically
// write the signed JSON to manifestOutPath. If allowOnChainWrites is
// true AND serviceURI is non-empty, also call WriteServiceURI so the
// resolver can find the operator's eth-address → URL pointer on chain.
//
// Idempotent on disk (atomic tmp + rename), idempotent on chain (the
// daemon's WriteServiceURI is a no-op when the chain already holds
// the desired URI).
//
// Returns the on-chain tx hash if a write actually occurred; empty
// string when allowOnChainWrites was false or the chain already
// matched.
func (c *Client) BuildSignWrite(
	ctx context.Context,
	id NodeIdentity,
	caps []CapabilityInput,
	manifestOutPath string,
	allowOnChainWrites bool,
	serviceURI string,
) (txHash string, err error) {
	if id.OperatorEthAddress == "" {
		return "", errors.New("publisherdaemon: empty OperatorEthAddress")
	}
	if id.NodeID == "" {
		return "", errors.New("publisherdaemon: empty NodeID")
	}
	if id.NodeURL == "" {
		return "", errors.New("publisherdaemon: empty NodeURL")
	}
	if manifestOutPath == "" {
		return "", errors.New("publisherdaemon: empty ManifestOutPath")
	}
	if len(caps) == 0 {
		return "", errors.New("publisherdaemon: no capabilities to publish")
	}

	// 1. Build.
	buildCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	pbCaps := make([]*registryv1.Capability, 0, len(caps))
	for _, capIn := range caps {
		pbModels := make([]*registryv1.Model, 0, len(capIn.Models))
		for _, m := range capIn.Models {
			pbModels = append(pbModels, &registryv1.Model{
				Id:                  m.ID,
				PricePerWorkUnitWei: m.PricePerWorkUnitWei,
				Warm:                m.Warm,
				ConstraintsJson:     m.ConstraintsJSON,
			})
		}
		pbCaps = append(pbCaps, &registryv1.Capability{
			Name:     capIn.Name,
			WorkUnit: capIn.WorkUnit,
			Models:   pbModels,
		})
	}
	node := &registryv1.Node{
		Id:              id.NodeID,
		Url:             id.NodeURL,
		Capabilities:    pbCaps,
		OperatorAddress: id.OperatorEthAddress,
		Enabled:         true,
	}
	buildResp, err := c.pub.BuildManifest(buildCtx, &registryv1.BuildManifestRequest{
		Nodes: []*registryv1.Node{node},
	})
	if err != nil {
		return "", fmt.Errorf("publisherdaemon: BuildManifest: %w", err)
	}

	// 2. Sign.
	signCtx, cancelSign := context.WithTimeout(ctx, 30*time.Second)
	defer cancelSign()
	signed, err := c.pub.SignManifest(signCtx, &registryv1.SignManifestRequest{
		ManifestJson: buildResp.GetManifestJson(),
	})
	if err != nil {
		return "", fmt.Errorf("publisherdaemon: SignManifest: %w", err)
	}

	// 3. Atomic write to manifestOutPath. Tmp file + rename so the
	// operator's HTTPS server never serves a partial file.
	if err := atomicWrite(manifestOutPath, signed.GetManifestJson(), 0o644); err != nil {
		return "", fmt.Errorf("publisherdaemon: write manifest: %w", err)
	}
	c.logger.Info("publisherdaemon: manifest signed and written",
		"path", manifestOutPath,
		"capabilities", len(caps))

	// 4. Optional on-chain write.
	if allowOnChainWrites && serviceURI != "" {
		writeCtx, cancelWrite := context.WithTimeout(ctx, 60*time.Second)
		defer cancelWrite()
		txResp, err := c.pub.WriteServiceURI(writeCtx, &registryv1.WriteServiceURIRequest{
			Uri: serviceURI,
		})
		if err != nil {
			return "", fmt.Errorf("publisherdaemon: WriteServiceURI: %w", err)
		}
		txHash = txResp.GetTxHash()
		if txHash == "" {
			c.logger.Info("publisherdaemon: WriteServiceURI returned empty tx hash; chain already matches",
				"service_uri", serviceURI)
		} else {
			c.logger.Info("publisherdaemon: setServiceURI on-chain write submitted",
				"tx_hash", txHash, "service_uri", serviceURI)
		}
	} else if allowOnChainWrites {
		c.logger.Warn("publisherdaemon: allow_on_chain_writes set but service_uri empty — skipping on-chain write")
	}

	return txHash, nil
}

// atomicWrite is the small "write tmpfile + rename" helper. Avoids
// the operator's HTTPS server serving a half-written file mid-deploy.
func atomicWrite(path string, data []byte, mode os.FileMode) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("mkdir %s: %w", dir, err)
	}
	tmp, err := os.CreateTemp(dir, ".manifest-*.tmp")
	if err != nil {
		return fmt.Errorf("create tmp: %w", err)
	}
	tmpPath := tmp.Name()
	cleanup := func() { _ = os.Remove(tmpPath) }
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		cleanup()
		return fmt.Errorf("write tmp: %w", err)
	}
	if err := tmp.Chmod(mode); err != nil {
		_ = tmp.Close()
		cleanup()
		return fmt.Errorf("chmod tmp: %w", err)
	}
	if err := tmp.Close(); err != nil {
		cleanup()
		return fmt.Errorf("close tmp: %w", err)
	}
	if err := os.Rename(tmpPath, path); err != nil {
		cleanup()
		return fmt.Errorf("rename %s → %s: %w", tmpPath, path, err)
	}
	return nil
}

func normalizeUnixTarget(s string) string {
	if strings.HasPrefix(s, "unix:") {
		return s
	}
	return "unix://" + s
}
