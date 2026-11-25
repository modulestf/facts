# Releases

Each release publishes one binary per platform, `mtf-facts_<tag>_darwin_amd64`, `mtf-facts_<tag>_darwin_arm64`, `mtf-facts_<tag>_linux_amd64` and `mtf-facts_<tag>_linux_arm64`, a `SHA256SUMS` file, and a build provenance attestation for each binary. The build is reproducible: the same tag and the Go version in `.go-version` give the same bytes on any host. [scripts/build-release.sh](../scripts/build-release.sh) fixes the build settings known to change the output: `-trimpath`, an empty build ID, the toolchain, `CGO_ENABLED=0`, `GOFLAGS=-mod=readonly`, `GOWORK=off`, an empty `GOEXPERIMENT`, `GOFIPS140=off` and the architecture levels. Go embeds no build time; the module version comes from the tag.

## Cut a release

Releases follow the [Conventional Commits](https://www.conventionalcommits.org/) in the titles of merged pull requests, which become the commit subjects on `master`. `pr-title-lint` checks each title.

1. A merge to `master` that holds a `feat`, `fix`, `perf` or `revert` commit or a breaking change starts `automated-release`. It bumps `version.json`, adds the new section to [CHANGELOG.md](../CHANGELOG.md) and opens and merges `chore(release): vX.Y.Z`. Other commit types wait for the next releasable one.
2. `tag-release` tags that merged commit with the version.
3. The tag starts the `release` workflow. It builds every binary, attests them and creates the GitHub release with the changelog section as its notes. The build job holds no token that can sign or write. The attestation runs in the `release` environment, so it waits for approval once that environment has reviewers. Only the job that creates the release can write to the repository. A last job asks [modulestf/homebrew-tap](https://github.com/modulestf/homebrew-tap) to update its formula.

The first two steps need the secret `RELEASE_TOKEN` in the `release-automation` environment, which only `master` may use: a fine-grained token of an administrator with contents and pull requests write. A tag or pull request made with the default `GITHUB_TOKEN` starts no workflow, and only administrators may create `v*` tags. The last job needs `HOMEBREW_TAP_TOKEN`, which may run workflows in the tap; without it the tap picks up the release on its daily schedule.

To cut a release by hand, as before, tag a commit on `master` whose `ci` run passed and push the tag. The workflow refuses a tag whose commit is not on `master`. A pre-release tag such as `v0.5.0-rc.1` is published as a pre-release.

```sh
git tag -a v0.5.0 -m v0.5.0 <commit>
git push origin v0.5.0
```

A pushed tag publishes a public release. Do not reuse or move a tag: delete a bad release and cut the next version.

## Verify a release

Check the attestation of a downloaded binary. `-R` alone accepts an attestation from any workflow on any ref of the repository, so also name the release workflow, the tag and GitHub-hosted runners:

```sh
gh attestation verify mtf-facts_v0.1.0_linux_amd64 -R modulestf/facts \
  --signer-workflow modulestf/facts/.github/workflows/release.yml \
  --source-ref refs/tags/v0.1.0 \
  --deny-self-hosted-runners
```

Check its SHA-256 against the release. On Linux:

```sh
sha256sum --ignore-missing -c SHA256SUMS
```

On macOS:

```sh
shasum -a 256 --ignore-missing -c SHA256SUMS
```

Rebuild the release from source and compare every SHA-256 with the published one. This needs `git`, `curl` and Go; a different local Go downloads the release's toolchain.

```sh
make release-verify TAG=v0.1.0
```

## Update a consumer pin

A consumer, such as the verify job of a hosted review workflow, pins one version and one SHA-256 per platform next to the step that downloads `mtf-facts`. To move the pin:

1. Verify the new release as above: the attestation and the rebuild.
2. Read the SHA-256 of each platform the consumer runs on from that release's `SHA256SUMS`.
3. Change the version and every SHA-256 in one commit. A version without its matching SHA-256 values fails the download check, as intended.
4. Keep the attestation check in the download step, with the same flags as above and the pinned tag, so the pin rests on both the hash and the provenance:

   ```sh
   gh attestation verify "mtf-facts_${VERSION}_linux_amd64" -R modulestf/facts \
     --signer-workflow modulestf/facts/.github/workflows/release.yml \
     --source-ref "refs/tags/${VERSION}" \
     --deny-self-hosted-runners
   ```
