# Volume Expander Operator

The purpose of the volume-expander-operator is to expand volumes when they are running out of space.
This is achieved by using the [volume expansion feature](https://kubernetes.io/docs/concepts/storage/persistent-volumes/#expanding-persistent-volumes-claims).

The operator periodically checks the `kubelet_volume_stats_used_bytes` and `kubelet_volume_stats_capacity_bytes published` by the kubelets to decide when to expand a volume.
Notice that these metrics are generated only when a volume is mounted to a pod. Also the kubelet takes a minute or two to start generating accurate values for these metrics. The operator accounts for that.

This operator works based on the following annotations to PersistentVolumeClaim resources:

| Annotation | Default  | Description  |
|:-|:-|:-|
| `volume-expander-operator.redhat-cop.io/autoexpand`  | N/A  | if set to "true" enables the volume-expander-operator to watch on this PVC  |
| `volume-expander-operator.redhat-cop.io/polling-frequency`  | `"30s"` | How frequently to poll the volume metrics. Express thi value as a valid golang [Duration](https://golang.org/pkg/time/#ParseDuration)  |
| `volume-expander-operator.redhat-cop.io/expand-threshold-percent` | `"80"` | the percentage of used storage after which the volume will be expanded. This must be a positive integer. |
| `volume-expander-operator.redhat-cop.io/expand-by-percent` | `"25"` | the percentage by which the volume will be expanded, relative to the current size. This must be an integer between 0 and 100 |
| `volume-expander-operator.redhat-cop.io/expand-up-to` | MaxInt64 | the upper bound for this volume to be expanded to. The default value is the largest quantity representable and is intended to be interepdte as infinite. If the default is used it is recommend to ensure the namespace has a quota on the used storage class. |

Note that not all of the storage driver implementations support volume expansion. It is a responsibility of the user/platform administrator to ensure that storage class and the persistent volume claim meet all the requirements needed for the volume expansion feature to work properly.

This operator was tested with [OCS](https://www.redhat.com/en/technologies/cloud-computing/openshift-container-storage), but should work with any other storage driver that supports volume expansion.

## Development

## Running the operator locally

```shell
export token=$(oc whoami -t)
export base_domain=$(oc get dns cluster -o jsonpath='{.spec.baseDomain}')
export prometheus_route=https://prometheus-k8s-openshift-monitoring.apps.${base_domain}
make run ENABLE_WEBHOOKS=false PROMETHEUS_URL=${prometheus_route} TOKEN=${token}
```

## Building/Pushing an image

```shell
export repo=raffaelespazzoli #replace with yours
make docker-build IMG=quay.io/$repo/volume-expander-operator:latest
make docker-push IMG=quay.io/$repo/volume-expander-operator:latest
```

## Deploy via OLM

```shell
operator-sdk-v1.1.0 run bundle --index-image quay.io/$repo/volume-expander-operator:latest --InstallMode OwnNamespace