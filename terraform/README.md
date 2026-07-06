# Terraform deployment

This stack mirrors the charm integration test deployment:

- local `charm-registry` charm with an `app-image` OCI resource
- PostgreSQL (`postgresql-k8s`)
- Gateway API ingress (`gateway-api-integrator`)
- two `ingress-configurator` applications, one for each ingress relation
- `self-signed-certificates` on channel `1/stable`

The deployment mode is **provider-first**: Terraform manages the model,
Charmhub applications, resources, and configuration through the Juju provider.
Relations are managed with the provider-native `juju_integration` resource. The
`charm-registry` charm is deployed from Charmhub.

## Prerequisites

1. A bootstrapped Juju controller on Canonical Kubernetes.
2. Canonical Kubernetes load balancer enabled:
   `sudo k8s get load-balancer.cidrs`
3. Accepted GatewayClass, usually `ck-gateway`:
   `kubectl get gatewayclass`
4. The `charm-registry` charm published to Charmhub.
5. Optional: an `app-image` OCI override reachable from the Kubernetes cluster.

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

If the Juju CLI needs a controller-qualified model, use the
`-m <controller>:<model>` form in the debug commands instead.
