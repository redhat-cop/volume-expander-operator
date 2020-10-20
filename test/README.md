# Tests

```shell
export namespace=test-volume-expander-operator
oc new-project ${namespace}
```

## Single Pod Test

```shell
oc apply -f ./test/single_pod.yaml -n ${namespace}
```