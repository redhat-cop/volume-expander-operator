## Running the operator locally

```shell
export token=$(oc whoami -t)
export base_domain=$(oc get dns cluster -o jsonpath='{.spec.baseDomain}')
export prometheus_route=https://prometheus-k8s-openshift-monitoring.apps.${base_domain}
make run ENABLE_WEBHOOKS=false PROMETHEUS_URL=${prometheus_route} TOKEN=${token}
```