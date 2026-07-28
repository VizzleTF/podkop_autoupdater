# Cutting a release

```sh
git tag -a v0.3.5 -m "..." && git push origin v0.3.5
```

That runs three jobs. `build` compiles a static binary per GOARCH and UPX-compresses
it. `package` stages those into one root filesystem per OpenWrt architecture, builds
apk and ipk packages with [owfeed](https://github.com/VizzleTF/owfeed), and signs a
manifest. `release` uploads the binaries, the packages, the manifest and its
signature to the GitHub release.

The tag becomes the package version. APKv3 wants a package revision, so a tag without
one gets `-r1` appended: `v0.3.5` publishes `0.3.5-r1`. Re-releasing the same upstream
version with a packaging change means tagging `v0.3.5-r2`.

## The signing key

| | |
|---|---|
| key id | `37ddece4c0eef357` |
| public half | [`keys/podkop-updater.pub`](keys/podkop-updater.pub), committed here |
| secret half | GitHub Actions secret `PODKOP_IPD_USIGN_KEY` on this repository |
| local copy | `$PODKOP_IPD_USIGN_KEY` in `~/.zsh_secrets` |
| what it signs | the release manifest, and each package file beside it |
| what it does **not** sign | anything inside a package — see below |

It is a usign key, the signify-style ed25519 scheme OpenWrt already ships in
base-files, because that is what a router can verify with nothing installed.

**It signs manifests, not packages.** The signature inside an apk is a different
scheme (EC prime256v1) and belongs to whoever publishes the feed, because that is the
key a subscriber installs as a trust anchor. This key answers a narrower question —
did the author publish these bytes — and authorises nothing on anybody's router.

**A key per repository, not one shared across them.** A signature says who wrote
something, never what it is about, so one key signing several repositories is exactly
how a manifest lifted from one of them verifies perfectly as another. The manifest
records its `repo` and readers check it, which closes that on its own; a separate key
means the question does not arise, and a compromise here reaches nothing else.

**It cannot be revoked.** apk has no CRL, no expiry and no way to say a key is dead.
So it must never reach a commit — `.gitignore` covers `*.pem`, `*.key` and `*.sec`,
and `owfeed keygen` refuses to write one inside a git working tree without `--force`.

### If it is lost

Generate a new one, publish the new public half here, and ask the feed to update its
pin. Nothing already released stops verifying: the old signatures stay valid against
the old public key, which is still in the git history.

```sh
cd ~/somewhere-outside-any-repo
owfeed keygen --usign --name podkop-updater
gh secret set PODKOP_IPD_USIGN_KEY --repo VizzleTF/podkop_autoupdater < podkop-updater.pem
```

Then copy `podkop-updater.pub` to `keys/` here and to `keys/` in
[VizzleTF/owfeed-packages](https://github.com/VizzleTF/owfeed-packages), and update
`SIG_KEY_ID` in that repository's `packages/podkop-updater/upstream.sh`.

## What the feed does with it

[VizzleTF/owfeed-packages](https://github.com/VizzleTF/owfeed-packages) pins the
public half and the key id. Pinning both matters: the id inside a signature only says
which key to look for, never that the key is the right one.

With the signature verifying, an update to this package in that feed can merge
without a person looking at it, because the provenance comes from somewhere other
than the feed itself. Before this repository signed anything, it could not.

## What is published, and why all of it

| asset | who reads it |
|---|---|
| `podkop_updater-<goarch>` and `.sha256` | `install.sh`, and self-update on routers already running it |
| `podkop-updater-<version>_<arch>.apk` | feeds, for 25.12 and later |
| `podkop-updater_<version>_<arch>.ipk` | feeds, for 24.10 and earlier |
| `manifest.txt` and `manifest.txt.sig` | a feed deciding what belongs to this release |

The bare binaries stay because an installer already on a router looks its asset up by
name and cannot be fixed remotely.

The apk assets carry the architecture in their filename and the packages inside do
not. An apk's name is derived from the package name and version alone — in a feed the
architecture is the directory, so it does not need to be in the name — but release
assets are flat, and twenty architectures would otherwise produce twenty files called
the same thing, nineteen of which would not exist. `owfeed release` renames only
where there is a collision, and records the architecture in the manifest so a feed
publishes each one under the canonical name.
