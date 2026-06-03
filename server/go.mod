module tempmon

go 1.22

// Direct dependencies. Run `go mod tidy` to fetch these and populate go.sum
// (this machine has no Go toolchain / network access, so go.sum is generated on
// the build host or in the Docker builder stage).
require (
	github.com/eclipse/paho.mqtt.golang v1.4.3
	modernc.org/sqlite v1.28.0
)
