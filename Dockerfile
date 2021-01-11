FROM golang:1.15.6-alpine3.12 as builder

ARG COMMIT 
ARG VERSION 

RUN mkdir /app
ADD ./cmd/ /app/cmd
ADD ./pkg/ /app/pkg
ADD ./go.mod /app/go.mod
ADD ./main.go /app/main.go

WORKDIR /app
RUN echo 'http://nl.alpinelinux.org/alpine/v3.12/main' > /etc/apk/repositories \
  && apk add --no-cache git openssl ca-certificates && update-ca-certificates

RUN openssl s_client -showcerts -connect github.com:443 </dev/null 2>/dev/null | openssl x509 -outform PEM > /usr/local/share/ca-certificates/github.crt
RUN openssl s_client -showcerts -connect proxy.golang.org:443 </dev/null 2>/dev/null | openssl x509 -outform PEM >  /usr/local/share/ca-certificates/golang.crt
RUN update-ca-certificates

RUN go build -o goto -ldflags="-extldflags \"-static\" -w -s -X goto/cmd.Version=$VERSION -X goto/cmd.Commit=$COMMIT" .

FROM alpine:3.12 as release
RUN echo 'http://nl.alpinelinux.org/alpine/v3.12/main' > /etc/apk/repositories \
  && \
  apk add --no-cache --update \
  curl \
  wget \
  bash \
  nmap \
  iputils  \
  tcpdump \
  conntrack-tools \
  tcpflow \
  iftop \
  bind-tools \
  busybox \
  busybox-extras\
  netcat-openbsd \
  openssl \
  jq

WORKDIR /app
COPY --from=builder /app/goto .

EXPOSE 8080

RUN addgroup --gid 10001 app \
    && adduser \
    --disabled-password \
    --gecos "" \
    --home /app \
    --ingroup app \
    --uid 10000 \
    app
USER 10000

ENTRYPOINT ["/app/goto"]
CMD ["--port", "8080"]