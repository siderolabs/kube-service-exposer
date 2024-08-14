## [kube-service-exposer 0.2.0](https://github.com/siderolabs/kube-service-exposer/releases/tag/v0.2.0) (2024-08-14)

Welcome to the v0.2.0 release of kube-service-exposer!



Please try out the release binaries and report any issues at
https://github.com/siderolabs/kube-service-exposer/issues.

### Contributors

* Utku Ozdemir

### Changes
<details><summary>1 commit</summary>
<p>

* [`a09759e`](https://github.com/siderolabs/kube-service-exposer/commit/a09759e70e1c326738a2d403f1973b00ee16429c) fix: prevent the goroutine leak from lb health checks
</p>
</details>

### Dependency Changes

* **golang.org/x/sync**               v0.7.0 -> v0.8.0
* **k8s.io/api**                      v0.30.2 -> v0.30.3
* **k8s.io/client-go**                v0.30.3 **_new_**
* **k8s.io/utils**                    fe8a2dddb1d0 -> 18e509b52bc8
* **sigs.k8s.io/controller-runtime**  v0.18.4 -> v0.18.5

Previous release can be found at [v0.1.4](https://github.com/siderolabs/kube-service-exposer/releases/tag/v0.1.4)

## [kube-service-exposer 0.1.3](https://github.com/siderolabs/kube-service-exposer/releases/tag/v0.1.3) (2024-06-20)

Welcome to the v0.1.3 release of kube-service-exposer!



Please try out the release binaries and report any issues at
https://github.com/siderolabs/kube-service-exposer/issues.

### Contributors

* Dmitriy Matrenichev
* Andrey Smirnov
* Artem Chernyshev
* Spencer Smith

### Changes
<details><summary>3 commits</summary>
<p>

* [`03f8b87`](https://github.com/siderolabs/kube-service-exposer/commit/03f8b87d5b3a1c98ff339fa41a0c84ca174b6741) fix: bump healthcheck timeouts to 1 second in the loadbalancer
* [`b26b137`](https://github.com/siderolabs/kube-service-exposer/commit/b26b1374a2eb54ea94fb108540285dd02d8401ea) chore: enable github actions with rekres
* [`a69cf80`](https://github.com/siderolabs/kube-service-exposer/commit/a69cf800dea586ed27c32507d7dba92d919b82d9) chore: add no-op github workflow
</p>
</details>

### Changes from siderolabs/gen
<details><summary>5 commits</summary>
<p>

* [`7654108`](https://github.com/siderolabs/gen/commit/7654108fe6ae15d4765584342709bc0bced6b3d6) chore: add hashtriemap implementation
* [`8485864`](https://github.com/siderolabs/gen/commit/84858640dc9c3032219380885283b995d4f2b0d1) chore: optimize maps.Values and maps.Keys
* [`238baf9`](https://github.com/siderolabs/gen/commit/238baf95e228d40f9f5b765b346688c704052715) chore: add typesafe `SyncMap` and bump stuff
* [`efca710`](https://github.com/siderolabs/gen/commit/efca710d509e6088d7a1a825bd49317df1427639) chore: add `FilterInPlace` method to maps and update module
* [`36a3ae3`](https://github.com/siderolabs/gen/commit/36a3ae312ce03876b2c961a1bcb4ef4c221593d7) feat: update module
</p>
</details>

### Changes from siderolabs/go-loadbalancer
<details><summary>2 commits</summary>
<p>

* [`0639758`](https://github.com/siderolabs/go-loadbalancer/commit/0639758a06785c0c8c65e18774b81d85ab40acdf) chore: bump deps
* [`aab4671`](https://github.com/siderolabs/go-loadbalancer/commit/aab4671fae0d14662a8d7167829c8c6725d28b38) chore: rekres, update dependencies
</p>
</details>

### Changes from siderolabs/go-retry
<details><summary>1 commit</summary>
<p>

* [`23b6fc2`](https://github.com/siderolabs/go-retry/commit/23b6fc21e54e702f324dbdd2576b6c7c60fb7bd5) fix: provider modern error unwrapping
</p>
</details>

### Dependency Changes

* **github.com/go-logr/zapr**                v1.2.4 -> v1.3.0
* **github.com/siderolabs/gen**              v0.4.5 -> v0.5.0
* **github.com/siderolabs/go-loadbalancer**  v0.3.2 -> v0.3.4
* **github.com/siderolabs/go-retry**         v0.3.2 -> v0.3.3
* **github.com/spf13/cobra**                 v1.7.0 -> v1.8.1
* **github.com/stretchr/testify**            v1.8.4 -> v1.9.0
* **go.uber.org/zap**                        v1.24.0 -> v1.27.0
* **golang.org/x/sync**                      v0.3.0 -> v0.7.0
* **k8s.io/api**                             v0.27.3 -> v0.30.2
* **k8s.io/apimachinery**                    v0.27.3 -> v0.30.2
* **k8s.io/utils**                           a36077c30491 -> fe8a2dddb1d0
* **sigs.k8s.io/controller-runtime**         v0.15.0 -> v0.18.4

Previous release can be found at [v0.1.2](https://github.com/siderolabs/kube-service-exposer/releases/tag/v0.1.2)

