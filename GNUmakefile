PLUGIN_BINARY=plugin/firecracker
export GO111MODULE=on

default: build

.PHONY: clean run build
clean:
	-rm -rf ${PLUGIN_BINARY}

build: clean
	go build -o ${PLUGIN_BINARY} .

run:
	sudo nomad agent -dev -config=${PWD}/example/config.hcl -plugin-dir=${PWD}/plugin -data-dir=${PWD}/data -bind=0.0.0.0

reset:
	-rm -rf run/*
	-sudo ip link del firecracker
	-sudo rm /var/lib/cni/networks/firecracker/*