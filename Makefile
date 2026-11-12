# usage: make VERSION=1.0.0 GOOS=darwin|linux|window release

# always run these targets
.PHONY: all clean docker-build docker-build-utils docker-build-net docker-build-kube docker-build-perf docker-build-grpc docker-push docker-push-utils docker-push-net docker-push-kube docker-push-perf docker-push-grpc docker-run gen gen-extauthz test e2e-test connect-probe connect-probe-build connect-probe-build-all

# variables
OUT := goto
GO_FILES := $(shell find . -name '*.go' | grep -v /vendor/)

COMMIT := $(shell git log -1 --pretty=tformat:%h)

IMAGE := uk0000/goto

E2E_BIN := bin/goto
PYTHON ?= python3

# the connect probe (tools/connectprobe): the arguments that it is run with, and the OS and architecture that it is built for
PROBE_ARGS ?=
PROBE_OS   ?= linux
PROBE_ARCH ?= amd64

# Corporate proxy settings forwarded into docker builds (picked up from the environment, upper or lower case). The
# containers can't resolve external hosts via DNS, so apk/go/openssl must go through the proxy. Expands to nothing if
# no proxy is configured. These are predefined build args, so they don't need ARG in the Dockerfile and aren't kept
# in the final image's history.
# DOCKER_PROXY=false (also 0 or no) skips passing the proxy args even if the proxy env vars are set,
# e.g. make docker-build VERSION=1.0.0 DOCKER_PROXY=false
DOCKER_PROXY ?= true
HTTP_PROXY  ?= $(http_proxy)
HTTPS_PROXY ?= $(https_proxy)
NO_PROXY    ?= $(no_proxy)
PROXY_BUILD_ARGS := $(if $(HTTP_PROXY),--build-arg http_proxy='$(HTTP_PROXY)' --build-arg HTTP_PROXY='$(HTTP_PROXY)') \
	$(if $(HTTPS_PROXY),--build-arg https_proxy='$(HTTPS_PROXY)' --build-arg HTTPS_PROXY='$(HTTPS_PROXY)') \
	$(if $(NO_PROXY),--build-arg no_proxy='$(NO_PROXY)' --build-arg NO_PROXY='$(NO_PROXY)')
PROXY_ARGS := $(if $(filter-out false False FALSE 0 no No NO,$(DOCKER_PROXY)),$(PROXY_BUILD_ARGS))

# File with the Alpine package repos (one URL per line, like /etc/apk/repositories) to use for the docker builds instead of
# the public ones, e.g. a mirror. Passed as a secret (not a build arg), so that it is not in the image history or layers.
# make docker-build VERSION=1.0.0 APK_REPOSITORIES_FILE=/path/to/repositories
# When not set, the public repos in the Dockerfile are used.
APK_REPOSITORIES_FILE ?=
comma := ,
APK_ARGS := $(if $(APK_REPOSITORIES_FILE),--secret id=apk_repositories$(comma)src='$(APK_REPOSITORIES_FILE)')

# Go module proxy and checksum database used by the docker builds, e.g. a mirror when the public proxy is not reachable
# make docker-build VERSION=1.0.0 GOPROXY=https://goproxy.example.com GOSUMDB='sum.golang.org https://goproxy.example.com/sumdb/'
# When not set, go uses its defaults. These are picked up from the environment too.
GOPROXY ?=
GOSUMDB ?=
GONOSUMDB ?=
GO_ARGS := $(if $(GOPROXY),--build-arg GOPROXY='$(GOPROXY)') $(if $(GOSUMDB),--build-arg GOSUMDB='$(GOSUMDB)') $(if $(GONOSUMDB),--build-arg GONOSUMDB='$(GONOSUMDB)')

all: build

gen: gen-extauthz
	protoc --proto_path=pkg/rpc/grpc/protos pkg/rpc/grpc/protos/goto.proto --go-grpc_out=pkg/rpc/grpc/pb

gen-extauthz:
	protoc --proto_path=pkg/rpc/grpc/extauthz --go_out=pkg/rpc/grpc/extauthz --go_opt=paths=source_relative --go-grpc_out=pkg/rpc/grpc/extauthz --go-grpc_opt=paths=source_relative pkg/rpc/grpc/extauthz/extauthz.proto

clean:
	rm -rf pkg/server/grpc/pb/*.go

# static linking isn't supported by the macOS toolchain (no static crt0.o), so it's skipped when building for darwin
TARGET_OS := $(if $(GOOS),$(GOOS),$(shell go env GOOS))
STATIC_FLAGS := $(if $(filter darwin,$(TARGET_OS)),,-extldflags \"-static\")

build: $(GO_FILES)
	GOOS=$(GOOS) GOARCH=$(GOARCH) go build -mod=mod -o $(OUT) -ldflags="$(STATIC_FLAGS) -w -s -X goto/pkg/global.Version=$(VERSION) -X goto/pkg/global.Commit=$(COMMIT)" .
	@chmod +x $(OUT)

run: build
	./$(OUT) --port 8080

test:
	go test -v ./...

# builds the binary into bin/ and runs all the e2e tests (tests/*/*-e2e-test.py) against it, via the driver in
# tests/e2e. To run only some of them: make e2e-test E2E_ARGS="registry startup"
e2e-test:
	@mkdir -p $(dir $(E2E_BIN))
	go build -o $(E2E_BIN) .
	GOTO_BIN=$(E2E_BIN) $(PYTHON) tests/e2e/run-e2e-tests.py -v $(E2E_ARGS)

