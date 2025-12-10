# Troubleshooting Guide

## Common Issues and Solutions

### 1. Webhook Not Responding

**Symptoms:**
- Pods fail to create with timeout errors
- Events show webhook timeout
- `kubectl get pods` hangs

**Diagnosis:**
```bash
# Check webhook pod status
kubectl get pods -n irsa-system

# View webhook logs
kubectl logs -n irsa-system -l app=irsa-webhook

# Check webhook service
kubectl get svc -n irsa-system irsa-webhook
kubectl get endpoints -n irsa-system irsa-webhook
```

**Solutions:**

1. **Webhook pods not running:**
   ```bash
   kubectl describe pods -n irsa-system -l app=irsa-webhook
   # Fix image pull issues, resource constraints, etc.
   ```

2. **TLS certificate issues:**
   ```bash
   # Regenerate certificates
   ./generate-certs.sh
   kubectl rollout restart deployment -n irsa-system irsa-webhook
   ```

3. **Service not routing correctly:**
   ```bash
   # Check if service selectors match pod labels
   kubectl get svc -n irsa-system irsa-webhook -o yaml
   kubectl get pods -n irsa-system -l app=irsa-webhook --show-labels
   ```

### 2. Pods Not Being Mutated

**Symptoms:**
- Pods create successfully but don't have injected configuration
- Environment variables missing
- Volume not mounted

**Diagnosis:**
```bash
# Check if ServiceAccount has annotation
kubectl get sa <service-account-name> -o yaml | grep vultr.com/role-arn

# Check webhook configuration
kubectl get mutatingwebhookconfiguration irsa-webhook -o yaml

# View webhook logs for the specific pod creation
kubectl logs -n irsa-system -l app=irsa-webhook --tail=100
```

**Solutions:**

1. **ServiceAccount annotation missing:**
   ```bash
   kubectl annotate sa <service-account-name> \
     vultr.com/role-arn="arn:aws:iam::123456789012:role/your-role"
   ```

2. **Namespace excluded from webhook:**
   Check the `namespaceSelector` in the MutatingWebhookConfiguration:
   ```bash
   kubectl get mutatingwebhookconfiguration irsa-webhook -o yaml
   ```

3. **Webhook not receiving requests:**
   ```bash
   # Check webhook logs for incoming requests
   kubectl logs -n irsa-system -l app=irsa-webhook --tail=50
   
   # Verify webhook configuration matches service
   kubectl get mutatingwebhookconfiguration irsa-webhook -o jsonpath='{.webhooks[0].clientConfig}'
   ```

### 3. RBAC Permission Errors

**Symptoms:**
- Webhook logs show "forbidden" or "unauthorized" errors
- Error fetching ServiceAccounts

**Diagnosis:**
```bash
# Check webhook ServiceAccount permissions
kubectl auth can-i get serviceaccounts \
  --as=system:serviceaccount:irsa-system:irsa-webhook \
  --all-namespaces

# View RBAC resources
kubectl get clusterrole irsa-webhook -o yaml
kubectl get clusterrolebinding irsa-webhook -o yaml
```

**Solutions:**

1. **Missing RBAC permissions:**
   ```bash
   # Reapply RBAC configuration
   kubectl apply -f deploy.yaml
   ```

2. **ServiceAccount not bound to role:**
   ```bash
   kubectl get clusterrolebinding irsa-webhook -o yaml
   # Verify subjects include the correct ServiceAccount
   ```

### 4. TLS/Certificate Issues

**Symptoms:**
- "x509: certificate signed by unknown authority"
- "TLS handshake error"
- Webhook returns 401 or 403

**Diagnosis:**
```bash
# Check certificate in secret
kubectl get secret -n irsa-system irsa-webhook-certs -o yaml

# Verify CA bundle in webhook config
kubectl get mutatingwebhookconfiguration irsa-webhook \
  -o jsonpath='{.webhooks[0].clientConfig.caBundle}' | base64 -d
```

