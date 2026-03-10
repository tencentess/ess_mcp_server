# ==============================================================================
# ESS MCP Server - Makefile
# ==============================================================================

APP_NAME    := ess_mcp_server
VERSION     ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo "dev")
IMAGE_NAME  ?= ccr.ccs.tencentyun.com/pulse-line-prod/ess-mcp-server
IMAGE_TAG   ?= $(VERSION)
REGISTRY    ?= ""
PORT        ?= 8080

GO          := go
GOFLAGS     := -v
LDFLAGS     := -s -w -X main.Version=$(VERSION)

# ------------------------------------------------------------------------------
# 本地开发
# ------------------------------------------------------------------------------

## run: 本地直接运行
.PHONY: run
run:
	$(GO) run -ldflags "$(LDFLAGS)" .

## build: 编译二进制
.PHONY: build
build:
	CGO_ENABLED=0 $(GO) build $(GOFLAGS) -ldflags "$(LDFLAGS)" -o bin/$(APP_NAME) .

## clean: 清理构建产物
.PHONY: clean
clean:
	rm -rf bin/ log/

## tidy: 整理 go module
.PHONY: tidy
tidy:
	$(GO) mod tidy

## test: 运行测试
.PHONY: test
test:
	$(GO) test ./... -v -cover

# ------------------------------------------------------------------------------
# Docker
# ------------------------------------------------------------------------------

PLATFORMS ?= linux/amd64,linux/arm64

## docker-build: 构建当前平台 Docker 镜像
.PHONY: docker-build
docker-build:
	docker buildx build --load --build-arg VERSION=$(VERSION) -t $(IMAGE_NAME):$(IMAGE_TAG) .

## docker-build-multiarch: 构建多平台镜像并推送
.PHONY: docker-build-multiarch
docker-build-multiarch:
	docker buildx build --platform $(PLATFORMS) --build-arg VERSION=$(VERSION) -t $(IMAGE_NAME):$(IMAGE_TAG) --push .

## docker-run: 本地 Docker 运行
.PHONY: docker-run
docker-run:
	docker run --rm -it \
		-p $(PORT):8080 \
		-v $(PWD)/config.yaml:/app/config.yaml:ro \
		$(IMAGE_NAME):$(IMAGE_TAG)

## docker-push: 推送镜像到仓库
.PHONY: docker-push
docker-push:
	docker push $(IMAGE_NAME):$(IMAGE_TAG)

## docker-stop: 停止运行中的容器
.PHONY: docker-stop
docker-stop:
	docker ps -q --filter ancestor=$(IMAGE_NAME):$(IMAGE_TAG) | xargs -r docker stop

## compose-up: Docker Compose 启动
.PHONY: compose-up
compose-up:
	docker compose up -d

## compose-down: Docker Compose 停止
.PHONY: compose-down
compose-down:
	docker compose down

## compose-logs: 查看 Docker Compose 日志
.PHONY: compose-logs
compose-logs:
	docker compose logs -f

# ------------------------------------------------------------------------------
# Kubernetes
# ------------------------------------------------------------------------------

## k8s-deploy: 部署到 Kubernetes
.PHONY: k8s-deploy
k8s-deploy:
	kubectl apply -f k8s/

## k8s-delete: 从 Kubernetes 删除
.PHONY: k8s-delete
k8s-delete:
	kubectl delete -f k8s/ --ignore-not-found

## k8s-status: 查看部署状态
.PHONY: k8s-status
k8s-status:
	kubectl get pods,svc,configmap -l app=$(APP_NAME)

# ------------------------------------------------------------------------------
# 帮助
# ------------------------------------------------------------------------------

## help: 显示帮助信息
.PHONY: help
help:
	@echo ""
	@echo "Usage: make <target>"
	@echo ""
	@echo "本地开发:"
	@echo "  run             本地直接运行 (go run)"
	@echo "  build           编译二进制到 bin/"
	@echo "  clean           清理构建产物"
	@echo "  tidy            整理 go module"
	@echo "  test            运行测试"
	@echo ""
	@echo "Docker:"
	@echo "  docker-build           构建当前平台镜像"
	@echo "  docker-build-multiarch 构建多平台镜像 (amd64+arm64) 并推送"
	@echo "  docker-run             本地 Docker 运行"
	@echo "  docker-push            推送镜像"
	@echo "  docker-stop            停止容器"
	@echo "  compose-up             Docker Compose 启动"
	@echo "  compose-down           Docker Compose 停止"
	@echo "  compose-logs           查看 Compose 日志"
	@echo ""
	@echo "Kubernetes:"
	@echo "  k8s-deploy      部署到 K8s"
	@echo "  k8s-delete      从 K8s 删除"
	@echo "  k8s-status      查看部署状态"
	@echo ""

.DEFAULT_GOAL := help
