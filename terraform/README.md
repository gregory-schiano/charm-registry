# Terraform deployment

This stack mirrors the charm integration test deployment:

- local `charm-registry` charm with an `app-image` OCI resource
- PostgreSQL (`postgresql-k8s`)
- Gateway API ingress (`gateway-api-integrator`)
- two `ingress-configurator` applications, one for each ingress relation
- `self-signed-certificates` on channel `1/stable`

The deployment mode is **hybrid local artifact**: Terraform manages the model,
Charmhub applications, and relations through the Juju provider, while a small
Terraform-owned Juju CLI bridge deploys or refreshes the unpublished local
`.charm` artifact.

## Prerequisites

1. A bootstrapped Juju controller on Canonical Kubernetes.
2. Canonical Kubernetes load balancer enabled:
   `sudo k8s get load-balancer.cidrs`
3. Accepted GatewayClass, usually `ck-gateway`:
   `kubectl get gatewayclass`
4. A built `.charm` stored somewhere the Juju CLI can read.
5. An `app-image` OCI reference reachable from the Kubernetes cluster.

## Usage

```sh
cd terraform
cp terraform.tfvars.example terraform.tfvars
$EDITOR terraform.tfvars
terraform init
terraform plan
terraform apply
```

For an existing model, set:

```hcl
create_model = false
model_name   = "your-model"
```

If the Juju CLI needs a controller-qualified model for the local charm bridge,
also set:

```hcl
juju_model_cli = "your-controller:your-model"
```