**Solutions:**

1. **Regenerate certificates:**
   ```bash
   ./generate-certs.sh
   ```

2. **Manually update CA bundle:**
   ```bash
   CA_BUNDLE=$(kubectl get secret -n irsa-system irsa-webhook-certs \
     -o jsonpath='{.data.ca\.crt}')
   
   kubectl patch mutatingwebhookconfiguration irsa-webhook \
     --type='json' \
     -p="[{'op': 'replace', 'path': '/webhooks/0/clientConfig/caBundle', 'value':'${CA_BUNDLE}'}]"
   ```

3. **Verify certificate SANs:**
   ```bash
   kubectl get secret -n irsa-system irsa-webhook-certs \
     -o jsonpath='{.data.tls\.crt}' | base64 -d | openssl x509 -text -noout
   ```

### 5. AWS Credential Issues

**Symptoms:**
- Pods can't authenticate with AWS
- "Unable to locate credentials" error
- "InvalidIdentityToken" error from AWS STS

**Diagnosis:**
```bash
# Check injected environment variables
kubectl exec <pod-name> -- env | grep AWS

# Verify token file exists
kubectl exec <pod-name> -- ls -la /var/run/secrets/vultr.com/serviceaccount/

# Check token contents (first 50 chars)
kubectl exec <pod-name> -- head -c 50 /var/run/secrets/vultr.com/serviceaccount/token

# Test AWS STS
kubectl exec <pod-name> -- aws sts get-caller-identity
```

**Solutions:**

1. **Token not mounted:**
   - Verify pod has the volume and volume mount
   - Check webhook logs for mutation
   - Delete and recreate the pod

2. **IAM role trust policy issue:**
   Ensure your IAM role has the correct trust policy:
   ```json
   {
     "Version": "2012-10-17",
     "Statement": [
       {
         "Effect": "Allow",
         "Principal": {
           "Federated": "arn:aws:iam::YOUR_ACCOUNT_ID:oidc-provider/YOUR_OIDC_PROVIDER"
         },
         "Action": "sts:AssumeRoleWithWebIdentity",
         "Condition": {
           "StringEquals": {
             "YOUR_OIDC_PROVIDER:aud": "vultr"
           }
         }
       }
     ]
   }
   ```

3. **Wrong audience in token:**
   - Verify the projected token has audience "vultr"
   - Check webhook configuration uses correct tokenAudience constant

### 6. Performance Issues

**Symptoms:**
- Pod creation is slow
- Webhook timeout warnings
- High resource usage

**Diagnosis:**
```bash
# Check webhook resource usage
kubectl top pods -n irsa-system

# View webhook latency in logs
kubectl logs -n irsa-system -l app=irsa-webhook | grep "Processing pod"

# Check for throttling
kubectl describe pods -n irsa-system -l app=irsa-webhook
```

**Solutions:**

1. **Increase webhook timeout:**
   ```bash
   kubectl patch mutatingwebhookconfiguration irsa-webhook \
     --type='json' \
     -p='[{"op": "replace", "path": "/webhooks/0/timeoutSeconds", "value": 30}]'
   ```

2. **Scale webhook deployment:**
   ```bash
   kubectl scale deployment -n irsa-system irsa-webhook --replicas=3
   ```

3. **Increase resource limits:**
   Edit deploy.yaml and increase CPU/memory limits:
   ```yaml
   resources:
     requests:
       cpu: 200m
       memory: 256Mi
     limits:
       cpu: 1000m
       memory: 512Mi
   ```

### 7. JSON Patch Generation Errors

**Symptoms:**
- "Failed to generate patches" in webhook logs
- Malformed patch errors
- Array index out of bounds

**Diagnosis:**
```bash
# Enable verbose logging (add to deployment)
# Set LOG_LEVEL=debug in environment

# Check specific pod that failed
kubectl logs -n irsa-system -l app=irsa-webhook --tail=100 | grep -A 10 "Failed"
```

