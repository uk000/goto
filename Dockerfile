FROM golang:alpine3.11

RUN echo 'https://nl.alpinelinux.org/alpine/v3.11/main' > /etc/apk/repositories

RUN apk add --no-cache --update \
  curl \
  wget \
  jq \
  netcat-openbsd \
  bash \
  nmap \
  iputils  \
  mtr \
  tcpdump \
  conntrack-tools \
  tcpflow \
  iftop \
  net-tools \
  bind-tools \
  busybox \
  busybox-extras


RUN mkdir /app
ADD ./cmd/ /app/cmd
ADD ./pkg/ /app/pkg
ADD ./go.mod /app/go.mod
ADD ./main.go /app/main.go

WORKDIR /app
RUN go build -o goto .

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