# kube-service-exposer

**kube-service-exposer** is a simple TCP proxy that exposes a Kubernetes service on a
port defined by a specific annotation within the given set of host CIDRs.

## Installation

Download the installation manifest:

```bash
kubectl apply -f https://raw.githubusercontent.com/siderolabs/kube-service-exposer/main/deploy/kube-service-exposer.yaml
```

Optionally, download and customize the `args` passed to the container before applying:

```yaml
args:
  - --debug=true
  - --annotation-key=my-annotation-key/port
  - --bind-cidrs=172.20.0.0/24
```

Multiple bind CIDRs can be specified by separating them with commas.
If `--bind-cidrs` are specified, the IP addresses on the hosts will be matched against these CIDRs,
and the Service will be exposed only on the matching addresses.

```bash

## Usage

Add the annotation `kube-service-exposer.sidero.dev/port` to the service you want to expose:

```yaml
apiVersion: v1
kind: Service
metadata:
  name: nginx
  annotations:
    kube-service-exposer.sidero.dev/port: "12345"
spec:
  selector:
    app: nginx
  ports:
    - name: http
      port: 80
      protocol: TCP
```

The service will be exposed on port `12345` on all nodes,
on the IP addresses within the CIDRs specified (all addresses by default).
When the annotation is just a port number, the first TCP service port is used.

The annotation also accepts a comma-separated list of entries.
Each entry is either a bare host port or a `host-port:service-port` pair,
where the service port can be a number or a name:

```yaml
kube-service-exposer.sidero.dev/port: "30080,30443:8080,30444:https"
```

This exposes `30080` (the first TCP port), `30443` (service port `8080`),
and `30444` (the port named `https`).

**Note**: `kube-service-exposer` only works with TCP.
Services without any TCP ports will be ignored.
Non-TCP entries in the annotation are skipped.

## Signing images

Released container images are signed with [cosign](https://docs.sigstore.dev/) keyless.
Talos verifies these signatures via an
[ImageVerificationConfig](https://www.talos.dev/v1.13/security/verifying-image-signatures/).

To sign a release, set `GITHUB_TOKEN` with `write:packages` scope and run:

```bash
GITHUB_TOKEN=$(gh auth token) make sign-images TAG=v0.X.Y
```

A browser opens for Google authentication. To backfill an older release, pass its tag instead.
