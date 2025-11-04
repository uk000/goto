# usage: make VERSION=1.0.0 GOOS=darwin|linux|window release

# always run these targets
.PHONY: all clean

# variables
OUT := goto
GO_FILES := $(shell find . -name '*.go' | grep -v /vendor/)

COMMIT := $(shell git log -1 --pretty=tformat:%h)
VERSION := 0.0.0

IMAGE := uk0000/goto

all: build

gen:
	protoc --proto_path=pkg/rpc/grpc/protos pkg/rpc/grpc/protos/goto.proto --go-grpc_out=pkg/rpc/grpc/pb

clean:
	rm -rf pkg/server/grpc/pb/*.go

build: $(GO_FILES)
	GOOS=$(GOOS) GOARCH=$(GOARCH) go build -mod=mod -o $(OUT) -ldflags="-extldflags \"-static\" -w -s -X goto/global.Version=$(VERSION) -X goto/global.Commit=$(COMMIT)" .
	@chmod +x $(OUT)

run: build
	./$(OUT) --port 8080

docker-build: Dockerfile $(GO_FILES)
	docker build --build-arg GOOS="linux" --build-arg GOARCH="amd64" --build-arg utils=1 --build-arg COMMIT=$(COMMIT) --build-arg VERSION=$(VERSION) -t $(IMAGE):$(VERSION) --platform linux/amd64 .
	docker build --build-arg GOOS="linux" --build-arg GOARCH="arm64" --build-arg utils=1 --build-arg COMMIT=$(COMMIT) --build-arg VERSION=$(VERSION) -t $(IMAGE):$(VERSION)-arm64 --platform linux/arm64 .

docker-build-utils: Dockerfile $(GO_FILES)
	docker build --build-arg GOOS="linux" --build-arg GOARCH="amd64" --build-arg utils=1 --build-arg COMMIT=$(COMMIT) --build-arg VERSION=$(VERSION) -t $(IMAGE):$(VERSION)-utils --platform linux/amd64 .
	docker build --build-arg GOOS="linux" --build-arg GOARCH="arm64" --build-arg utils=1 --build-arg COMMIT=$(COMMIT) --build-arg VERSION=$(VERSION) -t $(IMAGE):$(VERSION)-utils-arm64 --platform linux/arm64 .

docker-build-net: Dockerfile $(GO_FILES)
	docker build --build-arg GOOS="linux" --build-arg GOARCH="amd64" --build-arg utils=1 --build-arg net=1 --build-arg COMMIT=$(COMMIT) --build-arg VERSION=$(VERSION) -t $(IMAGE):$(VERSION)-net --platform linux/amd64 .
	docker build --build-arg GOOS="linux" --build-arg GOARCH="arm64" --build-arg utils=1 --build-arg net=1 --build-arg COMMIT=$(COMMIT) --build-arg VERSION=$(VERSION) -t $(IMAGE):$(VERSION)-net-arm64 --platform linux/arm64 .

docker-build-kube: Dockerfile $(GO_FILES)
	docker build --build-arg GOOS="linux" --build-arg GOARCH="amd64" --build-arg utils=1 --build-arg net=1 --build-arg kube=1 --build-arg COMMIT=$(COMMIT) --build-arg VERSION=$(VERSION) -t $(IMAGE):$(VERSION)-kube --platform linux/amd64 .
	docker build --build-arg GOOS="linux" --build-arg GOARCH="arm64" --build-arg utils=1 --build-arg net=1 --build-arg kube=1 --build-arg COMMIT=$(COMMIT) --build-arg VERSION=$(VERSION) -t $(IMAGE):$(VERSION)-kube-arm64 --platform linux/arm64 .

docker-build-perf: Dockerfile $(GO_FILES)
	docker build --build-arg GOOS="linux" --build-arg GOARCH="amd64" --build-arg utils=1 --build-arg net=1 --build-arg perf=1 --build-arg COMMIT=$(COMMIT) --build-arg VERSION=$(VERSION) -t $(IMAGE):$(VERSION)-perf --platform linux/amd64 .
	docker build --build-arg GOOS="linux" --build-arg GOARCH="arm64" --build-arg utils=1 --build-arg net=1 --build-arg perf=1 --build-arg COMMIT=$(COMMIT) --build-arg VERSION=$(VERSION) -t $(IMAGE):$(VERSION)-perf-arm64 --platform linux/arm64 .

docker-build-grpc: Dockerfile $(GO_FILES)
	docker build --build-arg GOOS="linux" --build-arg GOARCH="amd64" --build-arg utils=1 --build-arg net=1 --build-arg grpc=1 --build-arg COMMIT=$(COMMIT) --build-arg VERSION=$(VERSION) -t $(IMAGE):$(VERSION)-grpc --platform linux/amd64 .
	docker build --build-arg GOOS="linux" --build-arg GOARCH="arm64" --build-arg utils=1 --build-arg net=1 --build-arg grpc=1 --build-arg COMMIT=$(COMMIT) --build-arg VERSION=$(VERSION) -t $(IMAGE):$(VERSION)-grpc-arm64 --platform linux/arm64 .

docker-run: docker-build
	docker run --platform linux/arm64 -d --rm --name goto -p8080:8080 -it $(IMAGE):$(VERSION) /app/goto --port 8080