# Ember

A minimal, immutable Kubernetes-node operating system that lives entirely inside
its initrd and never pivots to a mounted root filesystem. Built with Bazel and
[apko](https://github.com/chainguard-dev/apko), designed for network-boot or
local installation on bare-metal or virtual machines.

## Quick start for developers

Build the entire stack:

```bash
bazel build //...
```

| Target | Artifact |
|--------|----------|
| `//image` | Open Container Image layout directory |
| `//os:initrd` | CPIO archive |

## Goals

*   Make deployments as simple as possible.
*   Establish a hardware root of trust.
*   Network boot by default with last-known-good fallback for cold starts.
*   Minize mutability to only the places the OS and Kubernetes runtime need it.
*   Support running with little or no local disks.
*   Use container tools for a reproducible, auditable operating system.

## Non-goals

*   The OS doesn't persist the K8s control plane data. If the whole cluster goes
down at once, it starts back up empty.
*   This OS doesn't support general-purpose use cases. It exists only to host
Kubernetes.

## Boot sequence

1.  Firmware (UEFI or BIOS) loads kernel + initrd.
2.  Kernel unpacks initrd and launches `/init`.
3.  Bring up primary interface via DHCP.
4.  Read the kernel cmdline for `ember.attest_url=https://…`.
5.  Perform TPM remote attestation to fetch a server credential.
6.  Download a sealed configuration overlay TAR using that credential.
7.  Extract the overlay into `/`.
8.  Start `rke2`.
9.  Wait for node to appear in cluster.
    1.  On success, copy the kernel and initrd to the EFI System Partition.
    2.  On failure, reboot, looping until the boot server serves a good
        configuration or becomes unreachable, resulting in a fallback to the
        last-known-good configuration.

10. Notably, don't pivot into a real sysroot. The system runs entirely in its
    initrd.

## Local testing with `QEMU`

```bash
# Build everything
bazel build //...

# Boot
qemu-system-x86_64 -m 2G -kernel <kernel> \
  -initrd bazel-bin/os/initrd \
  -append "console=ttyS0 ember.attest_url=https://attest.example.com"
```

Add `-tpmdev emulator,id=tpm0 -device tpm-tis,tpmdev=tpm0` if you want TPM emulation.

