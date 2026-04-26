// Package publisherdaemon is the worker's gRPC client for the
// co-located livepeer-service-registry publisher daemon.
//
// On worker startup, the runtime calls BuildSignWrite to:
//
//  1. Build a manifest from the worker's parsed worker.yaml
//     (capabilities + models) plus operator identity (eth address,
//     node id, node URL).
//  2. Sign the manifest via the publisher daemon (which holds the
//     keystore).
//  3. Atomically write the signed JSON to the operator-served path
//     (the operator's HTTPS server picks it up at the well-known
//     path: https://<service_uri>/.well-known/livepeer-registry.json).
//  4. Optionally write the on-chain `setServiceURI` pointer so the
//     resolver can find the manifest from the eth-address. Gated by
//     `allow_on_chain_writes` in worker.yaml; default off.
//
// This package is the integration glue. The publisher daemon
// implements the gRPC surface; this package adapts the
// vtuber-worker-node config + capability list into the proto types
// the daemon expects.
//
// Canonical references:
//
//	livepeer-service-registry/docs/design-docs/grpc-surface.md
//	livepeer-service-registry/docs/design-docs/manifest-schema.md
//	livepeer-vtuber-project/docs/design-docs/decisions/009-vtuber-leads-service-registry-adoption.md
//
// The full operator workflow (CI manifest validation, multi-capability
// publishing, on-chain refresh-on-config-change) lands in
// livepeer-vtuber-project/docs/exec-plans/active/service-registry-
// publisher-deployment.md.
package publisherdaemon
