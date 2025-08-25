# usage: make VERSION=1.0.0 GOOS=darwin|linux|window release

# always run these targets
.PHONY: all clean

# variables
OUT := goto
GO_FILES := $(shell find . -name '*.go' | grep -v /vendor/)

GOOS=darwin
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
	docker build --build-arg COMMIT=$(COMMIT) --build-arg VERSION=$(VERSION) -t $(IMAGE):$(VERSION) --platform linux/amd64 .
	docker build --build-arg COMMIT=$(COMMIT) --build-arg VERSION=$(VERSION) -t $(IMAGE):$(VERSION)-arm64 --platform linux/arm64 .

docker-build-ip: Dockerfile $(GO_FILES)
	docker build --build-arg iputils=1 --build-arg COMMIT=$(COMMIT) --build-arg VERSION=$(VERSION) -t $(IMAGE):$(VERSION) --platform linux/amd64 .
	docker build --build-arg iputils=1 --build-arg COMMIT=$(COMMIT) --build-arg VERSION=$(VERSION) -t $(IMAGE):$(VERSION)-arm64 --platform linux/arm64 .

docker-build-kube: Dockerfile $(GO_FILES)
	docker build --build-arg kube=1 --build-arg COMMIT=$(COMMIT) --build-arg VERSION=$(VERSION) -t $(IMAGE):$(VERSION) --platform linux/amd64 .
	docker build --build-arg kube=1 --build-arg COMMIT=$(COMMIT) --build-arg VERSION=$(VERSION) -t $(IMAGE):$(VERSION)-arm64 --platform linux/arm64 .

docker-run: docker-build
	docker run -d --rm --name goto -p8080:8080 -it $(IMAGE):$(VERSION) /app/goto --port 8080