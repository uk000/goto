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

clean:
	rm -rf goto

build: $(GO_FILES)
	GOOS=$(GOOS) GOARCH=amd64 go build -o $(OUT) -ldflags="-extldflags \"-static\" -w -s -X goto/cmd.Version=$(VERSION) -X goto/cmd.Commit=$(COMMIT)" .
	@chmod +x $(OUT)

run: build
	./$(OUT) --port 8080

docker-build: Dockerfile $(GO_FILES)
	docker build --build-arg COMMIT=$(COMMIT) --build-arg VERSION=$(VERSION) -t $(IMAGE):$(VERSION) .
	docker build --build-arg COMMIT=$(COMMIT) --build-arg VERSION=$(VERSION) -t $(IMAGE):latest .

docker-run: docker-build
	docker run -d --rm --name goto -p8080:8080 -it $(IMAGE):$(VERSION) /app/goto --port 8080