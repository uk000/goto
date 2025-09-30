FROM golang:1.24-alpine AS builder-base
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
RUN go mod download

FROM builder-base AS builder

ARG COMMIT 
ARG VERSION 

ADD ./cmd/ /goto/cmd
ADD ./ctl/ /goto/ctl
ADD ./pkg/ /goto/pkg
ADD ./main.go /goto/main.go

WORKDIR /goto

RUN --mount=type=cache,target="/root/.cache/go-build" go build -mod=mod -o goto -ldflags="-extldflags \"-static\" -w -s -X goto/global.Version=$VERSION -X goto/global.Commit=$COMMIT" .

WORKDIR /tmp


FROM alpine:3.22.1 AS release-base

ARG kube
ARG netutils
ARG perf
ARG ssl
ARG grpc

RUN echo 'http://nl.alpinelinux.org/alpine/v3.22/main' > /etc/apk/repositories && \
    echo 'http://nl.alpinelinux.org/alpine/v3.22/community' >> /etc/apk/repositories

RUN apk update \
	&& apk add --no-cache curl jq bash sudo su-exec

# RUN apk add --no-cache --repository=http://dl-cdn.alpinelinux.org/alpine/edge/testing grpcurl 

RUN if [[ -n "$netutils" ]] ; then apk add --no-cache nmap-ncat netcat-openbsd socat iputils iproute2 tcpdump bind-tools iptables ipvsadm tcpflow; echo "netutils=$netutils"; fi
RUN if [[ -n "$kube" ]] ; then apk add --no-cache kubectl etcd-ctl; echo "kubectl=$kube"; fi
RUN if [[ -n "$perf" ]] ; then apk add --no-cache hey iftop; echo "perf=$perf"; fi
RUN if [[ -n "$ssl" ]] ; then apk add --no-cache openssl; echo "perf=$ssl"; fi
RUN if [[ -n "$grpc" ]] ; then apk add --no-cache grpcurl; echo "perf=$grpc"; fi

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
