##############################################
# Stage 1: Build
##############################################
FROM --platform=${BUILDPLATFORM} golang:1.23-alpine AS builder

ARG VERSION=dev
ARG TARGETOS=linux
ARG TARGETARCH

RUN apk add --no-cache git ca-certificates tzdata

WORKDIR /app

COPY go.mod go.sum ./
RUN go mod download

COPY . .

RUN CGO_ENABLED=0 GOOS=${TARGETOS} GOARCH=${TARGETARCH} go build \
    -ldflags "-s -w -X main.Version=${VERSION}" \
    -o ess_mcp_server .

##############################################
# Stage 2: Runtime
##############################################
FROM alpine:3.19

RUN apk add --no-cache ca-certificates tzdata \
    && cp /usr/share/zoneinfo/Asia/Shanghai /etc/localtime \
    && echo "Asia/Shanghai" > /etc/timezone

WORKDIR /app

COPY --from=builder /app/ess_mcp_server .
COPY --from=builder /app/config.yaml .
COPY --from=builder /app/yaml/ ./yaml/

RUN mkdir -p /app/log

EXPOSE 8080

ENTRYPOINT ["./ess_mcp_server"]
