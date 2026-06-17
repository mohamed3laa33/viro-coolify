<!--
DO NOT EDIT README.md manually!
We're using [helm-docs](https://github.com/norwoodj/helm-docs) to render values of the chart.
If you updated values.yaml file make sure to render a new README.md locally before submitting a Pull Request.

If you're using [pre-commit](https://pre-commit.com/) make sure to install the hooks first:
```
pre-commit install
```
REAMDE.md will be updating automatically after that.

Otherwise, you should install helm-docs and manually update README.md. Navigate to repository root and run:
`helm-docs --chart-search-root=charts/common --template-files=README.md.gotmpl`

You may encounter `files were modified by this hook` error after updating README.md.gotmpl file when using pre-commit.
This is intended behaviour. Make sure to run `git add -A` once again to stage changes in the auto-updated REAMDE.md
-->

# Generic Helm Chart

![Version: 0.3.1](https://img.shields.io/badge/Version-0.3.1-informational?style=flat-square) ![Type: application](https://img.shields.io/badge/Type-application-informational?style=flat-square)

## Values

| Key | Type | Default | Description |
|-----|------|---------|-------------|
| autoscaling.enabled | bool | `false` |  |
| autoscaling.maxReplicas | int | `30` | Maximum number of pods |
| autoscaling.minReplicas | int | `1` | Minimum number of pods |
| autoscaling.targetCPUUtilizationPercentage | int | `80` | Target CPU utilization percentage |
| config | object | `{}` |  |
| cronjob.enabled | bool | `false` |  |
| cronjob.jobs | string | `nil` |  |
| deployment.additionalPodAnnotations | object | `{}` |  |
| deployment.additionalPodSpec | object | `{}` | Additional Pod Spec |
| deployment.affinity | object | `{}` | Assign custom affinity rules |
| deployment.annotations | object | `{}` | Define init container ref: https://kubernetes.io/docs/concepts/workloads/pods/init-containers/ initContainers:   - name: init-container     image: busybox:1.28     command: ['sh', '-c', "sleep 30"]     volumeMounts:       - name: name         mountPath: "/path" |
| deployment.args | list | `[]` | Override default container args |
| deployment.command | list | `[]` | Override default container command |
| deployment.containerSecurityContext | object | `{}` | SecurityContext settings for the main container |
| deployment.enabled | bool | `true` |  |
| deployment.env | object | `{}` | Environment variable to add to the pods |
| deployment.envFrom | list | `[]` | Environment variables from secrets or configmaps to add to the pods |
| deployment.image | object | `{"pullPolicy":"IfNotPresent","repository":"nginx","tag":"latest"}` | Image to use for the chart |
| deployment.image.pullPolicy | string | `"IfNotPresent"` | Image pull policy |
| deployment.image.repository | string | `"nginx"` | Image repository |
| deployment.image.tag | string | `"latest"` | Image tag |
| deployment.imagePullSecrets | list | `[]` | Reference to one or more secrets to be used when pulling images ref: https://kubernetes.io/docs/tasks/configure-pod-container/pull-image-private-registry/ |
| deployment.livenessProbe | object | `{}` | Controller Container liveness probe configuration ref: https://kubernetes.io/docs/tasks/configure-pod-container/configure-liveness-readiness-startup-probes/ |
| deployment.nodeSelector | object | `{}` | Define which Nodes the Pods are scheduled on. |
| deployment.podAnnotations | object | `{}` | Annotations to add to the pod |
| deployment.podSecurityContext | object | `{}` | SecurityContext holds pod-level security attributes and common container settings. This defaults to non root user with uid 1000 and gid 1000. ref: https://kubernetes.io/docs/tasks/configure-pod-container/security-context/ |
| deployment.readinessProbe | object | `{}` | Controller Container readiness probe configuration |
| deployment.replicaCount | int | `1` | Number of replicas for the pod |
| deployment.resources | object | `{}` | Resource limits & requests |
| deployment.startupProbe | object | `{}` | Controller Container startup probe configuration |
| deployment.tolerations | list | `[]` | Tolerations for use with node taints |
| externalSecret.enabled | bool | `false` |  |
| externalSecret.files | string | `nil` |  |
| externalSecret.refreshInterval | string | `"10s"` |  |
| externalSecret.secretStore.name | string | `"example-secret-store"` |  |
| extraLabels | object | `{}` | Additional common labels on resources |
| fullnameOverride | string | `""` | Provide a name to substitute for the full names of resources |
| ingress.annotations | object | `{}` | Annotations to add to the Ingress |
| ingress.className | string | `""` | Ingress class name |
| ingress.enabled | bool | `false` | Enable creation of Ingress |
| ingress.rules | string | `nil` | A list of hosts for the Ingress |
| ingress.tls | string | `nil` | Ingress TLS configuration |
| nameOverride | string | `""` | Provide a name in place of node for `app:` labels |
| namespaceOverride | string | `""` | Provide a name to substitute for the full names of resources |
| podDisruptionBudget | object | `{}` | See `kubectl explain poddisruptionbudget.spec` for more ref: https://kubernetes.io/docs/tasks/run-application/configure-pdb/ |
| privateRegistry | object | `{}` |  |
| secrets | object | `{}` | Creates a secret resource The value must be base64 encoded |
| service.annotations | object | `{}` | Annotations to add to the Service resource |
| service.enabled | bool | `true` |  |
| service.ports | string | `nil` | Ports to expose on the service |
| service.sessionAffinity | string | `""` | Session Affinitiy type |
| service.type | string | `"ClusterIP"` | Service type |
| serviceAccount.additionalLabels | string | `nil` |  |
| serviceAccount.annotations | string | `nil` |  |
| serviceAccount.enabled | bool | `false` |  |
| serviceAccount.name | string | `""` |  |
| serviceMonitor | object | `{"annotations":{},"enabled":false,"endpoints":[{"honorLabels":true,"interval":"1m","path":"/metrics","port":"http","scheme":"http","scrapeTimeout":"30s"}],"targetLabels":[]}` | If enabled, create service monitor of Prometheus-Operator ref: https://github.com/prometheus-operator/prometheus-operator/blob/main/Documentation/user-guides/getting-started.md#include-servicemonitors |
| serviceMonitor.annotations | object | `{}` | Annotations to assign to the ServiceMonitor |
| serviceMonitor.enabled | bool | `false` | Enables Service Monitor |
| serviceMonitor.endpoints | list | `[{"honorLabels":true,"interval":"1m","path":"/metrics","port":"http","scheme":"http","scrapeTimeout":"30s"}]` | List of endpoints of service which Prometheus scrapes |
| serviceMonitor.targetLabels | list | `[]` | Propagate certain service labels to Prometheus. |

----------------------------------------------
Autogenerated from chart metadata using [helm-docs v1.13.1](https://github.com/norwoodj/helm-docs/releases/v1.13.1)
