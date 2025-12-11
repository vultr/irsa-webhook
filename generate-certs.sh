#!/bin/bash

# Generate TLS certificates for the webhook
# This creates a self-signed CA and certificate for the webhook service

set -e

NAMESPACE="irsa-system"
SERVICE_NAME="irsa-webhook"
SECRET_NAME="irsa-webhook-certs"
WEBHOOK_CONFIG_NAME="irsa-webhook"

# Create temporary directory for certificate generation
CERT_DIR=$(mktemp -d)
trap "rm -rf ${CERT_DIR}" EXIT

echo "Generating certificates in ${CERT_DIR}..."

# Generate CA private key
openssl genrsa -out ${CERT_DIR}/ca.key 2048

# Generate CA certificate
openssl req -x509 -new -nodes -key ${CERT_DIR}/ca.key \
    -subj "/CN=IRSA Webhook CA" \
    -days 3650 \
    -out ${CERT_DIR}/ca.crt

# Generate webhook private key
openssl genrsa -out ${CERT_DIR}/tls.key 2048

# Create certificate signing request
cat > ${CERT_DIR}/csr.conf <<EOL
[req]
req_extensions = v3_req
distinguished_name = req_distinguished_name
[req_distinguished_name]
[v3_req]
basicConstraints = CA:FALSE
keyUsage = nonRepudiation, digitalSignature, keyEncipherment
extendedKeyUsage = serverAuth
subjectAltName = @alt_names
[alt_names]
DNS.1 = ${SERVICE_NAME}
DNS.2 = ${SERVICE_NAME}.${NAMESPACE}
DNS.3 = ${SERVICE_NAME}.${NAMESPACE}.svc
DNS.4 = ${SERVICE_NAME}.${NAMESPACE}.svc.cluster.local
EOL

# Generate certificate signing request
openssl req -new -key ${CERT_DIR}/tls.key \
    -subj "/CN=${SERVICE_NAME}.${NAMESPACE}.svc" \
    -out ${CERT_DIR}/tls.csr \
    -config ${CERT_DIR}/csr.conf

# Sign the certificate with the CA
openssl x509 -req -in ${CERT_DIR}/tls.csr \
    -CA ${CERT_DIR}/ca.crt \
    -CAkey ${CERT_DIR}/ca.key \
    -CAcreateserial \
    -out ${CERT_DIR}/tls.crt \
    -days 3650 \
    -extensions v3_req \
    -extfile ${CERT_DIR}/csr.conf

echo "Certificates generated successfully."

# Create namespace if it doesn't exist
kubectl create namespace ${NAMESPACE} --dry-run=client -o yaml | kubectl apply -f -

# Create or update secret with certificates
kubectl create secret tls ${SECRET_NAME} \
    --cert=${CERT_DIR}/tls.crt \
    --key=${CERT_DIR}/tls.key \
    --namespace=${NAMESPACE} \
    --dry-run=client -o yaml | kubectl apply -f -

echo "Secret ${SECRET_NAME} created/updated in namespace ${NAMESPACE}"

# Get CA bundle for webhook configuration
CA_BUNDLE=$(cat ${CERT_DIR}/ca.crt | base64 | tr -d '\n')

# Update MutatingWebhookConfiguration with CA bundle
if kubectl get mutatingwebhookconfiguration ${WEBHOOK_CONFIG_NAME} &> /dev/null; then
    kubectl patch mutatingwebhookconfiguration ${WEBHOOK_CONFIG_NAME} \
        --type='json' \
        -p="[{'op': 'replace', 'path': '/webhooks/0/clientConfig/caBundle', 'value':'${CA_BUNDLE}'}]"
    echo "MutatingWebhookConfiguration ${WEBHOOK_CONFIG_NAME} updated with CA bundle"
else
    echo "MutatingWebhookConfiguration ${WEBHOOK_CONFIG_NAME} not found. Please update deploy.yaml with:"
    echo "caBundle: ${CA_BUNDLE}"
fi

echo ""
echo "Setup complete! CA Bundle (for manual configuration):"
echo "${CA_BUNDLE}"
