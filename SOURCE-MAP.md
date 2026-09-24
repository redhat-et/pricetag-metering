# Source Map

This repository is a clean public import of the PriceTag metering service.

## Source

- Source repository: the rewritten PriceTag metering service source
- Source baseline: rewritten public `main` at `14a360b98f507000883aff91b70f4eb621f9b24b`
- Cleanup baseline: the merged Headroom removal cleanup
- New module path: `github.com/redhat-et/pricetag-metering`

## Deliberately Excluded

- Headroom integration and compression dashboard material.
- Legacy IPP/Envoy/Istio dogfood runbooks.
- Credentials, cluster-specific secrets, and personal environment files.
- Partner-owned deployment history.

The deployment and OpenShift composition layer lives in
[`redhat-et/pricetag`](https://github.com/redhat-et/pricetag).
