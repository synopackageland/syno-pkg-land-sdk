module github.com/synopackageland/syno-pkg-land-sdk

go 1.25.0

toolchain go1.26.9

// Non-Go trees: ./... and all match only the Go packages.
ignore (
	./rust
	./spec
	./ts
)

require (
	go.yaml.in/yaml/v4 v4.0.0-rc.4
	golang.org/x/sys v0.44.0
)

require (
	github.com/compose-spec/compose-go/v2 v2.16.1
	github.com/distribution/reference v0.5.0
	github.com/docker/go-connections v0.4.0 // indirect
	github.com/docker/go-units v0.5.0 // indirect
	github.com/go-viper/mapstructure/v2 v2.4.0 // indirect
	github.com/mattn/go-shellwords v1.0.12 // indirect
	github.com/opencontainers/go-digest v1.0.0 // indirect
	github.com/santhosh-tekuri/jsonschema/v6 v6.0.1 // indirect
	github.com/sirupsen/logrus v1.10.1 // indirect
	github.com/xhit/go-str2duration/v2 v2.1.0 // indirect
	go.yaml.in/yaml/v3 v3.0.5 // indirect
	golang.org/x/sync v0.3.0 // indirect
	golang.org/x/text v0.14.0
)
