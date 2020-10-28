apiVersion: v1
name: volume-expander-operator
version: ${version}
appVersion: ${version}
description: Helm chart that deploys volume-expander-operator
keywords:
  - volume
  - storage
  - csi
  - expansion
  - monitoring
sources:
  - https://github.com/redhat-cop/volume-expander-operator
engine: gotpl