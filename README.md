# IRSA Mutating Admission Webhook for Kubernetes

This webhook implements IAM Roles for Service Accounts (IRSA) for Kubernetes clusters, allowing pods to assume AWS IAM roles using projected service account tokens.

## Features

- Automatically injects AWS credentials configuration into pods
- Uses projected service account tokens with custom audience
- Supports multiple containers and init containers
- Follows security best practices
- No external dependencies beyond Kubernetes API

## How It Works

When a pod is created, the webhook:

1. Extracts the ServiceAccount name from the pod spec
2. Fetches the ServiceAccount from the Kubernetes API
3. Checks for the `vultr.com/role-arn` annotation
4. If present, mutates the pod to inject:
   - **Environment Variables:**
     - `AWS_ROLE_ARN`: The IAM role ARN from the annotation
     - `AWS_WEB_IDENTITY_TOKEN_FILE`: Path to the projected token
     - `AWS_STS_REGIONAL_ENDPOINTS`: Set to "regional"
   - **Volume:** A projected ServiceAccount token volume with audience "vultr"
   - **Volume Mounts:** Mounts the token at `/var/run/secrets/vultr.com/serviceaccount`

## Prerequisites

- Kubernetes 1.20+ (for projected service account tokens)
- `kubectl` configured to access your cluster
- OpenSSL (for certificate generation)
- Go 1.24+ (for building from source)

## Quick Start

### 1. Build the Docker Image

```bash
docker build -t your-registry/irsa-webhook:latest .
docker push your-registry/irsa-webhook:latest
```

Update `deploy.yaml` with your image location.

### 2. Generate TLS Certificates

The webhook requires TLS certificates to communicate with the Kubernetes API server:

```bash
chmod +x generate-certs.sh
./generate-certs.sh
```

This script will:
- Generate a self-signed CA and certificate
- Create a Kubernetes secret with the certificates
- Update the MutatingWebhookConfiguration with the CA bundle

### 3. Deploy the Webhook

```bash
kubectl apply -f deploy.yaml
```

This creates:
- Namespace: `irsa-system`
- ServiceAccount with RBAC permissions
- Deployment with 2 replicas
- Service
- MutatingWebhookConfiguration

### 4. Verify Deployment

```bash
kubectl get pods -n irsa-system
kubectl logs -n irsa-system -l app=irsa-webhook
```

## Usage

### Annotate ServiceAccount

To enable IRSA for a ServiceAccount, add the `vultr.com/role-arn` annotation:

```yaml
apiVersion: v1
kind: ServiceAccount
metadata:
  name: my-app
  namespace: default
  annotations:
    vultr.com/role-arn: "arn:aws:iam::123456789012:role/my-app-role"
```

### Deploy a Pod

Any pod using this ServiceAccount will automatically receive the AWS configuration:

```yaml
apiVersion: v1
kind: Pod
metadata:
  name: my-app
  namespace: default
spec:
  serviceAccountName: my-app
  containers:
  - name: app
    image: amazon/aws-cli:latest
    command: ["sleep", "3600"]
```

### Verify Injection

Check that the pod has the injected configuration:

```bash
# Check environment variables
kubectl exec my-app -- env | grep AWS

# Check volume mount
kubectl exec my-app -- ls -la /var/run/secrets/vultr.com/serviceaccount

# Test AWS credentials
kubectl exec my-app -- aws sts get-caller-identity
```

## Configuration

### Environment Variables

The webhook supports the following environment variables:

- `TLS_CERT_PATH`: Path to TLS certificate (default: `/etc/webhook/certs/tls.crt`)
- `TLS_KEY_PATH`: Path to TLS private key (default: `/etc/webhook/certs/tls.key`)
- `PORT`: HTTPS port to listen on (default: `8443`)

### Webhook Configuration

Edit the `MutatingWebhookConfiguration` in `deploy.yaml`:

- **failurePolicy**: Set to `Fail` for production to block pods if webhook is unavailable
- **timeoutSeconds**: Adjust timeout based on cluster performance
- **namespaceSelector**: Control which namespaces are affected

## Security Considerations

1. **Least Privilege**: The webhook ServiceAccount only has permissions to read ServiceAccounts
2. **TLS**: All communication is encrypted using TLS 1.2+
3. **Non-root**: Container runs as non-root user (65532)
4. **Read-only filesystem**: Container has read-only root filesystem
5. **No privilege escalation**: Security context prevents privilege escalation

## Troubleshooting

### Webhook Not Mutating Pods

1. Check webhook logs:
   ```bash
   kubectl logs -n irsa-system -l app=irsa-webhook
   ```

2. Verify MutatingWebhookConfiguration:
   ```bash
   kubectl get mutatingwebhookconfiguration irsa-webhook -o yaml
   ```

3. Check if ServiceAccount has the annotation:
   ```bash
   kubectl get sa <service-account-name> -o yaml
   ```

### Certificate Issues

If you see TLS errors, regenerate certificates:

```bash
./generate-certs.sh
kubectl rollout restart deployment -n irsa-system irsa-webhook
```

### RBAC Permissions

If webhook can't fetch ServiceAccounts, verify RBAC:

```bash
kubectl auth can-i get serviceaccounts --as=system:serviceaccount:irsa-system:irsa-webhook --all-namespaces
```

## Development

### Local Testing

You can test the webhook logic locally:

```go
package main

import (
    "testing"
    corev1 "k8s.io/api/core/v1"
)

func TestGeneratePatches(t *testing.T) {
    ws := &WebhookServer{}
    pod := &corev1.Pod{
        Spec: corev1.PodSpec{
            Containers: []corev1.Container{
                {Name: "test"},
            },
        },
    }

    patches, err := ws.generatePatches(pod, "arn:aws:iam::123456789012:role/test")
    if err != nil {
        t.Fatalf("Failed to generate patches: %v", err)
    }

    if len(patches) == 0 {
        t.Error("Expected patches to be generated")
    }
}
```

### Building from Source

```bash
go mod download
go build -o webhook main.go
```

## Architecture

```
┌─────────────┐
│ Kubernetes  │
│ API Server  │
└──────┬──────┘
       │
       │ AdmissionReview Request
       │
       ▼
┌─────────────────┐
│ IRSA Webhook    │
│                 │
│ 1. Parse Pod    │
│ 2. Get SA       │◄────┐
│ 3. Check Anno   │     │
│ 4. Gen Patches  │     │
└─────────────────┘     │
                        │
                  ┌─────┴──────┐
                  │ K8s API    │
                  │ (Get SA)   │
                  └────────────┘
```

## License

MIT

## Contributing

Contributions welcome! Please ensure:
- Code follows Go best practices
- Add tests for new functionality
- Update documentation as needed