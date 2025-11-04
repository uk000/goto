FROM golang:1.25-alpine AS builder-base

ARG GOOS
ARG GOARCH

RUN echo 'http://nl.alpinelinux.org/alpine/v3.22/main' > /etc/apk/repositories
RUN echo 'http://nl.alpinelinux.org/alpine/v3.22/community' >> /etc/apk/repositories
RUN apk update \
		&& apk add --no-cache openssl \
		&& apk add --no-cache ca-certificates \
		&& rm -rf /var/cache/apk/* \
		&& update-ca-certificates

RUN openssl s_client -showcerts -connect github.com:443 </dev/null 2>/dev/null | openssl x509 -outform PEM > /usr/local/share/ca-certificates/github.crt \
	&& openssl s_client -showcerts -connect proxy.golang.org:443 </dev/null 2>/dev/null | openssl x509 -outform PEM >  /usr/local/share/ca-certificates/golang-proxy.crt \
	&& openssl s_client -showcerts -connect sum.golang.org:443 </dev/null 2>/dev/null | openssl x509 -outform PEM >  /usr/local/share/ca-certificates/golang-sum.crt \
	&& openssl s_client -showcerts -connect dl-cdn.alpinelinux.org:443 </dev/null 2>/dev/null | openssl x509 -outform PEM > /usr/local/share/ca-certificates/alpine.crt \
	&& update-ca-certificates


RUN mkdir /goto
ADD ./go.mod /goto/go.mod

WORKDIR /goto
RUN GOOS=${GOOS} GOARCH=${GOARCH} go mod download

FROM builder-base AS builder

ARG COMMIT 
ARG VERSION 

ADD ./cmd/ /goto/cmd
ADD ./ctl/ /goto/ctl
ADD ./pkg/ /goto/pkg
ADD ./main.go /goto/main.go

WORKDIR /goto

RUN --mount=type=cache,target="/root/.cache/go-build" GOOS=${GOOS} GOARCH=${GOARCH} go build -mod=mod -o goto -ldflags="-extldflags \"-static\" -w -s -X goto/global.Version=$VERSION -X goto/global.Commit=$COMMIT" .

WORKDIR /tmp


FROM alpine:3.22 AS release-base-core

ARG utils

RUN echo 'http://nl.alpinelinux.org/alpine/v3.22/main' > /etc/apk/repositories && \
    echo 'http://nl.alpinelinux.org/alpine/v3.22/community' >> /etc/apk/repositories

RUN apk update && apk add bash sudo su-exec;
RUN if [[ -n "$utils" ]] ; then apk add curl jq; echo "utils=$utils"; fi

FROM release-base-core AS release-base-net

ARG net

RUN if [[ -n "$net" ]] ; then apk add nmap nmap-ncat netcat-openbsd socat tcpdump bind-tools iptables ipvsadm openssl; echo "netutils=$netutils"; fi


FROM release-base-net AS release-base

ARG kube
ARG perf
ARG grpc

RUN echo "kubectl=$kube, perf=$perf, ssl=$ssl, grpc=$grpc"

RUN if [[ -n "$kube" ]] ; then apk add kubectl etcd-ctl; echo "kube=$kube"; fi
RUN if [[ -n "$perf" ]] ; then apk add hey iftop; echo "perf=$perf"; fi
RUN if [[ -n "$grpc" ]] ; then \
		apk add --repository=http://dl-cdn.alpinelinux.org/alpine/edge/testing grpcurl; echo "grpc=$grpc"; \
	fi

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

USER 10000

ENTRYPOINT ["/goto/goto"]
CMD ["--ports", "8001,3000/rpc,4000/rpc,5000/rpc,10000/tcp", "--rpcPort", "8080", "--grpcPort", "8888"]
