FROM golang:1.23-alpine as builder-base
RUN echo 'http://nl.alpinelinux.org/alpine/v3.12/main' > /etc/apk/repositories
RUN apk update
RUN apk add openssl
RUN apk add ca-certificates
RUN update-ca-certificates

RUN openssl s_client -showcerts -connect github.com:443 </dev/null 2>/dev/null | openssl x509 -outform PEM > /usr/local/share/ca-certificates/github.crt
RUN openssl s_client -showcerts -connect proxy.golang.org:443 </dev/null 2>/dev/null | openssl x509 -outform PEM >  /usr/local/share/ca-certificates/golang-proxy.crt
RUN openssl s_client -showcerts -connect sum.golang.org:443 </dev/null 2>/dev/null | openssl x509 -outform PEM >  /usr/local/share/ca-certificates/golang-sum.crt
RUN update-ca-certificates

RUN mkdir /goto
ADD ./go.mod /goto/go.mod

WORKDIR /goto
RUN go mod download

FROM builder-base as builder

ARG COMMIT 
ARG VERSION 

ADD ./cmd/ /goto/cmd
ADD ./pkg/ /goto/pkg
ADD ./main.go /goto/main.go

WORKDIR /goto

RUN go build -mod=mod -o goto -ldflags="-extldflags \"-static\" -w -s -X goto/cmd.Version=$VERSION -X goto/cmd.Commit=$COMMIT" .

WORKDIR /tmp

ENV FORTIO_REPO=https://github.com/fortio/fortio
ENV FORTIO_VERSION=1.69.1

RUN wget ${FORTIO_REPO}/releases/download/v${FORTIO_VERSION}/fortio-linux_arm64-${FORTIO_VERSION}.tgz
RUN tar -xzf fortio-linux_arm64-${FORTIO_VERSION}.tgz

ENV DNSPING_REPO=https://github.com/fortio/dnsping
ENV DNSPING_VERSION=1.9.0

RUN wget ${DNSPING_REPO}/releases/download/v${DNSPING_VERSION}/dnsping_${DNSPING_VERSION}_linux_arm64.tar.gz
RUN tar -xzf dnsping_${DNSPING_VERSION}_linux_arm64.tar.gz

ENV GHZ_REPO=https://github.com/bojand/ghz
ENV DNSPING_VERSION=0.120.0


FROM alpine:3.21.3 as release-base
RUN echo 'http://nl.alpinelinux.org/alpine/v3.21/main' > /etc/apk/repositories
RUN apk update
RUN apk add curl
RUN apk add wget
RUN apk add bash
RUN apk add nmap
RUN apk add iputils
RUN apk add tcpdump
RUN apk add conntrack-tools
RUN apk add tcpflow
RUN apk add iftop
RUN apk add bind-tools
RUN apk add busybox
RUN apk add busybox-extras
RUN apk add netcat-openbsd
RUN apk add openssl
RUN apk add jq
RUN apk add hey

# ENV LANG en_US.UTF-8
# ENV LANGUAGE en_US:en
# ENV LC_ALL en_US.UTF-8

# ENV GLIBC_REPO=https://github.com/sgerrand/alpine-pkg-glibc
# ENV GLIBC_VERSION=2.35-r1

# RUN set -ex && \
#     apk --update add libstdc++ curl ca-certificates && \
#     for pkg in glibc-${GLIBC_VERSION} glibc-bin-${GLIBC_VERSION}; \
#         do curl -sSL ${GLIBC_REPO}/releases/download/${GLIBC_VERSION}/${pkg}.apk -o /tmp/${pkg}.apk; done && \
#     apk add --allow-untrusted /tmp/*.apk && \
#     rm -v /tmp/*.apk && \
#     /usr/glibc-compat/sbin/ldconfig /lib /usr/glibc-compat/lib

# RUN apk add dpkg


FROM release-base as release

WORKDIR /goto
COPY --from=builder /goto/goto .
COPY --from=builder /tmp/usr/bin/fortio .
COPY --from=builder /tmp/dnsping .
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
USER 10000

ENTRYPOINT ["/goto/goto"]
CMD ["--port", "8080"]