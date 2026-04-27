module github.com/Cloud-SPE/vtuber-worker-node

go 1.25.0

// The payment-daemon and service-registry-daemon modules now live in
// the consolidated livepeer-modules-project monorepo (its plans 0002
// + 0003). Both transitively require chain-commons, which also lives
// in that monorepo as a separate Go module — Go's `replace`
// directives don't propagate from libraries, so we replicate the
// chain-commons replace at our level.
//
// Workers deploying from tags drop all three replaces and pin
// versions once livepeer-modules-project's plan 0008 publishes
// tagged Go modules.
replace github.com/Cloud-SPE/livepeer-modules-project/payment-daemon => ../livepeer-modules-project/payment-daemon

replace github.com/Cloud-SPE/livepeer-modules-project/service-registry-daemon => ../livepeer-modules-project/service-registry-daemon

replace github.com/Cloud-SPE/livepeer-modules-project/chain-commons => ../livepeer-modules-project/chain-commons

require (
	github.com/Cloud-SPE/livepeer-modules-project/payment-daemon v0.0.0-00010101000000-000000000000
	github.com/Cloud-SPE/livepeer-modules-project/service-registry-daemon v0.0.0-00010101000000-000000000000
	github.com/prometheus/client_golang v1.23.2
	google.golang.org/grpc v1.80.0
)

require (
	github.com/ProjectZKM/Ziren/crates/go-runtime/zkvm_runtime v0.0.0-20251001021608-1fe7b43fc4d6 // indirect
	github.com/beorn7/perks v1.0.1 // indirect
	github.com/cespare/xxhash/v2 v2.3.0 // indirect
	github.com/decred/dcrd/dcrec/secp256k1/v4 v4.0.1 // indirect
	github.com/ethereum/go-ethereum v1.17.2 // indirect
	github.com/holiman/uint256 v1.3.2 // indirect
	github.com/munnerz/goautoneg v0.0.0-20191010083416-a7dc8b61c822 // indirect
	github.com/prometheus/client_model v0.6.2 // indirect
	github.com/prometheus/common v0.66.1 // indirect
	github.com/prometheus/procfs v0.16.1 // indirect
	go.yaml.in/yaml/v2 v2.4.2 // indirect
	golang.org/x/net v0.49.0 // indirect
	golang.org/x/sys v0.40.0 // indirect
	golang.org/x/text v0.33.0 // indirect
	google.golang.org/genproto/googleapis/rpc v0.0.0-20260120221211-b8f7ae30c516 // indirect
	google.golang.org/protobuf v1.36.11 // indirect
	gopkg.in/yaml.v3 v3.0.1 // indirect
)
