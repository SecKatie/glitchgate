# Kustomize Manifests for glitchgate

This directory contains Kubernetes manifests for deploying glitchgate.

## Structure

```
kustomize/
├── base/
│   ├── kustomization.yaml   # Base Kustomization
│   ├── deployment.yaml      # glitchgate Deployment
│   ├── service.yaml         # glitchgate Service
│   ├── configmap.yaml       # ConfigMap with non-sensitive config
│   ├── secrets.yaml         # Secret template (fill in values)
│   └── postgres.yaml        # PostgreSQL StatefulSet + Service
└── overlays/
    └── production/
        ├── kustomization.yaml  # Production overlay
        └── secrets.env          # Production secrets (DO NOT COMMIT)
```

## Usage

### Base Installation

For a simple single-instance deployment with SQLite (no postgres):

```bash
# Edit base/secrets.yaml with your values first
kubectl apply -k ./kustomize/base
```

### Production with PostgreSQL

The base configuration includes PostgreSQL by default. To deploy:

1. Copy and fill in the secrets:
   ```bash
   cp overlays/production/secrets.env.example overlays/production/secrets.env
   # Edit secrets.env with your actual values
   ```

2. Apply the production overlay:
   ```bash
   kubectl apply -k ./kustomize/overlays/production
   ```

### Without PostgreSQL (SQLite)

If you want SQLite instead of PostgreSQL, modify the configmap:

```yaml
# Remove GLITCHGATE_DATABASE_URL and use:
GLITCHGATE_DATABASE_PATH: "/data/glitchgate.db"
```

And remove `postgres.yaml` from the base kustomization.

## Health Checks

The deployment includes `/health` endpoint probes on port 4000.

## Storage

- **PostgreSQL**: Uses a PersistentVolumeClaim (1Gi by default)
- **glitchgate data**: Uses emptyDir volume (switch to PVC for persistence)

## Ingress Example

Add an Ingress for external access:

```yaml
apiVersion: networking.k8s.io/v1
kind: Ingress
metadata:
  name: glitchgate
  annotations:
    nginx.ingress.kubernetes.io/proxy-body-size: "50m"
spec:
  rules:
    - host: glitchgate.example.com
      http:
        paths:
          - path: /
            pathType: Prefix
            backend:
              service:
                name: glitchgate
                port:
                  number: 80
```
