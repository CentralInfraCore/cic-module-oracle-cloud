# cic-module-oracle-cloud

CIC provisioner-modul **Oracle Cloud Infrastructure (OCI)** célra — az első repo
a `cic-module-<provider>` névtérben. A testvér-modulok (`cic-module-aws`,
`cic-module-azure`, …) ugyanezt a formát követnék.

A [base-repo](https://github.com/CentralInfraCore/base-repo) `wasm/main`
template-jéből származtatva: CIC iSDK guest modul — egy kicsi WASM bináris,
amelyet a relay host (`CIC-Relay/core/cabinet`) [wazero](https://wazero.io)-n át
tölt be, és a `Call` ABI-n keresztül vezérel, kriptográfiailag aláírt release
pipeline-nal.

## Mit csinál

Egy deklarált kívánt OCI-állapotból (szándék) kiindulva az OCI REST API-t hajtja
a kívánt állapot eléréséhez. Mivel a WASM guest sandboxolt — nincs socket, nincs
óra, nincs implicit I/O —, **nem** hívja közvetlenül az OCI-t. A hálózatot és a
titkokat a relay capability-határán kínált **host-függvényeken** át éri el,
amelyek a relay meglévő Rust gépezete mögé kötnek:

| Igény | Mögötte (CIC-Relay) |
|---|---|
| Kimenő HTTP az OCI REST API-hoz | `http-executor` — `reqwest`, `EgressPolicy` host-allowlisttel |
| Kérés-aláírás / secret lekérés | `vault-adapter` — Vault transit + PKI countersign, secret `SecretString`-ként |

Az I/O a host-határon történik, így minden hívás rögzíthető — a modul számítása
a `(bemenő szándék + host-válaszok)` determinisztikus függvénye marad, ami
bizonyíthatóvá teszi a CIC ProofTrace modellben.

## Állapot — scaffold

Ez a template seedje. Ami **még nincs** kész:

- **A relay-oldali híd.** A relay ma csak egy `git` host-modult kínál a WASM
  guesteknek (`cmd/relay/git_host_funcs.go`). A `http-executor` és a
  `vault-adapter` a natív FFI úton él, **nem** wazero host-függvényként. Ezek
  host-függvénnyé emelése — a `git` mintájára — az engedélyező munka, amitől ez
  a modul függ.
- **Az `imports:` szerződés.** Az `abi.schema.yaml` ma csak `exports`-ot ír le.
  Egy host-függvényeket importáló guesthez az import-felületet fel kell venni a
  contractba.
- **Az OCI provisioning-logika** a `module/handlers.go`-ban (vagy Rust
  megfelelőjében).

## Nyitott döntés — Go vagy Rust

A seed a template Go/TinyGo scaffoldját hordozza, ami zölden tartja a CI-t és
demonstrálja a `Call` ABI-t. A végleges nyelv nyitott: a host-függvény ABI a
WASM import-határon van definiálva (`(i32,i32,i32,i32)->i32`, JSON a lineáris
memóriában), nyelvfüggetlenül — egy Rust guest ugyanazt a host-modult
importálhatja. Mindkét opció nyitva marad.

---

Fejlesztéshez és a make-parancsokhoz lásd az angol [README.md](README.md)-t és a
[docs/](docs/) alatti szerződéseket.
