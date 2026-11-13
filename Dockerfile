FROM node:22-bookworm-slim AS web
WORKDIR /src/web
COPY web/package*.json ./
RUN npm ci
COPY web/ ./
RUN npm run build

FROM golang:1.27.3-bookworm AS backend
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY cmd/ cmd/
COPY internal/ internal/
RUN CGO_ENABLED=0 go build -trimpath -ldflags='-s -w' -o /nodehub ./cmd/nodehub
RUN CGO_ENABLED=0 go build -trimpath -ldflags='-s -w' -o /nodehub-agent ./cmd/nodehub-agent

RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -trimpath -ldflags='-s -w' -o /nodehub-agent-linux-amd64 ./cmd/nodehub-agent
RUN CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build -trimpath -ldflags='-s -w' -o /nodehub-agent-linux-arm64 ./cmd/nodehub-agent

FROM scratch AS agent-binary
COPY --from=backend /nodehub-agent /nodehub-agent

FROM debian:bookworm-slim AS server
RUN apt-get update && apt-get install -y --no-install-recommends ca-certificates && rm -rf /var/lib/apt/lists/* && useradd --uid 10001 --create-home nodehub && mkdir -p /data /app && chown nodehub:nodehub /data
WORKDIR /app
COPY --from=backend /nodehub /app/nodehub
COPY --from=web /src/web/dist /app/web
COPY client/bootstrap.sh client/bootstrap.py client/nodehub-init.py client/mesh-install.py client/install.sh client/nodehub-init.service client/enable-overlayroot.example.sh /app/bootstrap/client/
COPY agent/install.sh agent/config.example.json agent/nodehub-agent.service /app/bootstrap/agent/
COPY --from=backend /nodehub-agent-linux-amd64 /app/bootstrap/bin/nodehub-agent-linux-amd64
COPY --from=backend /nodehub-agent-linux-arm64 /app/bootstrap/bin/nodehub-agent-linux-arm64
USER 10001:10001
EXPOSE 8080
ENTRYPOINT ["/app/nodehub"]
CMD ["-config", "/app/config.json"]
