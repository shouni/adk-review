FROM golang:1.26-alpine AS builder

RUN apk add --no-cache tzdata ca-certificates curl jq
WORKDIR /app

# SSH のホスト鍵は GitHub の API から取ってきて焼き込みます。
#
# **リポジトリに固定しません。** 鍵は信頼の起点なので、古びた値を持ち続けるより
# 出どころに追従するほうが安全です。固定すると、ローテート後は clone が全滅するのに
# 気付くのは本番で、直すにはコード変更とデプロイが要ります。ローテートの理由が漏洩
# だった場合は、漏れた鍵を固定し続けることにもなります。取得の失敗ならビルドが赤くなる
# だけで、再実行で済みます。
#
# ★ この行は **COPY . . より前** に置きます。後ろにあるとレイヤキャッシュが効かず、
# ソースを 1 文字変えるだけで毎ビルド GitHub API を叩いていました（未認証呼び出しなので、
# Cloud Build の共有 IP ではレート制限に当たる余地があります）。
RUN mkdir -p /etc/ssh \
    && curl -fsSL --retry 3 https://api.github.com/meta \
       | jq -er '.ssh_keys[] | "github.com \(.)"' > /etc/ssh/ssh_known_hosts \
    && test -s /etc/ssh/ssh_known_hosts \
    && grep -q "^github.com ssh-ed25519 " /etc/ssh/ssh_known_hosts

COPY go.mod go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod go mod download
COPY . .
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 go build -ldflags="-s -w" -o /app/main ./main.go

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
