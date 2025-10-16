# cert-trust

Synchronize Kubernetes TLS certificates between namespaces using two CRDs: `CertificateExport` and `CertificateImport`. A lightweight controller periodically copies TLS secrets based on cron-like schedules.

## Concepts
- `CertificateExport` (source namespace): points to a TLS secret (`kubernetes.io/tls`).
- `CertificateImport` (target namespace): references a `CertificateExport` (same namespace or `ns/name`) and writes/updates a target TLS secret.
- Each resource supports an optional cron schedule. Default: `@every 1h`.

## Quick Start

### Build and Deploy
```bash
# Build the controller binary
make build

# Build Docker image
make docker-build TAG=0.1.0

# Push to registry (replace with your registry)
docker tag nazman/cert-trust:0.1.0 YOUR_REGISTRY/cert-trust:0.1.0
docker push YOUR_REGISTRY/cert-trust:0.1.0

# Install with Helm
make helm-install IMAGE=YOUR_REGISTRY/cert-trust TAG=0.1.0
```

### Available Make Commands
```bash
make help                    # Show all available commands
make build                   # Build controller binary
make docker-build TAG=0.1.0 # Build Docker image
make docker-push TAG=0.1.0  # Push Docker image
make helm-install           # Install/upgrade Helm chart
make helm-uninstall         # Uninstall Helm chart
make print                  # Show current variables
```

## Installation

### Using Make (Recommended)
```bash
# Set your image registry and tag
export IMAGE=ghcr.io/your-org/cert-trust
export TAG=0.1.0

# Build and deploy
make docker-build TAG=$TAG
make docker-push TAG=$TAG
make helm-install IMAGE=$IMAGE TAG=$TAG
```

### Manual Helm Install
```bash
helm install cert-trust ./charts/cert-trust -n cert-trust --create-namespace
```

This installs:
- CRDs for `CertificateExport` and `CertificateImport`
- Deployment, RBAC, and ServiceAccount

Values of interest in `values.yaml`:
- `image.repository`, `image.tag`
- `leaderElection`

## Usage Examples

### Example 1: Basic Certificate Sync
1) Create a TLS secret in the source namespace:
```bash
kubectl create secret tls myapp-tls \
  --cert=path/to/cert.pem \
  --key=path/to/key.pem \
  -n backend
```

2) Create a CertificateExport:
```yaml
apiVersion: cert.trust.flolive.io/v1
kind: CertificateExport
metadata:
  name: export-myapp-cert
  namespace: backend
spec:
  secretRef: myapp-tls
  schedule: "0 */6 * * *" # every 6 hours (optional)
```

3) Create a CertificateImport in target namespace:
```yaml
apiVersion: cert.trust.flolive.io/v1
kind: CertificateImport
metadata:
  name: import-myapp-cert
  namespace: frontend
spec:
  fromExport: backend/export-myapp-cert
  targetSecret: myapp-tls
  schedule: "@every 2h" # optional
```

### Example 2: Cross-Namespace Certificate Sharing
```yaml
# In gateway namespace - export the certificate
apiVersion: cert.trust.flolive.io/v1
kind: CertificateExport
metadata:
  name: export-wildcard-cert
  namespace: gateway
spec:
  secretRef: wildcard-tls
  schedule: "*/30 * * * *" # every 30 minutes
---
# In api namespace - import the certificate
apiVersion: cert.trust.flolive.io/v1
kind: CertificateImport
metadata:
  name: import-wildcard-cert
  namespace: api
spec:
  fromExport: gateway/export-wildcard-cert
  targetSecret: wildcard-tls
---
# In web namespace - import the same certificate
apiVersion: cert.trust.flolive.io/v1
kind: CertificateImport
metadata:
  name: import-wildcard-cert
  namespace: web
spec:
  fromExport: gateway/export-wildcard-cert
  targetSecret: wildcard-tls
```

