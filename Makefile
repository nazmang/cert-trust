SHELL ?= /usr/bin/bash

# Image settings
IMAGE ?= ghcr.io/nazmang/cert-trust
TAG ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo 0.1.0)

# Helm settings
RELEASE ?= cert-trust
NAMESPACE ?= cert-trust
CHART_PATH ?= charts/cert-trust

.PHONY: help
help:
	@echo "Targets:"
	@echo "  build            Build controller binary"
	@echo "  docker-build     Build docker image $(IMAGE):$(TAG)"
	@echo "  docker-push      Push docker image $(IMAGE):$(TAG)"
	@echo "  helm-install     Install/upgrade Helm release $(RELEASE) in $(NAMESPACE)"
	@echo "  helm-uninstall   Uninstall Helm release $(RELEASE) from $(NAMESPACE)"
	@echo "  print            Show current vars"

.PHONY: build
build:
	CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -ldflags="-s -w" -o bin/manager ./cmd/cert-trust

.PHONY: docker-build
docker-build:
	docker build -t $(IMAGE):$(TAG) .

.PHONY: docker-push
docker-push:
	docker push $(IMAGE):$(TAG)

.PHONY: helm-install
helm-install:
	helm upgrade --install $(RELEASE) $(CHART_PATH) \
	  --namespace $(NAMESPACE) --create-namespace \
	  --set image.repository=$(IMAGE) \
	  --set image.tag=$(TAG)

.PHONY: helm-uninstall
helm-uninstall:
	helm uninstall $(RELEASE) --namespace $(NAMESPACE) || true

.PHONY: print
print:
	@echo IMAGE=$(IMAGE)
	@echo TAG=$(TAG)
	@echo RELEASE=$(RELEASE)
	@echo NAMESPACE=$(NAMESPACE)
	@echo CHART_PATH=$(CHART_PATH)
