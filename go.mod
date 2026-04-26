module github.com/Cloud-SPE/vtuber-worker-node

go 1.25

// Local sibling checkout until the libraries tag releases. Workers
// deploying from tags drop the replaces and pin versions.
replace github.com/Cloud-SPE/livepeer-payment-library => ../livepeer-payment-library

replace github.com/Cloud-SPE/livepeer-service-registry => ../livepeer-service-registry

require (
	github.com/Cloud-SPE/livepeer-payment-library v0.0.0-00010101000000-000000000000
	github.com/Cloud-SPE/livepeer-service-registry v0.0.0-00010101000000-000000000000
	github.com/prometheus/client_golang v1.20.5
	google.golang.org/grpc v1.80.0
)

require (
	github.com/beorn7/perks v1.0.1 // indirect
	github.com/cespare/xxhash/v2 v2.3.0 // indirect
	github.com/klauspost/compress v1.17.9 // indirect
	github.com/munnerz/goautoneg v0.0.0-20191010083416-a7dc8b61c822 // indirect
	github.com/prometheus/client_model v0.6.1 // indirect
	github.com/prometheus/common v0.55.0 // indirect
	github.com/prometheus/procfs v0.15.1 // indirect
	golang.org/x/net v0.49.0 // indirect
	golang.org/x/sys v0.40.0 // indirect
	golang.org/x/text v0.33.0 // indirect
	google.golang.org/genproto/googleapis/rpc v0.0.0-20260120221211-b8f7ae30c516 // indirect
	google.golang.org/protobuf v1.36.11 // indirect
	gopkg.in/yaml.v3 v3.0.1 // indirect
)