### Example 3: Same Namespace Reference
```yaml
# Both resources in the same namespace
apiVersion: cert.trust.flolive.io/v1
kind: CertificateExport
metadata:
  name: export-main-cert
  namespace: production
spec:
  secretRef: main-tls
---
apiVersion: cert.trust.flolive.io/v1
kind: CertificateImport
metadata:
  name: import-main-cert
  namespace: production
spec:
  fromExport: export-main-cert  # No namespace prefix needed
  targetSecret: main-tls-copy
```

## Monitoring

### Check Controller Status
```bash
# Check if controller is running
kubectl get pods -n cert-trust

# Check controller logs
kubectl logs -n cert-trust deployment/cert-trust-cert-trust

# Check CRD resources
kubectl get certificateexport -A
kubectl get certificateimport -A
```

### Check Sync Status
```bash
# Check last sync time
kubectl get certificateexport export-myapp-cert -n backend -o yaml
kubectl get certificateimport import-myapp-cert -n frontend -o yaml

# Check if target secret was created
kubectl get secret myapp-tls -n frontend
```

## Development

### Local Development
```bash
# Build and run locally
make build
./bin/manager --leader-elect=false

# Run with debug logging
go run ./cmd/cert-trust --leader-elect=false
```

### Testing
```bash
# Apply test resources
kubectl apply -f test/mainfest.yaml

# Check if sync worked
kubectl get secret srvx-cc-tls-default -n default
```

## Configuration

### Cron Schedule Examples
- `"*/2 * * * *"` - Every 2 minutes
- `"0 */6 * * *"` - Every 6 hours
- `"@every 1h"` - Every hour
- `"0 0 * * *"` - Daily at midnight
- `"0 0 * * 0"` - Weekly on Sunday

### Helm Values
```yaml
# charts/cert-trust/values.yaml
replicaCount: 1
image:
  repository: ghcr.io/your-org/cert-trust
  tag: "0.1.0"
  pullPolicy: IfNotPresent
leaderElection: false
resources: {}
```

## Troubleshooting

### Common Issues
1. **No sync happening**: Check controller logs for errors
2. **Permission denied**: Verify RBAC permissions
3. **Secret not found**: Ensure source secret exists and is type `kubernetes.io/tls`
4. **Wrong namespace**: Check `fromExport` reference format

### Debug Commands
```bash
# Check RBAC permissions
kubectl auth can-i list certificateexports --as=system:serviceaccount:cert-trust:cert-trust-cert-trust

# Check controller logs
kubectl logs -n cert-trust deployment/cert-trust-cert-trust --tail=50

# Check resource status
kubectl describe certificateexport -n backend export-myapp-cert
kubectl describe certificateimport -n frontend import-myapp-cert
```

## CI/CD

### GitHub Actions
The project includes GitHub Actions workflows for:

- **CI Pipeline** (`.github/workflows/ci.yml`):
  - Runs on push to main/master/develop branches and pull requests
  - Builds and tests the Go code
  - Lints and tests the Helm chart
  - Builds Docker image for testing

- **Release Pipeline** (`.github/workflows/release.yml`):
  - Triggers when pushing a tag (e.g., `v0.1.0`)
  - Builds and pushes Docker image to GitHub Container Registry
  - Packages and releases Helm chart
  - Creates GitHub release with chart artifacts

### Creating a Release
```bash
# Create and push a tag
git tag v0.1.0
git push origin v0.1.0

# The GitHub Action will automatically:
# 1. Build and push Docker image to ghcr.io/your-org/cert-trust:v0.1.0
# 2. Package Helm chart
# 3. Create GitHub release with chart artifacts
```

### Installing from GitHub Releases
```bash
# Add the repository using GitHub releases
helm repo add cert-trust https://github.com/your-org/cert-trust/releases/download/v0.1.0
helm repo update

# Install the chart
helm install cert-trust cert-trust/cert-trust -n cert-trust --create-namespace
```

## Security Notes
- RBAC grants read on secrets cluster-wide and write in target namespaces for imports
- Restrict installation namespace and permissions as needed
- Source secrets must be of type `kubernetes.io/tls`
- Status fields `status.lastSyncTime` are updated on best-effort basis
