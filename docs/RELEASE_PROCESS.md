# Maintainer release process

This document describes the safe publication process for Minimal Router OS. It
is intentionally conservative because a repository can contain sensitive
information outside the visible `main` tree, including old commits, pull request
refs, issue discussions, workflow logs, and artifacts.

## Core rule

Never make an original private development repository public when it contains
history or metadata that was not reviewed for disclosure.

Rewriting `main` alone does not guarantee that old pull request refs, issues,
comments, workflow logs, or artifacts disappear. The safe release starts from a
brand-new repository whose initial tree is a reviewed export while the original
development repository remains a private archive.

The new clean repository may accumulate additional reviewed commits while it is
still private. Every commit in that repository must remain inside the public
boundary and the full clean-repository history must pass secret scanning before
visibility changes. The requirement is **no inherited private history**, not that
normal public development must remain permanently limited to one commit.

## Before the release session

The following must already be true:

- the complete private development history is mirrored and independently
  verified;
- the checked-out source ref contains the reviewed public tree;
- standard CI and the current-tree secret scan pass;
- CodeQL analysis completes while the repository is private;
- runtime state, credentials, snapshots, backups, packet captures, local
  environment files, and internal handoff notes are absent;
- every screenshot and example uses synthetic data;
- any credential that appeared in private history has been rotated.

## Create the initial clean candidate locally

Check out the exact reviewed commit, verify that the working tree is clean, and
run:

```sh
git status --short
git rev-parse HEAD
sh scripts/prepare-public-root.sh HEAD /tmp/minimalrouter-public-root
```

An explicit reviewed branch, tag, or commit SHA may be supplied instead of
`HEAD`.

The script:

- exports only the selected source tree;
- removes the private staging checklist when present;
- rejects known internal/runtime paths and suspicious secret-bearing files;
- initializes a new repository with exactly one root commit;
- verifies that it has no tags or remotes;
- runs a full-history Gitleaks scan;
- never pushes, renames, changes visibility, or publishes anything.

Review the result:

```sh
git -C /tmp/minimalrouter-public-root log --oneline --decorate --all
git -C /tmp/minimalrouter-public-root ls-files
git -C /tmp/minimalrouter-public-root status --short
git -C /tmp/minimalrouter-public-root remote -v
```

Expected state: one initial commit, a clean working tree, and no remote.

## Owner-reviewed GitHub cutover

Perform these steps together with the repository owner. Keep every repository
private until the final visibility step.

1. Reconfirm the private mirrors and record their current commit IDs.
2. Rename the original private repository to a clearly private archive name.
3. Confirm that the renamed archive is still private and still contains the old
   pull requests, issues, workflow history, branches, and tags.
4. Create a new **private**, empty repository with the intended public name. Do
   not add a README, license, or `.gitignore` from GitHub's creation screen.
5. Add that new private repository as the only remote of the clean candidate and
   push its single `main` root commit.
6. Confirm on GitHub that the new repository has no inherited tags, old pull
   requests/issues, or unexpected Actions artifacts.
7. Perform any further public-only cleanup or documentation work through normal
   reviewed commits in the new clean repository.
8. Wait for CI, CodeQL analysis, current-tree scanning, and a full-history secret
   scan of the **entire clean repository** to pass.
9. Review the rendered README, screenshot, license, security policy, privacy
   policy, support policy, comparison text, installation warnings, governance,
   and changelog directly on GitHub.
10. Complete the repository settings checklist below.
11. Change visibility to public only after the owner gives an explicit final
    approval.
12. Re-run CodeQL after publication so its SARIF results upload to GitHub Code
    Scanning, then confirm the Security tab and all status badges are healthy.

Reusing the old repository name disables GitHub's automatic redirect from the
renamed archive. That is intentional: the public URL must resolve to the new
clean repository while the historical repository remains private.

## Repository settings checklist

Before visibility changes, configure or review:

- **Description:** `Minimal Alpine Linux router appliance with a Go control plane and React dashboard.`
- **Topics:** `router`, `firewall`, `alpine-linux`, `golang`, `react`, `nftables`,
  `wireguard`, `pppoe`, `homelab`, `networking`.
- **Default branch:** `main`.
- **Merge policy:** prefer squash merge for ordinary pull requests; disable
  unused merge methods when the team agrees.
- **Branch ruleset:** require pull requests, CI, CodeQL/required security checks,
  resolved conversations, and no force pushes or branch deletion.
- **Actions:** default workflow token permission should be read-only; grant write
  permissions only per workflow when required.
- **Security:** enable dependency graph, Dependabot alerts and security updates,
  secret scanning/push protection where available, code scanning, and private
  vulnerability reporting.
- **Issues:** enable issue templates and confirm the private-security contact link.
- **Discussions:** enable only when there is capacity to moderate and support it.
- **Releases:** restrict release creation to trusted maintainers and never publish
  unsigned production claims.
- **Social preview:** use a synthetic project image with no network data.
- **Website:** leave empty until an official project site exists.

GitHub reference:

- https://docs.github.com/en/repositories/creating-and-managing-repositories/renaming-a-repository
- https://docs.github.com/en/repositories/managing-your-repositorys-settings-and-features/managing-repository-settings/setting-repository-visibility
- https://docs.github.com/en/repositories/configuring-branches-and-merges-in-your-repository/managing-rulesets
- https://docs.github.com/en/code-security

## Final public verification

After visibility changes, verify from a signed-out browser or unrelated GitHub
account:

- the repository opens without authentication;
- the README logo, screenshot, badges, Mermaid diagram, and internal links render;
- `LICENSE`, `SECURITY.md`, `CONTRIBUTING.md`, `CODE_OF_CONDUCT.md`,
  `GOVERNANCE.md`, `PRIVACY.md`, and `SUPPORT.md` are discoverable;
- issue templates load and the vulnerability-reporting path is private;
- Actions, CodeQL, Dependabot, and secret-scanning status are healthy;
- clone and build instructions work from a fresh directory;
- no releases, packages, artifacts, branches, tags, issues, or discussions expose
  private development information.

## Local remote cleanup after cutover

Existing local clones may still point at the old URL. Do not assume any remote is
correct after a rename/recreation sequence.

For each private checkout, explicitly set `origin` to its intended private
repository URL, which should be stored outside this public source tree:

```sh
git remote set-url origin <PRIVATE_REPOSITORY_URL>
git remote -v
```

For a fresh public checkout, use the URL shown on the new public repository's
GitHub page.

Keep private and public development in separate local directories to reduce the
chance of pushing a household-specific change to the public project.

## Rollback

Until visibility is changed, rollback is simple:

- leave the renamed archive private and untouched;
- keep the new clean repository private;
- delete and recreate only the new clean repository if its metadata or history
  is not exactly as expected;
- rebuild the initial candidate from the reviewed cleanup branch;
- rerun all checks before resuming.

After publication, do not rewrite released public history casually. Revoke and
rotate any exposed credential immediately, document the incident, and follow the
security response process in `SECURITY.md`.