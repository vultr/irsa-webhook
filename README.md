# IRSA Mutating Admission Webhook for Kubernetes

This webhook implements IAM Roles for Service Accounts (IRSA) for Kubernetes clusters, allowing pods to assume Vultr IAM roles using projected service account tokens.

## Features

- Automatically injects Vultr credentials configuration into pods
- Compatibility with AWS SDK
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
  - Deploy a VKE cluster and do `export KUBECONFIG=~/Downloads/vke-64c243de-eb0b-4084-93ae-6c386bef8978.yaml`
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

## Complete Flow
- Register your cluster's JWKS as an OIDC Issuer at `https://api.vultr.com/v2/oidc/issuer`
  - For VKE clusters:
  ```
  {
    "source": "vke",
    "source_id": "a070d34b-8380-441a-8fb4-d5a9c4001226" #This is the id of your VKE cluster
  }
  ```
  - For NON VKE clusters:
  ```
  {
    "source": "external",
    "uri": "https://64c243de-eb0b-4084-93ae-6c386bef8978.vultr-k8s.com:6443/openid/v1/jwks",
    "kid": "Sf4VzjgTmm_pW91u5qZypZWwiac9_boRFPC5vEmuhCQ",
    "kty": "RSA",
    "n": "3nhZuoDdSSr6OvdnxfOiJKZoC3kcnuEqbJyxXx0ULZLld3rxOmY8w1cuVjNIOaQsZZzQ6qeR7Z315L-Cdi19SLJRcdPf4d0Nezj9pmE_C0VjyNa8w0ZeF23xgiSnE4-ZamLdPtmxWXGhyyBSc_3CRBo-yFdAYJrsmXT1jjm_DOFpI3ZnKqeK7zmG9pRK-OaXfIXw_PEAZ3scflUkv1tE_j21YnFYd8BSM_He_V4Wx3MRFEBqr9-NbVegsEaQsZU63G_BCxEQXHXXM1YJ9ubE29jvMUrSHNFrgLrAjQhXrwu-PpEU1ROwbG4G0FaWkxEzC2K2_gqVC-Q4g-eEYS73UQ",
    "e": "AQAB",
    "alg": "RS256",
    "use": "sig"
  }
  ```
- From this point you will be able to auth to the vultr API from inside your kubernetes cluster using the standard - See the file in this repo `test-oidc-issuer.yaml`
- Deploy this irsa-webhook to your cluster
- Pod->STS
  - Now when when a pod is owned by a serviceAccount with the annotation `vultr.com/role-arn`, the pod will send a token issued by the cluster to the Vultr sts endpoint.
- STS->Pod
  - Vultr's STS endpoint will respond with tokens issued by Vultr that are injected into the pod for the application running in the pod to consume


## The Full Flow with Both Tokens
```
┌──────────────────────────────────────────────────────────────────────────────┐
│ YOUR CLUSTER                                                                │
│                                                                             │
│ Kubernetes API Server (configured with your issuer)                         │
│ ├─ Generates TOKEN #1 (ServiceAccount JWT)                                  │
│ │  Signed with: cluster's private key                                       │
│ │  Claims:                                                                  │
│ │    iss: "https://api.vultr.com/v2/oidc"                                   │
│ │    aud: "vultr"                                                           │
│ │    sub: "system:serviceaccount:default:test-sa"                           │
│ └─ Mounts TOKEN #1 in pod at:                                               │
│    /var/run/secrets/kubernetes.io/serviceaccount/token                      │
│                                                                             │
│ ┌────────────────────────────────────────────────────────────────────────┐  │
│ │ Pod: my-app                                                            │  │
│ │                                                                        │  │
│ │ 1. Application starts                                                  │  │
│ │ 2. SDK reads TOKEN #1 from file                                        │  │
│ │ 3. SDK calls Vultr STS with TOKEN #1                                   │  │
│ └────────────────────────────────────────────────────────────────────────┘  │
└──────────────────────────────────────────────────────────────────────────────┘

                             │
TOKEN #1 (K8s JWT) sent to Vultr platform ────────────────────────────────────┘
                             ↓

┌──────────────────────────────────────────────────────────────────────────────┐
│ VULTR PLATFORM (api.vultr.com)                                              │
│                                                                             │
│ STS Service                                                                 │
│ ├─ Receives TOKEN #1 from pod                                               │
│ ├─ Validates TOKEN #1:                                                      │
│ │  └─ Fetches public key from /v2/oidc/jwks                                 │
│ │  └─ Verifies signature                                                    │
│ │  └─ Checks issuer, audience, expiration                                   │
│ │  └─ Checks role trust policy                                              │
│ ├─ Generates TOKEN #2 (Temporary Credentials)                               │
│ │  └─ AccessKeyId: VKAEXAMPLE123ABC                                         │
│ │  └─ SecretAccessKey: secretKEY789XYZ                                      │
│ │  └─ SessionToken: sessionTOKEN456DEF                                      │
│ └─ Returns TOKEN #2 to pod                                                  │
└──────────────────────────────────────────────────────────────────────────────┘

                             │
TOKEN #2 (Temporary credentials) sent back to pod ────────────────────────────┘
                             ↓

┌──────────────────────────────────────────────────────────────────────────────┐
│ YOUR CLUSTER                                                                │
│                                                                             │
│ ┌────────────────────────────────────────────────────────────────────────┐ │
│ │ Pod: my-app                                                            │  │
│ │                                                                        │  │
│ │ 4. SDK receives TOKEN #2 (credentials)                                 │  │
│ │ 5. SDK caches TOKEN #2                                                 │  │
│ │ 6. SDK uses TOKEN #2 for all API calls:                                │  │
│ │    - List buckets                                                      │  │
│ │    - Upload objects                                                    │  │
│ │    - etc.                                                              │  │
│ └────────────────────────────────────────────────────────────────────────┘ │
└──────────────────────────────────────────────────────────────────────────────┘

                             │
All API calls use TOKEN #2 (credentials) ────────────────────────────────────┘
                             ↓

┌──────────────────────────────────────────────────────────────────────────────┐
│ VULTR PLATFORM APIs (api.vultr.com/v2/*)                                    │
│                                                                             │
│ Object Storage API, Compute API, etc.                                       │
│ ├─ Receives request with TOKEN #2 (SessionToken)                            │
│ ├─ Validates TOKEN #2 against session database                              │
│ ├─ Checks permissions from role                                             │
│ └─ Executes API operation                                                   │
└──────────────────────────────────────────────────────────────────────────────┘

```
