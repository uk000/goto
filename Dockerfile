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


FROM alpine:3.12 as release-base
RUN echo 'http://nl.alpinelinux.org/alpine/v3.12/main' > /etc/apk/repositories
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


FROM release-base as release

WORKDIR /goto
COPY --from=builder /goto/goto .

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