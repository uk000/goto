FROM golang:1.15.2-alpine3.12 as builder

ARG COMMIT 
ARG VERSION 

RUN mkdir /app
ADD ./cmd/ /app/cmd
ADD ./pkg/ /app/pkg
ADD ./go.mod /app/go.mod
ADD ./main.go /app/main.go

WORKDIR /app
RUN go env -w GOINSECURE=* && go build -o goto -ldflags="-extldflags \"-static\" -w -s -X goto/cmd.Version=$VERSION -X goto/cmd.Commit=$COMMIT" .

FROM alpine:3.12 as release
RUN echo 'https://nl.alpinelinux.org/alpine/v3.12/main' > /etc/apk/repositories \
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