# times the phases (DNS, TCP, CONNECT, TLS, response) of calls that go through a CONNECT proxy, with the resolver and dialer
# of Go that the goto client uses. See tools/connectprobe/README.md. Without PROBE_ARGS it only tells what it needs:
#   make connect-probe PROBE_ARGS="-proxy goto-proxy:9000 -url http://server:8081/echo -n 5"
#   make connect-probe PROBE_ARGS="-h"
connect-probe:
	go run ./tools/connectprobe $(PROBE_ARGS)

# builds the probe, static, to copy to where the client runs (a pod, for one) into bin/connectprobe-<os>-<arch>:
#   make connect-probe-build PROBE_OS=linux PROBE_ARCH=arm64
connect-probe-build:
	@mkdir -p bin
	CGO_ENABLED=0 GOOS=$(PROBE_OS) GOARCH=$(PROBE_ARCH) go build -trimpath -ldflags="-w -s" -o bin/connectprobe-$(PROBE_OS)-$(PROBE_ARCH) ./tools/connectprobe

# builds it for linux on amd64 and arm64
connect-probe-build-all:
	$(MAKE) connect-probe-build PROBE_OS=linux PROBE_ARCH=amd64
	$(MAKE) connect-probe-build PROBE_OS=linux PROBE_ARCH=arm64

docker-build: Dockerfile $(GO_FILES)
	docker buildx build $(PROXY_ARGS) $(APK_ARGS) $(GO_ARGS) --load --build-arg utils=1 --build-arg COMMIT=$(COMMIT) --build-arg VERSION=$(VERSION) -t $(IMAGE):$(VERSION) .

docker-build-utils: Dockerfile $(GO_FILES)
	docker buildx build $(PROXY_ARGS) $(APK_ARGS) $(GO_ARGS) --load --build-arg utils=1 --build-arg COMMIT=$(COMMIT) --build-arg VERSION=$(VERSION) -t $(IMAGE):$(VERSION)-utils .

docker-build-net: Dockerfile $(GO_FILES)
	docker buildx build $(PROXY_ARGS) $(APK_ARGS) $(GO_ARGS) --load --build-arg utils=1 --build-arg net=1 --build-arg COMMIT=$(COMMIT) --build-arg VERSION=$(VERSION) -t $(IMAGE):$(VERSION)-net .

docker-build-kube: Dockerfile $(GO_FILES)
	docker buildx build $(PROXY_ARGS) $(APK_ARGS) $(GO_ARGS) --load --build-arg utils=1 --build-arg net=1 --build-arg kube=1 --build-arg COMMIT=$(COMMIT) --build-arg VERSION=$(VERSION) -t $(IMAGE):$(VERSION)-kube .

docker-build-perf: Dockerfile $(GO_FILES)
	docker buildx build $(PROXY_ARGS) $(APK_ARGS) $(GO_ARGS) --load --build-arg utils=1 --build-arg net=1 --build-arg perf=1 --build-arg COMMIT=$(COMMIT) --build-arg VERSION=$(VERSION) -t $(IMAGE):$(VERSION)-perf .

docker-build-grpc: Dockerfile $(GO_FILES)
	docker buildx build $(PROXY_ARGS) $(APK_ARGS) $(GO_ARGS) --load --build-arg utils=1 --build-arg net=1 --build-arg grpc=1 --build-arg COMMIT=$(COMMIT) --build-arg VERSION=$(VERSION) -t $(IMAGE):$(VERSION)-grpc .

docker-push: Dockerfile $(GO_FILES)
	docker buildx build $(PROXY_ARGS) $(APK_ARGS) $(GO_ARGS) --push --platform linux/amd64,linux/arm64 --build-arg utils=1 --build-arg COMMIT=$(COMMIT) --build-arg VERSION=$(VERSION) -t $(IMAGE):$(VERSION) .

docker-push-utils: Dockerfile $(GO_FILES)
	docker buildx build $(PROXY_ARGS) $(APK_ARGS) $(GO_ARGS) --push --platform linux/amd64,linux/arm64 --build-arg utils=1 --build-arg COMMIT=$(COMMIT) --build-arg VERSION=$(VERSION) -t $(IMAGE):$(VERSION)-utils .

docker-push-net: Dockerfile $(GO_FILES)
	docker buildx build $(PROXY_ARGS) $(APK_ARGS) $(GO_ARGS) --push --platform linux/amd64,linux/arm64 --build-arg utils=1 --build-arg net=1 --build-arg COMMIT=$(COMMIT) --build-arg VERSION=$(VERSION) -t $(IMAGE):$(VERSION)-net .

docker-push-kube: Dockerfile $(GO_FILES)
	docker buildx build $(PROXY_ARGS) $(APK_ARGS) $(GO_ARGS) --push --platform linux/amd64,linux/arm64 --build-arg utils=1 --build-arg net=1 --build-arg kube=1 --build-arg COMMIT=$(COMMIT) --build-arg VERSION=$(VERSION) -t $(IMAGE):$(VERSION)-kube .

docker-push-perf: Dockerfile $(GO_FILES)
	docker buildx build $(PROXY_ARGS) $(APK_ARGS) $(GO_ARGS) --push --platform linux/amd64,linux/arm64 --build-arg utils=1 --build-arg net=1 --build-arg perf=1 --build-arg COMMIT=$(COMMIT) --build-arg VERSION=$(VERSION) -t $(IMAGE):$(VERSION)-perf .

docker-push-grpc: Dockerfile $(GO_FILES)
	docker buildx build $(PROXY_ARGS) $(APK_ARGS) $(GO_ARGS) --push --platform linux/amd64,linux/arm64 --build-arg utils=1 --build-arg net=1 --build-arg grpc=1 --build-arg COMMIT=$(COMMIT) --build-arg VERSION=$(VERSION) -t $(IMAGE):$(VERSION)-grpc .

docker-run: docker-build
	docker run -d --rm --name goto -p8080:8080 -it $(IMAGE):$(VERSION) /app/goto --port 8080