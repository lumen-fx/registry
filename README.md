# lpm

A package registry and its command-line client. Users register, publish
packages, and publish releases against them; clients search the registry and
fetch release artifacts over HTTPS.

## Layout

```
server/    the registry API and web UI, a single Go binary backed by Postgres
cli/       lpm, the command-line client
k8s/       Kubernetes manifests for the server
```

Each Go module has its own README: [server](server/README.md) covers the API,
configuration, migrations, and tests; [cli](cli/README.md) covers installing
and using `lpm`.

## Releases

`release.yml` builds the server image and pushes it to GHCR on every push to
`main` and on `v*` tags, tagged `sha-<commit>`, `main`, and the version.
`cli-release.yml` cuts cross-compiled `lpm` binaries from the same `v*` tag as
a GitHub release, then publishes the release to the registry itself, which is
what `curl -fsSL https://registry.lumenfx.dev/install.sh | sh` installs. One
tag releases everything.

## Deploying

The image carries the server and the migrator, so a rollout and its migration
Job always run the same code. Nothing needs building by hand.

`k8s/base` runs Postgres in the cluster as a StatefulSet with a 10Gi volume, one
replica, no replication or failover. One command does the whole thing:

```sh
kubectl apply -k k8s/base
```

The base tracks `ghcr.io/lumen-fx/registry:main` with `imagePullPolicy: Always`,
which is fine for a staging cluster and wrong for anything you need to identify
later. Pin the commit instead:

```sh
cd k8s/base
kustomize edit set image ghcr.io/lumen-fx/registry:sha-1a2b3c4
```

### Production

`k8s/overlays/prod` replaces the Ingress with Cloudflare tunnel connectors and
adds a daily update CronJob: it reruns the migrator from the newest `main`
image, then restarts the rollout so every pod re-pulls. A new image on GHCR is
in production within a day of landing; when nothing new was pushed, the cycle
is a no-op.

The tunnel is token-managed, so its public hostname and the route to the
`lpm-server` Service are configured in the Cloudflare dashboard. The token is
never committed; create it once, then apply:

```sh
kubectl create namespace lpm
kubectl -n lpm create secret generic cloudflare-tunnel \
  --from-literal=token=<tunnel token>
kubectl apply -k k8s/overlays/prod
```

### Locally

`k8s/overlays/minikube` builds the image straight into the cluster's own daemon,
so no registry is involved:

```sh
minikube start
minikube image build -t lpm-server:dev server/
kubectl apply -k k8s/overlays/minikube
```

minikube needs `br_netfilter` loaded on the host, otherwise bridged pod traffic
skips iptables, kube-proxy's ClusterIP rules never apply, and nothing resolves
DNS:

```sh
sudo modprobe br_netfilter
```

No secret to create and no password to choose. A bootstrap Job generates a
32 character password on the first apply and writes it to the `lpm-postgres`
Secret, which Postgres and the server both read. Nothing sensitive is committed
or typed, and no password reaches a shell history.

The Job checks for the Secret before generating, so every later apply leaves
the existing password alone. Until it finishes, the Postgres and server pods sit
in `CreateContainerConfigError` waiting for the Secret, then start on their own.

To rotate, delete the Secret and rerun the Job. Postgres keeps the old password
in its volume, so also reset the role:

```sh
kubectl -n lpm delete secret lpm-postgres
kubectl -n lpm delete job lpm-bootstrap-secret
kubectl apply -k k8s/base
kubectl -n lpm exec statefulset/postgres -- \
  psql -U lpm -d lpm -c "ALTER ROLE lpm PASSWORD '<the new one>'"
kubectl -n lpm rollout restart deployment/lpm-server
```

What the manifests do:

All paths below are under `k8s/base`.

- **bootstrap-secret.yaml**: generates the password, plus the ServiceAccount
  and Role that let it create exactly one Secret in this namespace.
- **postgres.yaml**: headless Service and a StatefulSet, reading
  `lpm-postgres`. `PGDATA` sits below the mount point so a volume
  arriving with `lost+found` does not stop `initdb`.
- **migration-job.yaml**: waits for Postgres, runs `/migrate`, exits. Name it
  per release so each deploy gets its own Job.
- **deployment.yaml**: two replicas, surge-only rollout. Liveness probes `/`
  and readiness probes `/health`: a database blip should fail readiness, not
  restart every pod.
- **service.yaml**, **ingress.yaml**: ClusterIP and TLS. Replace the host.
- **poddisruptionbudget.yaml**: keeps one replica serving while a node drains.

Before this is a production deployment, it still needs backups of the Postgres
volume, a real image tag instead of `latest`, and `sslmode=require` if the
database ever moves outside the cluster.
