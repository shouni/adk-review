FROM golang:1.26-alpine AS builder

RUN apk add --no-cache tzdata ca-certificates curl jq
WORKDIR /app

# SSH のホスト鍵は GitHub の API から取得して焼き込みます。
#
# 鍵をリポジトリに置いて COPY しないでください。ローテートに追従できなくなり、
# clone が全滅するのに気付くのは本番、直すにはデプロイが要ります。
#
# このステップは COPY . . より前に置いてください。後ろだとソース変更のたびに
# レイヤキャッシュが外れ、未認証の API 呼び出しを毎ビルド繰り返します。
RUN mkdir -p /etc/ssh \
    && curl -fsSL --retry 3 https://api.github.com/meta \
       | jq -er '.ssh_keys[] | "github.com \(.)"' > /etc/ssh/ssh_known_hosts \
    && test -s /etc/ssh/ssh_known_hosts \
    && grep -q "^github.com ssh-ed25519 " /etc/ssh/ssh_known_hosts

COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -ldflags="-s -w" -o /app/main ./main.go

FROM scratch

COPY --from=builder /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/
COPY --from=builder /usr/share/zoneinfo /usr/share/zoneinfo
COPY --from=builder /etc/ssh/ssh_known_hosts /etc/ssh/ssh_known_hosts
COPY --from=builder /tmp /tmp

WORKDIR /app
COPY --chown=65532:65532 --from=builder /app/main /app/main

ENV TZ=Asia/Tokyo

ENV SSH_KNOWN_HOSTS=/etc/ssh/ssh_known_hosts

EXPOSE 8080

USER 65532:65532

CMD ["/app/main"]
