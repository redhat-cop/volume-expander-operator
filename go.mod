module github.com/redhat-cop/volume-expander-operator

go 1.13

require (
	github.com/go-logr/logr v0.1.0
	github.com/json-iterator/go v1.1.10
	github.com/onsi/ginkgo v1.12.1
	github.com/onsi/gomega v1.10.1
	github.com/prometheus/client_golang v1.0.0
	github.com/prometheus/common v0.4.1
	go.etcd.io/etcd v0.0.0-20191023171146-3cf2f69b5738
	k8s.io/api v0.18.6
	k8s.io/apimachinery v0.18.6
	k8s.io/client-go v0.18.6
	sigs.k8s.io/controller-runtime v0.6.2
)
