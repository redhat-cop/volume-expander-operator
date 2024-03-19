module github.com/redhat-cop/volume-expander-operator

go 1.16

require (
	github.com/go-logr/logr v1.3.0
	github.com/json-iterator/go v1.1.11
	github.com/onsi/ginkgo v1.16.4
	github.com/onsi/gomega v1.32.0
	github.com/prometheus/client_golang v1.11.0
	github.com/prometheus/common v0.26.0
	k8s.io/api v0.21.2
	k8s.io/apimachinery v0.21.2
	k8s.io/client-go v0.21.2
	sigs.k8s.io/controller-runtime v0.9.2
)
