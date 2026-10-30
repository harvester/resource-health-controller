# harvester-resource-health-controller

A standalone Harvester add-on that reports the health of Harvester resources.

It imports the CRDs and generated controllers of `harvester/harvester` (and, later, other repositories),
registers `OnChange` handlers for the resources to be checked, and surfaces the result in two CRDs:

- `ComponentHealth` (`health.harvesterhci.io/v1beta1`, cluster scoped): one CR per resource kind
  (e.g. `harvester-controller-node`, `harvester-controller-vm`). `status.checks` is a map keyed by check reason.
- `HealthSummary` (`health.harvesterhci.io/v1beta1`, cluster scoped): a single CR named `cluster` that holds the
  error / warning counts per component. It is maintained by the summary controller.

See the HEP `enhancements/20260915-resource-health-dashboard.md` in `harvester/harvester` for the design.

## How it works

1. Each watched resource `OnChange` handler only enqueues the key of the `ComponentHealth` CR that owns that resource kind.
2. The workqueue de-duplicates the keys, so a burst of events collapses into a single reconcile.
3. `OnComponentHealthChanged(key)` dispatches by key through the ordered `componentReconcilers` registry to
   `reconcileXxx()`, which recomputes the checks from the informer cache and writes them to the `ComponentHealth` CR
   only when they changed. The same registry supplies the keys for startup and full-health enqueues, so adding a
   component requires only one registry entry for both dispatch and enqueue.
4. The summary controller watches every `ComponentHealth`, groups them by the `health.harvesterhci.io/component`
   label and upserts `HealthSummary/cluster`.

5. Ignore rules (see below) are applied on the merged checks right before they are written, so the
   ignore marks are always derived from the current checks and rules, never stored by anyone else.

Current checks:

| ComponentHealth | Watched resource | Reasons |
| --- | --- | --- |
| `harvester-controller-node` | `Node` | `NodeCordoned` |
| `harvester-controller-vm` | `VirtualMachineInstance` | `VMHostDeviceLiveMigrationUnsupported`, `VMNodeSelectorLiveMigrationUnsupported` |
| `harvester-controller-volume` | Longhorn `Volume` | condition reasons of false conditions |
| `harvester-controller-vmbackup` | `VirtualMachineBackup` | `VMBackupFailed` |
| `harvester-controller-schedulevmbackup` | `ScheduleVMBackup` | condition reasons |
| `harvester-controller-vmimage` | `VirtualMachineImage` | `Image*` reasons |
| `harvester-controller-upgrade` | `Upgrade` | `Upgrade*` reasons, one per false condition |
| `harvester-controller-addon` | `Addon` | `AddonOperationFailed` while the last enable, disable or update operation failed |
| `harvester-controller-supportbundle` | `SupportBundle` | `SupportBundleFailed` while the bundle is in the error state; `SupportBundleGenerating` (Info) while it is generating |
| `harvester-controller-upgradelog` | `UpgradeLog` | `UpgradeLog*` warnings, one per false logging condition |
| `harvester-controller-volumeremotebackup` | `VolumeRemoteBackup` | `VolumeRemoteBackupFailed` while the Ready condition is false for a reason other than the operation being in progress |
| `harvester-controller-volumeremoterestore` | `VolumeRemoteRestore` | `VolumeRemoteRestoreFailed` while the Ready condition is false for a reason other than the operation being in progress |

## Ignoring checks

A check can be ignored with a cluster scoped `HealthIgnoreRule`. The check stays in the `ComponentHealth` status
(marked `ignored: true`, or `ignored: true` on the single affected resource), so it can be reverted and audited, but it
no longer counts in the `HealthSummary`. The dashboard hides the ignored checks by default.

```yaml
apiVersion: health.harvesterhci.io/v1beta1
kind: HealthIgnoreRule
metadata:
  name: ignore-volume-pvc-aaa
  labels:
    health.harvesterhci.io/source: user
spec:
  componentHealth: harvester-controller-volume   # name of the ComponentHealth
  checkKey: ReplicaSchedulingFailure             # key in status.checks
  resources: [pvc-aaa]    # optional, names in status.checks[].affectedResources.names; whole check when empty
  expiresAt: "2026-11-18T10:00:00Z"              # optional
  reason: "test volume, expected"                # optional
```

- Only the controller writes `ComponentHealth.status`; the dashboard only creates or deletes rules.
- A rule is matched with `spec.componentHealth`, labels are not used by the controller.
- A rule change reconciles every `ComponentHealth`, and an expiring rule schedules a reconcile at `expiresAt`.
## Develop

The build system is a single `Dockerfile` driven by `make` (no Dapper). Docker with buildx is the only requirement.

```sh
make ci              # validate (golangci-lint) + build + test, all inside the builder image
make build           # binaries into ./bin
make test
make validate
make generate        # deepcopy / clients / CRD manifests / bindata, inside the builder image
make generate-manifest   # CRD manifests into ./chart/crds
make package         # container image from ./bin

# alternatively, regenerate locally (needs Go and controller-gen in PATH)
go generate ./...

# run against the cluster in your kubeconfig
make run-controller ARGS="--no-leader-election --debug"
```

`scripts/version` reads git to derive the version. In a checkout without any commit, hand-write `scripts/.version_env`
(see `scripts/version` for the fields) before running `make`.

## Deploy

The Helm chart lives in `chart/` and runs the controller as a `Deployment`.

```sh
helm install harvester-resource-health-controller ./chart -n harvester-system \
  --set image.repository=<repo>/harvester-resource-health-controller --set image.tag=<tag>
kubectl get componenthealths
kubectl get healthsummary cluster -o yaml
```
