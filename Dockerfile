# Set with --build-arg grpc=1 to include grpcurl, see the grpcurl stages below
ARG grpc

FROM golang:1.25-alpine AS builder-base

ARG TARGETOS
ARG TARGETARCH

# Go module proxy and checksum database, set with --build-arg to use a mirror instead of the public proxy. An empty value
# makes go use its default. Kept as ENV so that the stages built from this one (builder, grpcurl-build) get them too.
ARG GOPROXY
ARG GOSUMDB
ARG GONOSUMDB
ENV GOPROXY=${GOPROXY} GOSUMDB=${GOSUMDB} GONOSUMDB=${GONOSUMDB}

# Alpine package repos. To use a mirror instead of these, pass a repositories file as the secret apk_repositories (docker buildx
# build --secret id=apk_repositories,src=<file>): every RUN that uses apk mounts it over /etc/apk/repositories only while it
# runs, so the mirror is neither in the image layers nor in its history. Without the secret the public repos below are used.
RUN echo 'http://dl-cdn.alpinelinux.org/alpine/v3.22/main' > /etc/apk/repositories
RUN echo 'http://dl-cdn.alpinelinux.org/alpine/v3.22/community' >> /etc/apk/repositories
RUN --mount=type=secret,id=apk_repositories,target=/etc/apk/repositories \
		apk update \
		&& apk add --no-cache openssl \
		&& apk add --no-cache ca-certificates \
		&& rm -rf /var/cache/apk/* \
		&& update-ca-certificates

# Behind a TLS-intercepting corporate proxy (https_proxy/HTTPS_PROXY build args, see PROXY_ARGS in the Makefile), connect
# through the proxy and trust the self-signed root CA at the top of the chain it presents. Without a proxy, fetch each
# host's cert directly.
RUN proxy="${https_proxy:-$HTTPS_PROXY}"; proxy="${proxy#*://}"; proxy="${proxy%/}"; \
	if [ -n "$proxy" ]; then \
		openssl s_client -proxy "$proxy" -servername github.com -showcerts -connect github.com:443 </dev/null 2>/dev/null \
			| awk '/BEGIN CERTIFICATE/{buf=""} {buf=buf $0 "\n"} /END CERTIFICATE/{last=buf} END{printf "%s", last}' > /tmp/proxy-root.crt; \
		if [ -s /tmp/proxy-root.crt ] \
			&& [ "$(openssl x509 -in /tmp/proxy-root.crt -noout -subject_hash)" = "$(openssl x509 -in /tmp/proxy-root.crt -noout -issuer_hash)" ]; then \
			cp /tmp/proxy-root.crt /usr/local/share/ca-certificates/proxy-root.crt && update-ca-certificates; \
		else \
			echo "WARNING: proxy $proxy did not present a self-signed root CA, not adding it to trusted certs"; \
		fi; \
		rm -f /tmp/proxy-root.crt; \
	else \
		openssl s_client -showcerts -connect github.com:443 </dev/null 2>/dev/null | openssl x509 -outform PEM > /usr/local/share/ca-certificates/github.crt \
		&& openssl s_client -showcerts -connect proxy.golang.org:443 </dev/null 2>/dev/null | openssl x509 -outform PEM >  /usr/local/share/ca-certificates/golang-proxy.crt \
		&& openssl s_client -showcerts -connect sum.golang.org:443 </dev/null 2>/dev/null | openssl x509 -outform PEM >  /usr/local/share/ca-certificates/golang-sum.crt \
		&& openssl s_client -showcerts -connect dl-cdn.alpinelinux.org:443 </dev/null 2>/dev/null | openssl x509 -outform PEM > /usr/local/share/ca-certificates/alpine.crt \
		&& update-ca-certificates; \
	fi


RUN mkdir /goto
ADD ./go.mod ./go.sum /goto/

WORKDIR /goto
# The module cache is a cache mount, so that the modules aren't downloaded again when this layer is rebuilt (e.g. after a change
# to the Dockerfile above), and shared by the stages that build go code. They have to mount it the same way.
RUN --mount=type=cache,target=/go/pkg/mod GOOS=${TARGETOS} GOARCH=${TARGETARCH} go mod download

FROM builder-base AS builder

ARG TARGETOS
ARG TARGETARCH
ARG COMMIT
ARG VERSION

ADD ./cmd/ /goto/cmd
ADD ./ctl/ /goto/ctl
ADD ./pkg/ /goto/pkg
ADD ./main.go /goto/main.go

WORKDIR /goto

RUN --mount=type=cache,target=/go/pkg/mod --mount=type=cache,target="/root/.cache/go-build" GOOS=${TARGETOS} GOARCH=${TARGETARCH} go build -mod=mod -o goto -ldflags="-extldflags \"-static\" -w -s -X goto/pkg/global.Version=$VERSION -X goto/pkg/global.Commit=$COMMIT" .

WORKDIR /tmp


# grpcurl is not in the Alpine 3.22 repos (only in edge/testing), so it's built from source. The stage that is built is
# chosen by the grpc build arg: grpcurl-1 builds it, grpcurl-0 is empty, so builds without grpc don't build or copy it.
FROM builder-base AS grpcurl-build

ARG GRPCURL_VERSION=v1.9.4

RUN --mount=type=cache,target=/go/pkg/mod CGO_ENABLED=0 go install -ldflags="-w -s" github.com/fullstorydev/grpcurl/cmd/grpcurl@${GRPCURL_VERSION}

FROM scratch AS grpcurl-1
COPY --from=grpcurl-build /go/bin/grpcurl /grpcurl

FROM scratch AS grpcurl-0

FROM grpcurl-${grpc:-0} AS grpcurl


FROM alpine:3.22 AS release-base-core

ARG utils

RUN echo 'http://dl-cdn.alpinelinux.org/alpine/v3.22/main' > /etc/apk/repositories && \
    echo 'http://dl-cdn.alpinelinux.org/alpine/v3.22/community' >> /etc/apk/repositories

RUN --mount=type=secret,id=apk_repositories,target=/etc/apk/repositories \
	apk update && apk add bash sudo su-exec;
RUN --mount=type=secret,id=apk_repositories,target=/etc/apk/repositories \
	if [[ -n "$utils" ]] ; then apk add curl jq && echo "utils=$utils"; fi

FROM release-base-core AS release-base-net

ARG net

RUN --mount=type=secret,id=apk_repositories,target=/etc/apk/repositories \
	if [[ -n "$net" ]] ; then apk add nmap nmap-ncat netcat-openbsd socat tcpdump bind-tools iptables ipvsadm openssl && echo "net=$net"; fi


FROM release-base-net AS release-base

ARG kube
ARG perf
ARG grpc

RUN echo "kubectl=$kube, perf=$perf, ssl=$ssl, grpc=$grpc"

RUN --mount=type=secret,id=apk_repositories,target=/etc/apk/repositories \
	if [[ -n "$kube" ]] ; then apk add kubectl etcd-ctl && echo "kube=$kube"; fi
RUN --mount=type=secret,id=apk_repositories,target=/etc/apk/repositories \
	if [[ -n "$perf" ]] ; then apk add hey iftop && echo "perf=$perf"; fi
COPY --from=grpcurl / /usr/local/bin/

FROM release-base AS release

WORKDIR /goto
COPY --from=builder /goto/goto .
ENV PATH="$PATH:."

EXPOSE 8080

RUN addgroup --gid 10001 goto \
	&& adduser \
	--disabled-password \
	--gecos "" \
	--home /goto \
	--ingroup goto \
	--uid 10000 \
	goto
USER root
RUN su root \
	&& addgroup -S sudo \
	&& addgroup goto sudo \
	&& getent group sudo \
	&& echo '%sudo ALL = (ALL) NOPASSWD: ALL' > /etc/sudoers.d/nopasswd
# Create the global bash configuration directory and inject PS1
RUN mkdir -p /etc/bash && \
    echo "export PS1='\[\e[32m\]\${NAMESPACE:-\w}\[\e[m\]/\[\e[36m\]\${POD_NAME:-\h}\[\e[m\] [\[\e[33m\]\${NODE_NAME:-local}\[\e[m\]] \\$ '" >> /etc/bash/bashrc
# Ensure non-login shells (like kubectl exec -- bash) still load it
ENV BASH_ENV=/etc/bash/bashrc

USER 10000

ENTRYPOINT ["/goto/goto"]
CMD ["--ports", "8001,3000/rpc,4000/rpc,5000/rpc,10000/tcp", "--rpcPort", "8080", "--grpcPort", "8888"]
