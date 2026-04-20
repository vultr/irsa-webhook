.PHONY: build push deploy certs test clean

IMAGE_NAME ?= ewr.vultrcr.com/chansey/irsa-webhook
IMAGE_TAG ?= latest
NAMESPACE ?= irsa-system

# Build the Go binary
build:
	CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -a -o webhook main.go

# Build Docker image
docker-build:
	docker build -t $(IMAGE_NAME):$(IMAGE_TAG) .

# Push Docker image
docker-push:
	docker push $(IMAGE_NAME):$(IMAGE_TAG)

# Generate TLS certificates
certs:
	chmod +x generate-certs.sh
	./generate-certs.sh

# Deploy to Kubernetes
deploy:
	@if [ ! -f .ca-bundle.txt ]; then \
		echo "Error: .ca-bundle.txt not found. Run 'make certs' first."; \
		exit 1; \
	fi
	@CA_BUNDLE=$$(cat .ca-bundle.txt) && \
	sed "s|CA_BUNDLE_PLACEHOLDER|$$CA_BUNDLE|g" deploy.yaml | kubectl apply -f -

# Undeploy from Kubernetes
undeploy:
	kubectl delete -f deploy.yaml

# View logs
logs:
	kubectl logs -n $(NAMESPACE) -l app=irsa-webhook -f

# Test with example pod
test-example:
	kubectl apply -f example.yaml
	@echo "Waiting for pod to be ready..."
	@kubectl wait --for=condition=Ready pod/example-app --timeout=60s || true
	@echo ""
	@echo "Pod logs:"
	@kubectl logs example-app

# Clean up test resources
clean-example:
	kubectl delete -f example.yaml --ignore-not-found

# Run Go tests
go-test:
	go test -v ./...

# Format Go code
fmt:
	go fmt ./...

# Run Go vet
vet:
	go vet ./...

# Download Go dependencies
deps:
	go mod download
	go mod tidy

# Complete build and deploy pipeline
all: deps build docker-build docker-push certs deploy

# Check webhook status
status:
	@echo "=== Webhook Deployment ==="
	@kubectl get deployment -n $(NAMESPACE) irsa-webhook
	@echo ""
	@echo "=== Webhook Pods ==="
	@kubectl get pods -n $(NAMESPACE) -l app=irsa-webhook
	@echo ""
	@echo "=== Webhook Service ==="
	@kubectl get svc -n $(NAMESPACE) irsa-webhook
	@echo ""
	@echo "=== MutatingWebhookConfiguration ==="
	@kubectl get mutatingwebhookconfiguration irsa-webhook

# Clean all resources
clean:
	kubectl delete -f deploy.yaml --ignore-not-found
	kubectl delete namespace $(NAMESPACE) --ignore-not-found
	rm -f webhook

# Restart webhook deployment
restart:
	kubectl rollout restart deployment -n $(NAMESPACE) irsa-webhook
	kubectl rollout status deployment -n $(NAMESPACE) irsa-webhook

# Help
help:
	@echo "Available targets:"
	@echo "  build          - Build Go binary"
	@echo "  docker-build   - Build Docker image"
	@echo "  docker-push    - Push Docker image"
	@echo "  certs          - Generate TLS certificates"
	@echo "  deploy         - Deploy to Kubernetes"
	@echo "  undeploy       - Remove from Kubernetes"
	@echo "  logs           - View webhook logs"
	@echo "  test-example   - Deploy and test example pod"
	@echo "  clean-example  - Remove example pod"
	@echo "  go-test        - Run Go tests"
	@echo "  fmt            - Format Go code"
	@echo "  vet            - Run Go vet"
	@echo "  deps           - Download dependencies"
	@echo "  all            - Complete build and deploy"
	@echo "  status         - Check webhook status"
	@echo "  clean          - Remove all resources"
	@echo "  restart        - Restart webhook deployment"
	@echo "  help           - Show this help"