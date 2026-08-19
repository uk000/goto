# usage: make VERSION=1.0.0 GOOS=darwin|linux|window release

# always run these targets
.PHONY: all clean docker-build docker-build-utils docker-build-net docker-build-kube docker-build-perf docker-build-grpc docker-push docker-push-utils docker-push-net docker-push-kube docker-push-perf docker-push-grpc docker-run

# variables
OUT := goto
GO_FILES := $(shell find . -name '*.go' | grep -v /vendor/)

COMMIT := $(shell git log -1 --pretty=tformat:%h)

IMAGE := uk0000/goto

all: build

gen:
	protoc --proto_path=pkg/rpc/grpc/protos pkg/rpc/grpc/protos/goto.proto --go-grpc_out=pkg/rpc/grpc/pb

clean:
	rm -rf pkg/server/grpc/pb/*.go

build: $(GO_FILES)
	GOOS=$(GOOS) GOARCH=$(GOARCH) go build -mod=mod -o $(OUT) -ldflags="-extldflags \"-static\" -w -s -X goto/pkg/global.Version=$(VERSION) -X goto/pkg/global.Commit=$(COMMIT)" .
	@chmod +x $(OUT)

run: build
	./$(OUT) --port 8080

docker-build: Dockerfile $(GO_FILES)
	docker buildx build --load --build-arg utils=1 --build-arg COMMIT=$(COMMIT) --build-arg VERSION=$(VERSION) -t $(IMAGE):$(VERSION) .

docker-build-utils: Dockerfile $(GO_FILES)
	docker buildx build --load --build-arg utils=1 --build-arg COMMIT=$(COMMIT) --build-arg VERSION=$(VERSION) -t $(IMAGE):$(VERSION)-utils .

docker-build-net: Dockerfile $(GO_FILES)
	docker buildx build --load --build-arg utils=1 --build-arg net=1 --build-arg COMMIT=$(COMMIT) --build-arg VERSION=$(VERSION) -t $(IMAGE):$(VERSION)-net .

docker-build-kube: Dockerfile $(GO_FILES)
	docker buildx build --load --build-arg utils=1 --build-arg net=1 --build-arg kube=1 --build-arg COMMIT=$(COMMIT) --build-arg VERSION=$(VERSION) -t $(IMAGE):$(VERSION)-kube .

docker-build-perf: Dockerfile $(GO_FILES)
	docker buildx build --load --build-arg utils=1 --build-arg net=1 --build-arg perf=1 --build-arg COMMIT=$(COMMIT) --build-arg VERSION=$(VERSION) -t $(IMAGE):$(VERSION)-perf .

docker-build-grpc: Dockerfile $(GO_FILES)
	docker buildx build --load --build-arg utils=1 --build-arg net=1 --build-arg grpc=1 --build-arg COMMIT=$(COMMIT) --build-arg VERSION=$(VERSION) -t $(IMAGE):$(VERSION)-grpc .

docker-push: Dockerfile $(GO_FILES)
	docker buildx build --push --platform linux/amd64,linux/arm64 --build-arg utils=1 --build-arg COMMIT=$(COMMIT) --build-arg VERSION=$(VERSION) -t $(IMAGE):$(VERSION) .

docker-push-utils: Dockerfile $(GO_FILES)
	docker buildx build --push --platform linux/amd64,linux/arm64 --build-arg utils=1 --build-arg COMMIT=$(COMMIT) --build-arg VERSION=$(VERSION) -t $(IMAGE):$(VERSION)-utils .

docker-push-net: Dockerfile $(GO_FILES)
	docker buildx build --push --platform linux/amd64,linux/arm64 --build-arg utils=1 --build-arg net=1 --build-arg COMMIT=$(COMMIT) --build-arg VERSION=$(VERSION) -t $(IMAGE):$(VERSION)-net .

docker-push-kube: Dockerfile $(GO_FILES)
	docker buildx build --push --platform linux/amd64,linux/arm64 --build-arg utils=1 --build-arg net=1 --build-arg kube=1 --build-arg COMMIT=$(COMMIT) --build-arg VERSION=$(VERSION) -t $(IMAGE):$(VERSION)-kube .

docker-push-perf: Dockerfile $(GO_FILES)
	docker buildx build --push --platform linux/amd64,linux/arm64 --build-arg utils=1 --build-arg net=1 --build-arg perf=1 --build-arg COMMIT=$(COMMIT) --build-arg VERSION=$(VERSION) -t $(IMAGE):$(VERSION)-perf .

docker-push-grpc: Dockerfile $(GO_FILES)
	docker buildx build --push --platform linux/amd64,linux/arm64 --build-arg utils=1 --build-arg net=1 --build-arg grpc=1 --build-arg COMMIT=$(COMMIT) --build-arg VERSION=$(VERSION) -t $(IMAGE):$(VERSION)-grpc .

docker-run: docker-build
	docker run -d --rm --name goto -p8080:8080 -it $(IMAGE):$(VERSION) /app/goto --port 8080