# Notices for components inside `taglib.wasm`

`go.senan.xyz/taglib` v0.14.0 embeds a prebuilt WebAssembly module,
`taglib.wasm`, that is linked into the Rewind binary. It was built with
wasi-sdk 27 (see go-taglib's `build/Dockerfile`) and statically contains the
components below. Their license texts were fetched once from the upstream
repositories at the exact commits used, and are shipped in release archives
under `LICENSES/go.senan.xyz/taglib/wasm/`.

| File | Component | Upstream source (exact commit) | License |
| --- | --- | --- | --- |
| `taglib-COPYING.LGPL`, `taglib-COPYING.MPL` | TagLib 2.1.1 | taglib/taglib `7d86716194777e0294453bfdc9dd170bd033e1f4` (the `v2.1.1` tag; the commit go-taglib v0.14.0 pins as a submodule) | LGPL-2.1 or MPL-1.1, used under LGPL-2.1 |
| `utfcpp-LICENSE` | utfcpp (TagLib's bundled UTF-8 library) | nemtrif/utfcpp `df857efc5bbc2aa84012d865f7d7e9cccdc08562` (TagLib 2.1.1 submodule) | BSL-1.0 |
| `wasi-libc-LICENSE`, `wasi-libc-LICENSE-MIT` | wasi-libc | WebAssembly/wasi-libc `3f7eb4c7d6ede4dde3c4bffa6ed14e8d656fe93f` (wasi-sdk-27 submodule) | Apache-2.0 WITH LLVM-exception OR Apache-2.0 OR MIT |
| `musl-COPYRIGHT` | musl code within wasi-libc | same wasi-libc commit, `libc-top-half/musl/COPYRIGHT` | MIT |
| `libcxx-LICENSE.TXT` | LLVM libc++ | llvm/llvm-project `87f0227cb60147a26a1eeb4fb06e3b505e9c7261` (wasi-sdk-27 submodule) | Apache-2.0 WITH LLVM-exception |

These files must be re-checked whenever the go-taglib version changes;
`scripts/lgpl-sources.sh` refuses to run for any go-taglib version other than
the reviewed one.
