# syntax=docker/dockerfile:1
FROM --platform=$BUILDPLATFORM node:22.23.2-alpine AS frontend
WORKDIR /frontend
COPY frontend/package.json frontend/package-lock.json ./
RUN npm ci --no-audit --no-fund
COPY frontend/ ./
RUN npm run build

FROM --platform=$BUILDPLATFORM golang:1.26.11 AS build
WORKDIR /src
COPY backend/go.mod backend/go.sum ./
RUN go mod download
COPY backend/ ./
COPY --from=frontend /frontend/dist/browser/ ./internal/admin/assets/ui/
ARG TARGETOS
ARG TARGETARCH
RUN CGO_ENABLED=0 GOOS="$TARGETOS" GOARCH="$TARGETARCH" go build -trimpath -ldflags="-s -w" -o /out/gateway ./cmd/gateway \
    && mkdir -p /out/data /out/config

FROM gcr.io/distroless/static-debian12:nonroot AS gateway
COPY --from=build /out/gateway /usr/local/bin/gateway
COPY --from=build --chown=65532:65532 /out/data /data
COPY --from=build --chown=65532:65532 /out/config /config
USER 65532:65532
EXPOSE 8080
ENTRYPOINT ["/usr/local/bin/gateway"]