**Solutions:**

1. **Review pod specification:**
   - Ensure pod spec is valid JSON
   - Check for unusual container configurations

2. **Update webhook logic:**
   - Fix any bugs in generatePatches function
   - Add error handling for edge cases

### 8. Multiple Webhooks Conflict

**Symptoms:**
- Pod mutations from other webhooks interfering
- Unexpected pod configuration
- Volume/env var conflicts

**Diagnosis:**
```bash
# List all mutating webhooks
kubectl get mutatingwebhookconfigurations

# Check webhook order
kubectl get mutatingwebhookconfigurations -o yaml | grep -A 5 "name:"
```

**Solutions:**

1. **Adjust webhook order:**
   Webhooks are processed alphabetically by name. Rename if needed:
   ```bash
   # Add a prefix to control order
   kubectl patch mutatingwebhookconfiguration irsa-webhook \
     --type='json' \
     -p='[{"op": "replace", "path": "/metadata/name", "value": "01-irsa-webhook"}]'
   ```

2. **Add reinvocationPolicy:**
   ```yaml
   webhooks:
   - name: irsa.vultr.com
     reinvocationPolicy: IfNeeded  # or Never
   ```

## Debug Commands Cheat Sheet

```bash
# View all webhook-related resources
kubectl get all -n irsa-system
kubectl get mutatingwebhookconfiguration irsa-webhook
kubectl get clusterrole irsa-webhook
kubectl get clusterrolebinding irsa-webhook

# Test webhook directly
kubectl run test-pod --image=nginx --dry-run=client -o yaml | \
  kubectl create -f - --namespace=default

# Watch webhook logs in real-time
kubectl logs -n irsa-system -l app=irsa-webhook -f

# Check webhook pod health
kubectl get pods -n irsa-system -l app=irsa-webhook -o wide
kubectl describe pods -n irsa-system -l app=irsa-webhook

# View recent events
kubectl get events -n irsa-system --sort-by='.lastTimestamp'

# Test ServiceAccount annotation
kubectl get sa -A -o jsonpath='{range .items[?(@.metadata.annotations.vultr\.com/role-arn)]}{.metadata.namespace}{" "}{.metadata.name}{" "}{.metadata.annotations.vultr\.com/role-arn}{"\n"}{end}'

# Validate webhook configuration
kubectl get mutatingwebhookconfiguration irsa-webhook -o yaml | grep -E "(caBundle|service|path|port)"
```

## Getting Help

If you're still experiencing issues:

1. **Collect diagnostic information:**
   ```bash
   # Run this script and save output
   kubectl get all -n irsa-system > diagnostics.txt
   kubectl logs -n irsa-system -l app=irsa-webhook --tail=200 >> diagnostics.txt
   kubectl get mutatingwebhookconfiguration irsa-webhook -o yaml >> diagnostics.txt
   kubectl get events -n irsa-system >> diagnostics.txt
   ```

2. **Check webhook version:**
   ```bash
   kubectl get deployment -n irsa-system irsa-webhook -o jsonpath='{.spec.template.spec.containers[0].image}'
   ```

3. **Review logs with timestamps:**
   ```bash
   kubectl logs -n irsa-system -l app=irsa-webhook --timestamps=true --tail=100
   ```

4. **Test in isolation:**
   - Create a separate test namespace
   - Deploy a simple test pod
   - Monitor webhook behavior

## Prevention

**Best Practices to Avoid Issues:**

1. Always test in a non-production cluster first
2. Set `failurePolicy: Ignore` during initial deployment
3. Monitor webhook performance and logs
4. Keep certificates up to date (rotate every 365 days)
5. Use resource limits to prevent webhook from consuming too much
6. Implement readiness and liveness probes
7. Scale webhook deployment for high-traffic clusters
8. Document all ServiceAccount annotations
