FROM golang:1.24-alpine AS builder-base
RUN echo 'http://nl.alpinelinux.org/alpine/v3.22/main' > /etc/apk/repositories
RUN echo 'http://nl.alpinelinux.org/alpine/v3.22/community' >> /etc/apk/repositories
RUN apk update
RUN apk add openssl
RUN apk add ca-certificates
RUN update-ca-certificates

RUN openssl s_client -showcerts -connect github.com:443 </dev/null 2>/dev/null | openssl x509 -outform PEM > /usr/local/share/ca-certificates/github.crt \
	&& openssl s_client -showcerts -connect proxy.golang.org:443 </dev/null 2>/dev/null | openssl x509 -outform PEM >  /usr/local/share/ca-certificates/golang-proxy.crt \
	&& openssl s_client -showcerts -connect sum.golang.org:443 </dev/null 2>/dev/null | openssl x509 -outform PEM >  /usr/local/share/ca-certificates/golang-sum.crt \
	&& update-ca-certificates

RUN mkdir /goto
ADD ./go.mod /goto/go.mod

WORKDIR /goto
RUN go mod download

FROM builder-base AS builder

ARG COMMIT 
ARG VERSION 

ADD ./cmd/ /goto/cmd
ADD ./pkg/ /goto/pkg
ADD ./main.go /goto/main.go

WORKDIR /goto

RUN --mount=type=cache,target="/root/.cache/go-build" go build -mod=mod -o goto -ldflags="-extldflags \"-static\" -w -s -X goto/global.Version=$VERSION -X goto/global.Commit=$COMMIT" .

WORKDIR /tmp

# ENV DNSPING_REPO=https://github.com/fortio/dnsping
# ENV DNSPING_VERSION=1.9.0

# RUN wget ${DNSPING_REPO}/releases/download/v${DNSPING_VERSION}/dnsping_${DNSPING_VERSION}_linux_arm64.tar.gz
# RUN tar -xzf dnsping_${DNSPING_VERSION}_linux_arm64.tar.gz


FROM alpine:3.22.0 AS release-base

ARG kube
ARG iputils
ARG perf

RUN echo 'http://nl.alpinelinux.org/alpine/v3.22/main' > /etc/apk/repositories \
	&& echo 'http://nl.alpinelinux.org/alpine/v3.22/community' >> /etc/apk/repositories

RUN apk update \
	&& apk add --no-cache curl jq bash sudo su-exec

# RUN apk add --no-cache --repository=http://dl-cdn.alpinelinux.org/alpine/edge/testing grpcurl 

RUN if [[ -n "$iputils" ]] ; then apk add --no-cache nmap-ncat iputils iproute2 iptables ipvsadm tcpdump tcpflow bind-tools; echo "iputils=$iputils"; fi
RUN if [[ -n "$kube" ]] ; then apk add --no-cache kubectl; echo "kubectl=$kube"; fi
RUN if [[ -n "$perf" ]] ; then apk add hey; echo "perf=$perf"; fi

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
CMD ["--port", "8080"]